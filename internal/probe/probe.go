// Package probe sends one request for a URL with a request profile, follows
// same-site redirects, measures TTFB and reads the body.
package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/clarivo-co/sitewarm/internal/cdn"
)

// DefaultRequestTimeout is the deadline for each request, body included.
const DefaultRequestTimeout = 30 * time.Second

// MaxRedirects is how many redirects are followed before redirect-error.
const MaxRedirects = 5

// ErrCookieHostsRequired is returned when a profile has cookies but no host
// was allowed to receive them.
var ErrCookieHostsRequired = errors.New("a profile has cookies: --cookie-hosts is required")

// Config configures a Prober.
type Config struct {
	// CookieHosts are the only hostnames that receive profile cookies.
	CookieHosts []string
	// RequestTimeout defaults to DefaultRequestTimeout.
	RequestTimeout time.Duration
	// Transport defaults to a clone of http.DefaultTransport.
	Transport http.RoundTripper
}

// Prober sends requests. It is safe for concurrent use.
type Prober struct {
	client      *http.Client
	timeout     time.Duration
	cookieHosts map[string]bool
}

// New validates the configuration against the profiles that will be used.
func New(cfg Config, profiles []Profile) (*Prober, error) {
	hosts := map[string]bool{}
	for _, h := range cfg.CookieHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts[h] = true
		}
	}
	for _, p := range profiles {
		if p.Cookie != "" && len(hosts) == 0 {
			return nil, ErrCookieHostsRequired
		}
	}
	transport := cfg.Transport
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		// Accept-Encoding is set by the profile; bodies are decoded here.
		t.DisableCompression = true
		t.MaxIdleConnsPerHost = 16
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		transport = t
	}
	timeout := cfg.RequestTimeout
	if timeout == 0 {
		timeout = DefaultRequestTimeout
	}
	return &Prober{
		client: &http.Client{
			Transport: transport,
			// Redirects are followed by Do so cookies and hosts can be checked
			// on every hop.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		timeout:     timeout,
		cookieHosts: hosts,
	}, nil
}

// Hop is one redirect response in a chain.
type Hop struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
}

// Observation is the outcome of one request (one attempt), redirects included.
type Observation struct {
	Start    time.Time
	URL      string
	FinalURL string
	// Redirects lists the redirect responses before the final one.
	Redirects []Hop
	// RedirectError is set for loops, more than MaxRedirects hops, or a hop
	// to another site.
	RedirectError string
	Status        int
	Header        http.Header
	// TTFB runs from the request being written to the first response byte
	// of the final hop; it excludes DNS, TCP and TLS.
	TTFB               time.Duration
	HasTTFB            bool
	Indexability       Indexability
	IndexabilityDetail string
	WireBytes          int64
	Err                error
}

func isRedirect(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// Do requests rawURL with the profile, following up to MaxRedirects
// same-site redirects.
func (p *Prober) Do(ctx context.Context, rawURL string, prof Profile) Observation {
	obs := Observation{Start: time.Now(), URL: rawURL}
	start, err := url.Parse(rawURL)
	if err != nil {
		obs.Err = err
		return obs
	}
	current := start
	seen := map[string]bool{start.String(): true}
	for {
		obs.FinalURL = current.String()
		r := p.fetch(ctx, current, prof)
		obs.Status, obs.Header, obs.Err = r.status, r.header, r.err
		obs.TTFB, obs.HasTTFB = r.ttfb, r.hasTTFB
		obs.Indexability, obs.IndexabilityDetail = r.body.Indexability, r.body.Detail
		obs.WireBytes = r.body.WireBytes
		if obs.Err != nil || !isRedirect(r.status) {
			return obs
		}
		obs.Redirects = append(obs.Redirects, Hop{URL: current.String(), Status: r.status})
		loc := r.header.Get("Location")
		if loc == "" {
			obs.RedirectError = fmt.Sprintf("%d without Location", r.status)
			return obs
		}
		next, err := current.Parse(loc)
		if err != nil {
			obs.RedirectError = "invalid Location: " + err.Error()
			return obs
		}
		next.Fragment = ""
		switch {
		case next.Scheme != "http" && next.Scheme != "https":
			obs.RedirectError = "redirect to unsupported scheme " + next.Scheme
		case !SameSite(start, next):
			obs.RedirectError = "redirect to another host: " + next.Host
		case seen[next.String()]:
			obs.RedirectError = "redirect loop at " + next.String()
		case len(obs.Redirects) > MaxRedirects:
			obs.RedirectError = fmt.Sprintf("more than %d redirects", MaxRedirects)
		}
		if obs.RedirectError != "" {
			obs.FinalURL = next.String()
			return obs
		}
		seen[next.String()] = true
		current = next
	}
}

// SameSite reports whether two URLs are on the same host, counting the apex
// and its www as one.
func SameSite(a, b *url.URL) bool {
	norm := func(u *url.URL) string {
		return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	}
	return norm(a) == norm(b)
}

type fetchResult struct {
	status  int
	header  http.Header
	ttfb    time.Duration
	hasTTFB bool
	body    bodyResult
	err     error
}

func (p *Prober) fetch(ctx context.Context, u *url.URL, prof Profile) fetchResult {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	// The transport may call the trace hooks from its own goroutines.
	var mu sync.Mutex
	var wrote, firstByte time.Time
	mark := func(t *time.Time) {
		mu.Lock()
		*t = time.Now()
		mu.Unlock()
	}
	trace := &httptrace.ClientTrace{
		WroteRequest:         func(httptrace.WroteRequestInfo) { mark(&wrote) },
		GotFirstResponseByte: func() { mark(&firstByte) },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, u.String(), nil)
	if err != nil {
		return fetchResult{err: err}
	}
	req.Header = prof.Header.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Header.Set("Pragma", cdn.AkamaiPragma)
	if prof.Cookie != "" && u.Scheme == "https" && p.cookieHosts[strings.ToLower(u.Hostname())] {
		req.Header.Set("Cookie", prof.Cookie)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fetchResult{err: err}
	}
	defer resp.Body.Close()
	r := fetchResult{status: resp.StatusCode, header: resp.Header}
	mu.Lock()
	if !wrote.IsZero() && !firstByte.IsZero() {
		r.ttfb, r.hasTTFB = firstByte.Sub(wrote), true
	}
	mu.Unlock()
	analyze := resp.StatusCode >= 200 && resp.StatusCode < 300
	r.body = readBody(resp, analyze)
	if r.body.Err != nil {
		// A body that cannot be fully downloaded within the deadline is an
		// error: the CDN may not have cached it.
		r.err = r.body.Err
	}
	return r
}
