package verify

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ernestova/sitemap_warmup/internal/cdn"
	"github.com/ernestova/sitemap_warmup/internal/probe"
)

func TestClassifyObservation(t *testing.T) {
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			out.Add(kv[i], kv[i+1])
		}
		return out
	}
	tests := []struct {
		name string
		obs  probe.Observation
		want cdn.State
	}{
		{"network error", probe.Observation{Err: errors.New("timeout")}, cdn.Error},
		{"redirect error", probe.Observation{RedirectError: "loop"}, cdn.RedirectError},
		{"5xx", probe.Observation{Status: 502, Header: h()}, cdn.Error},
		{"403", probe.Observation{Status: 403, Header: h("Cf-Cache-Status", "DYNAMIC")}, cdn.Challenged},
		{"cloudflare challenge", probe.Observation{Status: 503, Header: h("Cf-Mitigated", "challenge")}, cdn.Challenged},
		{"404", probe.Observation{Status: 404, Header: h()}, cdn.ClientError},
		{"410", probe.Observation{Status: 410, Header: h()}, cdn.ClientError},
		{"hit", probe.Observation{Status: 200, Header: h("Cf-Cache-Status", "HIT")}, cdn.Hit},
		{"miss private is bypass", probe.Observation{Status: 200, Header: h("X-Vercel-Cache", "MISS", "Cache-Control", "private, max-age=0")}, cdn.Bypass},
		{"miss no-store is bypass", probe.Observation{Status: 200, Header: h("Cf-Cache-Status", "MISS", "Cache-Control", "no-store")}, cdn.Bypass},
		{"plain miss", probe.Observation{Status: 200, Header: h("Cf-Cache-Status", "MISS", "Cache-Control", "public, s-maxage=60")}, cdn.Miss},
		{"vary user-agent", probe.Observation{Status: 200, Header: h("Cf-Cache-Status", "HIT", "Vary", "Accept-Encoding, User-Agent")}, cdn.VariantUnverifiable},
		{"vary accept-encoding is fine", probe.Observation{Status: 200, Header: h("Cf-Cache-Status", "HIT", "Vary", "Accept-Encoding")}, cdn.Hit},
		{"no cdn", probe.Observation{Status: 200, Header: h("Server", "nginx")}, cdn.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.obs).State; got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}
