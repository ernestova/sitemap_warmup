package sitewarm_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/clarivo-co/sitewarm/pkg/sitewarm"
)

// cdnServer emulates a CDN edge: the first `misses` requests per URL and
// User-Agent are misses, the rest hits.
func cdnServer(t *testing.T, misses int, headers func(hit bool) map[string]string) *httptest.Server {
	var mu sync.Mutex
	seen := map[string]int{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		key := r.URL.Path + "|" + r.Header.Get("Sec-Ch-Ua-Mobile")
		seen[key]++
		hit := seen[key] > misses
		mu.Unlock()
		for k, v := range headers(hit) {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "public, s-maxage=600")
		w.Write([]byte("<html><head><title>ok</title></head><body></body></html>"))
	}))
}

func pick(hit bool, h, m string) string {
	if hit {
		return h
	}
	return m
}

var cdns = map[string]func(hit bool) map[string]string{
	"cloudflare": func(hit bool) map[string]string {
		return map[string]string{"Cf-Cache-Status": pick(hit, "HIT", "MISS"), "Cf-Ray": "8c1d2e3f-SJC"}
	},
	"vercel": func(hit bool) map[string]string {
		return map[string]string{"X-Vercel-Cache": pick(hit, "HIT", "MISS"), "X-Vercel-Id": "sfo1::iad1::abc"}
	},
	"cloudfront": func(hit bool) map[string]string {
		return map[string]string{"X-Cache": pick(hit, "Hit from cloudfront", "Miss from cloudfront"), "X-Amz-Cf-Pop": "SFO53-P1"}
	},
	"fastly": func(hit bool) map[string]string {
		return map[string]string{"X-Cache": pick(hit, "MISS, HIT", "HIT, MISS"), "X-Served-By": "cache-iad-1, cache-sjc1-SJC"}
	},
	"akamai": func(hit bool) map[string]string {
		return map[string]string{"X-Cache": pick(hit, "TCP_MEM_HIT from a1", "TCP_MISS from a1")}
	},
}

func writeCSV(t *testing.T, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "top.csv")
	if err := os.WriteFile(path, []byte("url\n"+strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fastOptions(csv string) sitewarm.Options {
	o := sitewarm.DefaultOptions()
	o.TopFrom = csv
	o.RPS = 0 // unlimited in tests
	o.RetryInterval = 20 * time.Millisecond
	o.MaxWait = 2 * time.Second
	o.RunTimeout = 10 * time.Second
	return o
}

func TestScanEachCDNWarmsToHit(t *testing.T) {
	for name, headers := range cdns {
		t.Run(name, func(t *testing.T) {
			srv := cdnServer(t, 2, headers)
			defer srv.Close()
			res, err := sitewarm.Scan(context.Background(), fastOptions(writeCSV(t, srv.URL+"/", srv.URL+"/pricing")))
			if err != nil {
				t.Fatal(err)
			}
			if !res.Summary.Passed || res.Summary.Total != 4 || res.Summary.HitRatio != 1 {
				t.Fatalf("summary = %+v", res.Summary)
			}
			for _, e := range res.Results {
				if e.CDN != name || e.State != sitewarm.Hit || e.InitialState != sitewarm.Miss || e.Attempts != 3 {
					t.Fatalf("%s/%s: cdn %s state %s initial %s attempts %d", e.URL, e.Profile, e.CDN, e.State, e.InitialState, e.Attempts)
				}
				if e.TimeToHitMS == nil || e.TTFBFirstMS == nil || e.TTFBFinalMS == nil {
					t.Fatalf("missing timings: %+v", e)
				}
				if name != "akamai" && e.POP == "" {
					t.Fatalf("missing PoP for %s", name)
				}
			}
		})
	}
}

func TestScanFailsOnMissAfterMaxWait(t *testing.T) {
	srv := cdnServer(t, 1<<30, cdns["cloudflare"])
	defer srv.Close()
	o := fastOptions(writeCSV(t, srv.URL+"/"))
	o.MaxWait = 100 * time.Millisecond
	res, err := sitewarm.Scan(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Passed || res.Summary.ByState[sitewarm.Miss] != 2 {
		t.Fatalf("summary = %+v", res.Summary)
	}
}

func TestScanStatusMapping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/gone", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusGone) })
	mux.HandleFunc("/account", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Cache-Status", "DYNAMIC")
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head></head></html>"))
	})
	mux.HandleFunc("/hidden", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Cache-Status", "HIT")
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><meta name="robots" content="noindex"></head></html>`))
	})
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/new", http.StatusMovedPermanently) })
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Cache-Status", "HIT")
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head></head></html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	o := fastOptions(writeCSV(t, srv.URL+"/gone", srv.URL+"/account", srv.URL+"/hidden", srv.URL+"/old"))
	o.Profiles = []string{"desktop"}
	res, err := sitewarm.Scan(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		state  sitewarm.State
		failed bool
	}{
		"/gone":    {sitewarm.ClientError, true},
		"/account": {sitewarm.Bypass, false},
		"/hidden":  {sitewarm.Hit, true}, // noindex fails by default
		"/old":     {sitewarm.Hit, false},
	}
	for _, e := range res.Results {
		w := want[strings.TrimPrefix(e.URL, srv.URL)]
		if e.State != w.state || e.Failed != w.failed {
			t.Errorf("%s: state %s failed %v, want %s %v", e.URL, e.State, e.Failed, w.state, w.failed)
		}
	}
	if old := res.Results[3]; old.FinalURL != srv.URL+"/new" || len(old.Warnings) == 0 {
		t.Errorf("redirect not reported: %+v", old)
	}
}

func TestScanRunTimeoutIsIncomplete(t *testing.T) {
	srv := cdnServer(t, 1<<30, cdns["vercel"])
	defer srv.Close()
	var rows []string
	for i := range 20 {
		rows = append(rows, fmt.Sprintf("%s/p%d", srv.URL, i))
	}
	o := fastOptions(writeCSV(t, rows...))
	o.RPS = 20
	o.RunTimeout = 300 * time.Millisecond
	res, err := sitewarm.Scan(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stopped != "run-timeout" || res.Summary.ByState[sitewarm.Incomplete] != 40 || res.Summary.Passed {
		t.Fatalf("stopped %q summary %+v", res.Stopped, res.Summary)
	}
}

func TestScanConfigErrors(t *testing.T) {
	csv := writeCSV(t, "https://example.com/")
	cases := map[string]func(*sitewarm.Options){
		"no source":           func(o *sitewarm.Options) { o.TopFrom = "" },
		"bad fail-on":         func(o *sitewarm.Options) { o.FailOn = "nope" },
		"cookie without host": func(o *sitewarm.Options) { o.Cookies = map[string]string{"desktop": "a=b"} },
		"unknown profile":     func(o *sitewarm.Options) { o.Profiles = []string{"tablet"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			o := fastOptions(csv)
			mutate(&o)
			if _, err := sitewarm.Scan(context.Background(), o); !errors.Is(err, sitewarm.ErrConfig) {
				t.Fatalf("got %v, want ErrConfig", err)
			}
		})
	}
}

func TestTopCutsTheList(t *testing.T) {
	srv := cdnServer(t, 0, cdns["cloudflare"])
	defer srv.Close()
	o := fastOptions(writeCSV(t, srv.URL+"/a", srv.URL+"/b", srv.URL+"/c"))
	o.Top = 2
	res, err := sitewarm.Scan(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.Total != 4 || res.Results[0].URL != srv.URL+"/a" || res.Results[2].URL != srv.URL+"/b" {
		t.Fatalf("got %d results: %v", res.Summary.Total, res.Results[0].URL)
	}
}

func TestDoctorNotImplemented(t *testing.T) {
	if _, err := sitewarm.Doctor(context.Background(), "https://example.com/", sitewarm.DefaultOptions()); !errors.Is(err, sitewarm.ErrNotImplemented) {
		t.Fatalf("got %v", err)
	}
}
