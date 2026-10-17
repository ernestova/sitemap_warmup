// Package sitewarm warms a list of URLs through a CDN and verifies, per URL
// and request profile, that the CDN serves them from cache.
//
// It is the only public package of the module; everything else is internal.
package sitewarm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/clarivo-co/sitewarm/internal/cdn"
	"github.com/clarivo-co/sitewarm/internal/probe"
	"github.com/clarivo-co/sitewarm/internal/report"
	"github.com/clarivo-co/sitewarm/internal/source"
	"github.com/clarivo-co/sitewarm/internal/verify"
)

// Version is set at build time.
var Version = "dev"

// State is the normalized cache state of one URL and profile.
type State = cdn.State

// Normalized states.
const (
	Hit                 = cdn.Hit
	Stale               = cdn.Stale
	Miss                = cdn.Miss
	Bypass              = cdn.Bypass
	VariantUnverifiable = cdn.VariantUnverifiable
	Challenged          = cdn.Challenged
	ClientError         = cdn.ClientError
	Error               = cdn.Error
	RedirectError       = cdn.RedirectError
	Incomplete          = cdn.Incomplete
	Unknown             = cdn.Unknown
	SkippedRobots       = cdn.SkippedRobots
)

// Result is the scan report.
type Result = report.Report

// Defaults documented for the CLI.
const (
	DefaultTop           = 50
	DefaultConcurrency   = 4
	DefaultRPS           = 5
	DefaultRetryInterval = 10 * time.Second
	DefaultMaxWait       = 5 * time.Minute
	DefaultRunTimeout    = 15 * time.Minute
	DefaultFailOn        = report.DefaultFailOn
)

// Options configures a scan.
type Options struct {
	// TopFrom is the path of the CSV with the URLs, in priority order.
	TopFrom string
	// FromSitemap is the URL of a sitemap or sitemap index. It excludes
	// TopFrom. Its URLs are checked against robots.txt unless IgnoreRobots.
	FromSitemap  string
	IgnoreRobots bool
	// BaseURL resolves a CSV "path" column.
	BaseURL string
	// Top keeps the first N URLs of the list.
	Top int
	// Profiles defaults to the desktop and mobile Chrome presets.
	Profiles []string
	// AcceptLanguage overrides the preset's Accept-Language.
	AcceptLanguage string
	// Cookies maps a profile name to its Cookie header value.
	Cookies map[string]string
	// CookieHosts are the only hosts that receive cookies, over HTTPS only.
	CookieHosts []string

	Concurrency   int
	RPS           float64
	RetryInterval time.Duration
	MaxWait       time.Duration
	RunTimeout    time.Duration
	// FailOn is a comma-separated --fail-on value.
	FailOn string

	// RequestTimeout defaults to 30s; Transport to a tuned clone of
	// http.DefaultTransport. Both exist for tests and embedding.
	RequestTimeout time.Duration
	Transport      http.RoundTripper
}

// DefaultOptions returns the documented defaults.
func DefaultOptions() Options {
	return Options{
		Top:           DefaultTop,
		Profiles:      []string{"desktop", "mobile"},
		Concurrency:   DefaultConcurrency,
		RPS:           DefaultRPS,
		RetryInterval: DefaultRetryInterval,
		MaxWait:       DefaultMaxWait,
		RunTimeout:    DefaultRunTimeout,
		FailOn:        DefaultFailOn,
	}
}

// ErrConfig wraps configuration errors detected before any request.
var ErrConfig = errors.New("invalid configuration")

// ErrNotImplemented is returned by features planned for a later release.
var ErrNotImplemented = errors.New("not implemented yet")

func configErr(err error) error { return fmt.Errorf("%w: %w", ErrConfig, err) }

// Scan warms and verifies every URL with every profile. The returned Result
// is complete even when ctx is canceled or the run times out: unfinished
// results are incomplete and Result.Stopped says why. Errors are returned
// only for invalid configuration.
func Scan(ctx context.Context, opts Options) (*Result, error) {
	failOn, err := report.ParseFailOn(opts.FailOn)
	if err != nil {
		return nil, configErr(err)
	}
	if (opts.TopFrom == "") == (opts.FromSitemap == "") {
		return nil, configErr(ErrSource)
	}
	profiles, err := selectProfiles(opts)
	if err != nil {
		return nil, configErr(err)
	}
	fetcher := source.Fetcher{
		Client:    sourceClient(opts),
		UserAgent: profiles[0].Header.Get("User-Agent"),
	}

	var urls []string
	src := opts.TopFrom
	if opts.TopFrom != "" {
		f, err := os.Open(opts.TopFrom)
		if err != nil {
			return nil, configErr(err)
		}
		urls, err = source.ReadCSV(f, opts.BaseURL)
		f.Close()
		if err != nil {
			return nil, configErr(fmt.Errorf("%s: %w", opts.TopFrom, err))
		}
	} else {
		src = opts.FromSitemap
		if urls, err = fetcher.ReadSitemap(ctx, opts.FromSitemap, opts.Top); err != nil {
			return nil, configErr(err)
		}
	}
	if opts.Top > 0 && len(urls) > opts.Top {
		urls = urls[:opts.Top]
	}

	// robots.txt is consulted only for sitemap URLs: a --top-from list is
	// chosen explicitly by its operator.
	skip := map[string]bool{}
	var sourceWarnings []string
	if opts.FromSitemap != "" && !opts.IgnoreRobots {
		robots := fetcher.NewRobots()
		for _, u := range urls {
			ok, err := robots.Allowed(ctx, u)
			if err != nil || !ok {
				skip[u] = true
			}
		}
		sourceWarnings = robots.Warnings()
	}
	prober, err := probe.New(probe.Config{
		CookieHosts:    opts.CookieHosts,
		RequestTimeout: opts.RequestTimeout,
		Transport:      opts.Transport,
	}, profiles)
	if err != nil {
		return nil, configErr(err)
	}

	// all keeps the list order; only the pairs not skipped are requested.
	all := make([]verify.Pair, 0, len(urls)*len(profiles))
	var pairs []verify.Pair
	for _, u := range urls {
		for _, p := range profiles {
			all = append(all, verify.Pair{URL: u, Profile: p})
			if !skip[u] {
				pairs = append(pairs, verify.Pair{URL: u, Profile: p})
			}
		}
	}

	res := &Result{
		Version:   Version,
		StartedAt: time.Now(),
		Warnings:  sourceWarnings,
		Settings: report.Settings{
			Source:        src,
			IgnoreRobots:  opts.FromSitemap != "" && opts.IgnoreRobots,
			Top:           opts.Top,
			Concurrency:   opts.Concurrency,
			RPS:           opts.RPS,
			RetryInterval: opts.RetryInterval.String(),
			MaxWait:       opts.MaxWait.String(),
			RunTimeout:    opts.RunTimeout.String(),
		},
	}
	for _, p := range profiles {
		res.Settings.Profiles = append(res.Settings.Profiles, p.Name)
	}
	outcomes, stopped := verify.Run(ctx, verify.Config{
		Concurrency:     opts.Concurrency,
		RPS:             opts.RPS,
		RetryInterval:   opts.RetryInterval,
		MaxWait:         opts.MaxWait,
		RunTimeout:      opts.RunTimeout,
		MaxErrorRetries: verify.DefaultMaxErrorRetries,
	}, pairs, func(ctx context.Context, p verify.Pair) probe.Observation {
		return prober.Do(ctx, p.URL, p.Profile)
	})
	res.FinishedAt = time.Now()
	switch {
	case stopped == nil:
	case errors.Is(stopped, context.DeadlineExceeded) && ctx.Err() == nil:
		res.Stopped = "run-timeout"
	default:
		res.Stopped = "canceled"
	}
	merged := make([]verify.Outcome, 0, len(all))
	next := 0
	for _, p := range all {
		if skip[p.URL] {
			merged = append(merged, report.SkippedOutcome(p))
			continue
		}
		merged = append(merged, outcomes[next])
		next++
	}
	report.Build(res, merged, failOn)
	return res, nil
}

// ErrSource means zero or several URL sources were given.
var ErrSource = errors.New("choose exactly one URL source: --top-from <file.csv> or --from-sitemap <url>")

// sourceClient fetches sitemaps and robots.txt. Unlike the probe, it lets
// Go decode Content-Encoding gzip.
func sourceClient(opts Options) *http.Client {
	timeout := opts.RequestTimeout
	if timeout == 0 {
		timeout = probe.DefaultRequestTimeout
	}
	transport := opts.Transport
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func selectProfiles(opts Options) ([]probe.Profile, error) {
	presets := probe.Presets(Version, opts.AcceptLanguage)
	names := opts.Profiles
	if len(names) == 0 {
		names = []string{"desktop", "mobile"}
	}
	var out []probe.Profile
	for _, n := range names {
		found := false
		for _, p := range presets {
			if p.Name == n {
				p.Cookie = opts.Cookies[n]
				out = append(out, p)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown profile %q (available: desktop, mobile)", n)
		}
	}
	return out, nil
}

// Doctor diagnoses a single URL per profile. It is planned for a later
// release and currently returns ErrNotImplemented.
func Doctor(ctx context.Context, url string, opts Options) (*Result, error) {
	return nil, ErrNotImplemented
}
