package cdn

import (
	"net/http"
	"testing"
)

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name  string
		h     http.Header
		cdn   string
		state State
		pop   string
	}{
		{"cloudflare hit", hdr("Cf-Cache-Status", "HIT", "Cf-Ray", "8c1d2e3f4a5b6c7d-SJC"), Cloudflare, Hit, "SJC"},
		{"cloudflare stale", hdr("Cf-Cache-Status", "STALE"), Cloudflare, Stale, ""},
		{"cloudflare updating", hdr("Cf-Cache-Status", "UPDATING"), Cloudflare, Stale, ""},
		{"cloudflare revalidated", hdr("Cf-Cache-Status", "REVALIDATED"), Cloudflare, Stale, ""},
		{"cloudflare miss", hdr("Cf-Cache-Status", "MISS"), Cloudflare, Miss, ""},
		{"cloudflare expired", hdr("Cf-Cache-Status", "EXPIRED"), Cloudflare, Miss, ""},
		{"cloudflare bypass", hdr("Cf-Cache-Status", "BYPASS"), Cloudflare, Bypass, ""},
		{"cloudflare dynamic", hdr("Cf-Cache-Status", "DYNAMIC"), Cloudflare, Bypass, ""},
		{"cloudflare unmapped", hdr("Cf-Cache-Status", "NONE"), Cloudflare, Unknown, ""},
		{"cloudflare ray only", hdr("Cf-Ray", "abc-LHR"), Cloudflare, Unknown, "LHR"},

		{"vercel hit", hdr("X-Vercel-Cache", "HIT", "X-Vercel-Id", "sfo1::iad1::abc-123"), Vercel, Hit, "sfo1"},
		{"vercel prerender", hdr("X-Vercel-Cache", "PRERENDER"), Vercel, Hit, ""},
		{"vercel stale", hdr("X-Vercel-Cache", "STALE"), Vercel, Stale, ""},
		{"vercel miss", hdr("X-Vercel-Cache", "MISS"), Vercel, Miss, ""},
		{"vercel unmapped", hdr("X-Vercel-Cache", "REVALIDATED"), Vercel, Unknown, ""},

		{"cloudfront hit", hdr("X-Cache", "Hit from cloudfront", "X-Amz-Cf-Pop", "SFO53-P1"), CloudFront, Hit, "SFO53-P1"},
		{"cloudfront refresh hit", hdr("X-Cache", "RefreshHit from cloudfront"), CloudFront, Hit, ""},
		{"cloudfront miss", hdr("X-Cache", "Miss from cloudfront"), CloudFront, Miss, ""},
		{"cloudfront unmapped", hdr("X-Cache", "Error from cloudfront"), CloudFront, Unknown, ""},

		{"fastly edge hit after shield miss", hdr("X-Cache", "MISS, HIT", "X-Served-By", "cache-iad-1, cache-sjc10045-SJC"), Fastly, Hit, "cache-sjc10045-SJC"},
		{"fastly edge miss after shield hit", hdr("X-Cache", "HIT, MISS", "X-Served-By", "cache-iad-1, cache-sjc1-SJC"), Fastly, Miss, "cache-sjc1-SJC"},
		{"fastly single hit", hdr("X-Cache", "HIT", "X-Served-By", "cache-lhr1-LHR"), Fastly, Hit, "cache-lhr1-LHR"},
		{"fastly unmapped", hdr("X-Cache", "PASS", "X-Served-By", "cache-lhr1-LHR"), Fastly, Unknown, "cache-lhr1-LHR"},

		{"akamai hit", hdr("X-Cache", "TCP_HIT from a23-1-2-3.deploy.akamaitechnologies.com (AkamaiGHost/22.1)"), Akamai, Hit, ""},
		{"akamai mem hit", hdr("X-Cache", "TCP_MEM_HIT from a1"), Akamai, Hit, ""},
		{"akamai miss", hdr("X-Cache", "TCP_MISS from a1"), Akamai, Miss, ""},
		{"akamai refresh hit", hdr("X-Cache", "TCP_REFRESH_HIT from a1"), Akamai, Stale, ""},
		{"akamai without debug", hdr("Server", "AkamaiGHost"), Akamai, Unknown, ""},

		{"no cdn", hdr("Server", "nginx"), "", Unknown, ""},
		{"generic x-cache is not a cdn", hdr("X-Cache", "HIT"), "", Unknown, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.h)
			if got.CDN != tt.cdn || got.State != tt.state || got.POP != tt.pop {
				t.Errorf("Classify = {cdn %q state %q pop %q}, want {%q %q %q}", got.CDN, got.State, got.POP, tt.cdn, tt.state, tt.pop)
			}
		})
	}
}

func TestClassifyKeepsRawUnmappedValue(t *testing.T) {
	got := Classify(hdr("Cf-Cache-Status", "Weird"))
	if got.State != Unknown || got.Raw != "Weird" {
		t.Fatalf("got %+v, want unknown with raw value", got)
	}
}

func TestClassifyStackedOutermostWins(t *testing.T) {
	// Cloudflare in front of Vercel: Cloudflare answered the client.
	got := Classify(hdr("Cf-Cache-Status", "MISS", "X-Vercel-Cache", "HIT"))
	if got.CDN != Cloudflare || got.State != Miss {
		t.Fatalf("got %s/%s, want cloudflare/miss", got.CDN, got.State)
	}
	if len(got.Others) != 1 || got.Others[0].CDN != Vercel || got.Others[0].State != Hit {
		t.Fatalf("others = %+v, want vercel hit", got.Others)
	}
	// CloudFront in front of Fastly.
	got = Classify(hdr("X-Cache", "Hit from cloudfront", "X-Served-By", "cache-lhr1-LHR"))
	if got.CDN != CloudFront {
		t.Fatalf("got %s, want cloudfront first", got.CDN)
	}
}
