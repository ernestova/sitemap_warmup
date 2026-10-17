// Package cdn maps the cache headers of each supported CDN to a normalized
// cache state and extracts the PoP that served the response.
package cdn

import (
	"net/http"
	"strings"
)

// State is the normalized outcome for one URL and profile.
type State string

const (
	Hit                 State = "hit"
	Stale               State = "stale"
	Miss                State = "miss"
	Bypass              State = "bypass"
	VariantUnverifiable State = "variant-unverifiable"
	Challenged          State = "challenged"
	ClientError         State = "client-error"
	Error               State = "error"
	Incomplete          State = "incomplete"
	Unknown             State = "unknown"
	RedirectError       State = "redirect-error"
	// SkippedRobots is a sitemap URL that robots.txt disallows, or whose
	// robots.txt could not be read. It is never requested.
	SkippedRobots State = "skipped-robots"
)

// States lists every normalized state, in report order.
var States = []State{
	Hit, Stale, Miss, Bypass, VariantUnverifiable, Challenged,
	ClientError, Error, RedirectError, Incomplete, Unknown, SkippedRobots,
}

// CDN names.
const (
	Cloudflare = "cloudflare"
	CloudFront = "cloudfront"
	Fastly     = "fastly"
	Akamai     = "akamai"
	Vercel     = "vercel"
)

// Detection is what one CDN reported for a response.
type Detection struct {
	CDN   string `json:"cdn"`
	State State  `json:"state"`
	// Raw is the header value the state was read from, unchanged.
	Raw string `json:"raw"`
	POP string `json:"pop,omitempty"`
}

// Result is the classification of a response's cache headers.
type Result struct {
	Detection
	// Others holds the remaining CDNs found in the same response when CDNs
	// are stacked; they never decide the state.
	Others []Detection `json:"others,omitempty"`
}

// AkamaiPragma is sent on every request so Akamai exposes its debug headers.
const AkamaiPragma = "akamai-x-cache-on, akamai-x-get-cache-key"

// detectors run in precedence order: the outermost CDN decides the state
// when several are stacked. Vercel goes last because it is usually the
// origin behind another CDN.
var detectors = []func(http.Header) (Detection, bool){
	cloudflare, cloudfront, fastly, akamai, vercel,
}

// Classify returns the cache state of a response. With no recognizable CDN
// header the state is Unknown and CDN is empty. Any value outside the
// mapping table is Unknown with the raw value kept.
func Classify(h http.Header) Result {
	var res Result
	found := false
	for _, detect := range detectors {
		d, ok := detect(h)
		if !ok {
			continue
		}
		if !found {
			res.Detection = d
			found = true
			continue
		}
		res.Others = append(res.Others, d)
	}
	if !found {
		res.State = Unknown
	}
	return res
}

func lookup(table map[string]State, v string) State {
	if s, ok := table[strings.ToUpper(strings.TrimSpace(v))]; ok {
		return s
	}
	return Unknown
}

var cloudflareStates = map[string]State{
	"HIT": Hit, "STALE": Stale, "UPDATING": Stale, "REVALIDATED": Stale,
	"MISS": Miss, "EXPIRED": Miss, "BYPASS": Bypass, "DYNAMIC": Bypass,
}

func cloudflare(h http.Header) (Detection, bool) {
	raw := h.Get("Cf-Cache-Status")
	ray := h.Get("Cf-Ray")
	if raw == "" && ray == "" {
		return Detection{}, false
	}
	d := Detection{CDN: Cloudflare, Raw: raw, State: Unknown}
	if raw != "" {
		d.State = lookup(cloudflareStates, raw)
	}
	// cf-ray: "8c1d2e3f4a5b6c7d-SJC"
	if i := strings.LastIndexByte(ray, '-'); i >= 0 {
		d.POP = ray[i+1:]
	}
	return d, true
}

var cloudfrontStates = map[string]State{
	"HIT FROM CLOUDFRONT": Hit, "REFRESHHIT FROM CLOUDFRONT": Hit,
	"MISS FROM CLOUDFRONT": Miss,
}

func cloudfront(h http.Header) (Detection, bool) {
	raw := h.Get("X-Cache")
	pop := h.Get("X-Amz-Cf-Pop")
	isCF := strings.Contains(strings.ToLower(raw), "cloudfront")
	if !isCF && pop == "" && h.Get("X-Amz-Cf-Id") == "" {
		return Detection{}, false
	}
	d := Detection{CDN: CloudFront, POP: pop, State: Unknown}
	if isCF {
		d.Raw = raw
		d.State = lookup(cloudfrontStates, raw)
	}
	return d, true
}

var fastlyStates = map[string]State{"HIT": Hit, "MISS": Miss}

func fastly(h http.Header) (Detection, bool) {
	servedBy := h.Get("X-Served-By")
	isFastly := strings.HasPrefix(strings.ToLower(servedBy), "cache-") ||
		h.Get("X-Fastly-Request-Id") != "" || h.Get("Fastly-Debug-Path") != ""
	if !isFastly {
		return Detection{}, false
	}
	raw := h.Get("X-Cache")
	d := Detection{CDN: Fastly, Raw: raw, State: Unknown}
	// "MISS, HIT": shield first, then the edge that answered. Only the last
	// value counts.
	if raw != "" {
		d.State = lookup(fastlyStates, lastItem(raw))
	}
	if servedBy != "" {
		d.POP = lastItem(servedBy)
	}
	return d, true
}

var akamaiStates = map[string]State{
	"TCP_HIT": Hit, "TCP_MEM_HIT": Hit, "TCP_MISS": Miss, "TCP_REFRESH_HIT": Stale,
}

func akamai(h http.Header) (Detection, bool) {
	raw := h.Get("X-Cache")
	isDebug := strings.HasPrefix(strings.ToUpper(strings.TrimSpace(raw)), "TCP_")
	isAkamai := isDebug || strings.Contains(strings.ToLower(h.Get("Server")), "akamai") ||
		h.Get("X-Akamai-Transformed") != "" || h.Get("Akamai-Grn") != ""
	if !isAkamai {
		return Detection{}, false
	}
	// Without the debug response (the Pragma was ignored) the state is unknown.
	d := Detection{CDN: Akamai, State: Unknown}
	if isDebug {
		d.Raw = raw
		// "TCP_MISS from a23-1-2-3.deploy.akamaitechnologies.com (...)"
		d.State = lookup(akamaiStates, strings.Fields(raw)[0])
	}
	return d, true
}

var vercelStates = map[string]State{
	"HIT": Hit, "PRERENDER": Hit, "STALE": Stale, "MISS": Miss,
}

func vercel(h http.Header) (Detection, bool) {
	raw := h.Get("X-Vercel-Cache")
	id := h.Get("X-Vercel-Id")
	if raw == "" && id == "" {
		return Detection{}, false
	}
	d := Detection{CDN: Vercel, Raw: raw, State: Unknown}
	if raw != "" {
		d.State = lookup(vercelStates, raw)
	}
	// x-vercel-id: "sfo1::iad1::abcde-1700000000000-0123456789ab"; the first
	// region is the edge that answered.
	if region, _, ok := strings.Cut(id, "::"); ok {
		d.POP = region
	}
	return d, true
}

func lastItem(list string) string {
	parts := strings.Split(list, ",")
	return strings.TrimSpace(parts[len(parts)-1])
}
