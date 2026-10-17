package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseRobotsGroupSelection(t *testing.T) {
	txt := `
# comment
User-agent: Googlebot
Disallow: /

User-agent: *
Disallow: /private
Sitemap: https://example.com/sitemap.xml

User-agent: other
User-agent: SiteWarm
Disallow: /admin
Allow: /admin/public

user-agent: sitewarm
disallow: /tmp # merged with the group above
`
	rules := parseRobots([]byte(txt), RobotsToken)
	cases := map[string]bool{
		"/":               true,
		"/private":        true, // the * group does not apply when a sitewarm group exists
		"/admin":          false,
		"/admin/x":        false,
		"/admin/public/a": true,
		"/tmp/file":       false,
	}
	for path, want := range cases {
		if got := allowed(rules, path); got != want {
			t.Errorf("%s: allowed = %v, want %v", path, got, want)
		}
	}

	star := parseRobots([]byte(txt), "unlisted")
	if allowed(star, "/private/x") || !allowed(star, "/admin") {
		t.Error("without its own group, the * group must apply")
	}
	if !allowed(parseRobots([]byte("User-agent: googlebot\nDisallow: /\n"), RobotsToken), "/x") {
		t.Error("no matching group and no * group must allow everything")
	}
}

func TestRobotsLongestMatchAndOperators(t *testing.T) {
	rules := parseRobots([]byte(`User-agent: *
Disallow: /shop
Allow: /shop/
Disallow: /*.pdf$
Disallow: /search*q=
Allow: /same
Disallow: /same
Disallow:
`), RobotsToken)
	cases := map[string]bool{
		"/shop":          false,
		"/shop/item":     true, // longer allow wins
		"/doc.pdf":       false,
		"/doc.pdf?x=1":   true, // $ anchors the end
		"/search?q=shoe": false,
		"/search":        true,
		"/same":          true, // equal length: allow wins
		"/other":         true, // empty Disallow matches nothing
	}
	for path, want := range cases {
		if got := allowed(rules, path); got != want {
			t.Errorf("%s: allowed = %v, want %v", path, got, want)
		}
	}
}

func TestRobotsFetchPolicies(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		allowed bool
		warns   bool
	}{
		{"404 allows everything", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, true, false},
		{"5xx skips", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }, false, true},
		{"rules apply", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("User-agent: sitewarm\nDisallow: /page\n"))
		}, false, false},
		{"timeout skips", func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/robots.txt" {
					t.Errorf("unexpected request %s", r.URL.Path)
				}
				c.handler(w, r)
			}))
			defer srv.Close()
			client := srv.Client()
			client.Timeout = 100 * time.Millisecond
			robots := Fetcher{Client: client, UserAgent: "test"}.NewRobots()
			for i := 0; i < 2; i++ {
				ok, err := robots.Allowed(context.Background(), srv.URL+"/page?x=1")
				if err != nil || ok != c.allowed {
					t.Fatalf("Allowed = %v, %v; want %v", ok, err, c.allowed)
				}
			}
			if n := requests.Load(); n != 1 {
				t.Errorf("robots.txt fetched %d times, want once per host", n)
			}
			w := robots.Warnings()
			if (len(w) > 0) != c.warns {
				t.Errorf("warnings %v", w)
			}
			if c.warns && !strings.Contains(w[0], "skipped-robots") {
				t.Errorf("warning %q does not name the state", w[0])
			}
		})
	}
}
