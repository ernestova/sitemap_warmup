// Package verify turns observations into normalized states and schedules
// requests until every URL and profile reaches a final state.
package verify

import (
	"net/http"
	"strings"

	"github.com/clarivo-co/sitewarm/internal/cdn"
	"github.com/clarivo-co/sitewarm/internal/probe"
)

// Verdict is the state of one observation and why.
type Verdict struct {
	State  cdn.State
	CDN    cdn.Result
	Detail string
}

// Classify assigns a normalized state to one observation.
func Classify(obs probe.Observation) Verdict {
	switch {
	case obs.Err != nil:
		return Verdict{State: cdn.Error, Detail: obs.Err.Error()}
	case obs.RedirectError != "":
		return Verdict{State: cdn.RedirectError, Detail: obs.RedirectError}
	}
	res := cdn.Classify(obs.Header)
	switch {
	case obs.Status == http.StatusForbidden || strings.EqualFold(obs.Header.Get("Cf-Mitigated"), "challenge"):
		return Verdict{State: cdn.Challenged, CDN: res, Detail: http.StatusText(obs.Status)}
	case obs.Status >= 500:
		return Verdict{State: cdn.Error, CDN: res, Detail: http.StatusText(obs.Status)}
	case obs.Status >= 400:
		return Verdict{State: cdn.ClientError, CDN: res, Detail: http.StatusText(obs.Status)}
	case obs.Status >= 300:
		// Redirects Do does not follow (300, 304, 305...).
		return Verdict{State: cdn.RedirectError, CDN: res, Detail: http.StatusText(obs.Status)}
	case obs.Status < 200:
		return Verdict{State: cdn.Unknown, CDN: res, Detail: http.StatusText(obs.Status)}
	}

	v := Verdict{State: res.State, CDN: res}
	if v.State == cdn.Miss && hasDirective(obs.Header.Values("Cache-Control"), "private", "no-store") {
		v.State = cdn.Bypass
		v.Detail = "Cache-Control: " + strings.Join(obs.Header.Values("Cache-Control"), ", ")
	}
	switch v.State {
	case cdn.Hit, cdn.Stale, cdn.Miss:
		// The cache key depends on the User-Agent, which the profile cannot
		// reproduce for real users.
		if hasDirective(obs.Header.Values("Vary"), "user-agent") {
			v.State = cdn.VariantUnverifiable
			v.Detail = "Vary: " + strings.Join(obs.Header.Values("Vary"), ", ")
		}
	}
	return v
}

func hasDirective(values []string, names ...string) bool {
	for _, v := range values {
		for _, d := range strings.Split(v, ",") {
			d, _, _ = strings.Cut(d, "=")
			d = strings.ToLower(strings.TrimSpace(d))
			for _, n := range names {
				if d == n {
					return true
				}
			}
		}
	}
	return false
}

// Terminal reports whether a state ends the retries for its URL and profile.
// Miss and stale are retried until --max-wait; error up to MaxErrorRetries.
func Terminal(s cdn.State) bool {
	switch s {
	case cdn.Miss, cdn.Stale, cdn.Error:
		return false
	}
	return true
}
