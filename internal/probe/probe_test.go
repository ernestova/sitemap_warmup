package probe

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const noindexPage = `<!doctype html><html><head><title>x</title><meta name="robots" content="noindex, follow"></head><body>hi</body></html>`

func newProber(t *testing.T, cfg Config, profiles ...Profile) *Prober {
	t.Helper()
	p, err := New(cfg, profiles)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var desktop = Presets("test", "")[0]

func encode(t *testing.T, enc string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	switch enc {
	case "gzip":
		w = gzip.NewWriter(&buf)
	case "deflate":
		w = zlib.NewWriter(&buf)
	case "raw-deflate":
		w, _ = flate.NewWriter(&buf, flate.DefaultCompression)
	case "br":
		w = brotli.NewWriter(&buf)
	case "zstd":
		zw, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		w = zw
	}
	w.Write(body)
	w.Close()
	return buf.Bytes()
}

func TestBodyDecodingFindsNoIndex(t *testing.T) {
	for _, enc := range []string{"gzip", "deflate", "raw-deflate", "br", "zstd", ""} {
		t.Run("enc="+enc, func(t *testing.T) {
			body := []byte(noindexPage)
			header := enc
			if enc != "" {
				body = encode(t, enc, body)
				header = strings.TrimPrefix(enc, "raw-")
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept-Encoding") != "gzip, deflate, br, zstd" {
					t.Errorf("Accept-Encoding = %q", r.Header.Get("Accept-Encoding"))
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if header != "" {
					w.Header().Set("Content-Encoding", header)
				}
				w.Write(body)
			}))
			defer srv.Close()
			obs := newProber(t, Config{}).Do(context.Background(), srv.URL, desktop)
			if obs.Err != nil {
				t.Fatal(obs.Err)
			}
			if obs.Indexability != NoIndex {
				t.Fatalf("indexability = %s (%s), want noindex", obs.Indexability, obs.IndexabilityDetail)
			}
		})
	}
}

func TestBodyCorruptOrUnknownEncodingIsIndexabilityError(t *testing.T) {
	for _, enc := range []string{"gzip", "br", "zstd", "compress"} {
		t.Run(enc, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.Header().Set("Content-Encoding", enc)
				w.Write([]byte("this is not compressed data at all, definitely not"))
			}))
			defer srv.Close()
			obs := newProber(t, Config{}).Do(context.Background(), srv.URL, desktop)
			if obs.Err != nil {
				t.Fatal(obs.Err)
			}
			if obs.Indexability != IndexabilityError {
				t.Fatalf("indexability = %s, want indexability-error", obs.Indexability)
			}
		})
	}
}

func TestXRobotsTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("X-Robots-Tag", "googlebot: noindex")
	}))
	defer srv.Close()
	if obs := newProber(t, Config{}).Do(context.Background(), srv.URL, desktop); obs.Indexability != NoIndex {
		t.Fatalf("indexability = %s, want noindex", obs.Indexability)
	}
}

func TestLargeHeadIsInconclusiveAndBodyFullyConsumed(t *testing.T) {
	// A <head> of 300 KB pushes </head> past the parse window; the noindex
	// after it cannot be seen. The 1 MB body is still downloaded in full.
	head := "<html><head>" + strings.Repeat("<!-- padding -->", 300<<10/16)
	page := head + `<meta name="robots" content="noindex"></head><body>` + strings.Repeat("x", 1<<20) + "</body></html>"
	var written atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		n, _ := io.Copy(w, strings.NewReader(page))
		written.Store(n)
	}))
	defer srv.Close()
	obs := newProber(t, Config{}).Do(context.Background(), srv.URL, desktop)
	if obs.Err != nil {
		t.Fatal(obs.Err)
	}
	if obs.Indexability != IndexabilityInconclusive {
		t.Fatalf("indexability = %s, want inconclusive", obs.Indexability)
	}
	if obs.WireBytes != int64(len(page)) || written.Load() != int64(len(page)) {
		t.Fatalf("read %d bytes, server wrote %d, page is %d", obs.WireBytes, written.Load(), len(page))
	}
}

func TestBodyIsDrainedAfterEarlyHeadEnd(t *testing.T) {
	page := "<html><head></head><body>" + strings.Repeat("y", 2<<20) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(encode(t, "gzip", []byte(page)))
	}))
	defer srv.Close()
	obs := newProber(t, Config{}).Do(context.Background(), srv.URL, desktop)
	if obs.Indexability != Indexable {
		t.Fatalf("indexability = %s", obs.Indexability)
	}
	if want := int64(len(encode(t, "gzip", []byte(page)))); obs.WireBytes != want {
		t.Fatalf("wire bytes = %d, want the full %d", obs.WireBytes, want)
	}
}

func TestSlowBodyHitsDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head></head>"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	start := time.Now()
	obs := newProber(t, Config{RequestTimeout: 200 * time.Millisecond}).Do(context.Background(), srv.URL, desktop)
	if obs.Err == nil {
		t.Fatal("want a deadline error for a body that never ends")
	}
	if !errors.Is(obs.Err, context.DeadlineExceeded) {
		t.Logf("error: %v", obs.Err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("deadline not applied to the body: took %v", time.Since(start))
	}
}

func TestTTFBAndPresetHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "Chrome/"+ChromeMajor) || !strings.Contains(ua, "sitewarm/test (+"+RepoURL+")") {
			t.Errorf("User-Agent = %q", ua)
		}
		if r.Header.Get("Pragma") != "akamai-x-cache-on, akamai-x-get-cache-key" {
			t.Errorf("Pragma = %q", r.Header.Get("Pragma"))
		}
		if r.Header.Get("Sec-Ch-Ua-Mobile") != "?0" {
			t.Errorf("Sec-Ch-Ua-Mobile = %q", r.Header.Get("Sec-Ch-Ua-Mobile"))
		}
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()
	obs := newProber(t, Config{}).Do(context.Background(), srv.URL, desktop)
	if !obs.HasTTFB || obs.TTFB < 50*time.Millisecond {
		t.Fatalf("TTFB = %v (has %v), want ≥ 50ms", obs.TTFB, obs.HasTTFB)
	}
	mobile := Presets("test", "es-ES")[1]
	if mobile.Header.Get("Sec-Ch-Ua-Mobile") != "?1" || mobile.Header.Get("Accept-Language") != "es-ES" {
		t.Fatalf("mobile preset = %v", mobile.Header)
	}
}

func redirectServer(t *testing.T, routes map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to, ok := routes[r.URL.Path]; ok {
			http.Redirect(w, r, to, http.StatusMovedPermanently)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head></head></html>"))
	}))
}

func TestRedirectChain(t *testing.T) {
	srv := redirectServer(t, map[string]string{"/blog": "/blog/", "/blog/": "/blog/index"})
	defer srv.Close()
	obs := newProber(t, Config{}).Do(context.Background(), srv.URL+"/blog", desktop)
	if obs.RedirectError != "" || obs.Status != 200 {
		t.Fatalf("redirect error %q status %d", obs.RedirectError, obs.Status)
	}
	if obs.FinalURL != srv.URL+"/blog/index" || len(obs.Redirects) != 2 {
		t.Fatalf("final %s, chain %v", obs.FinalURL, obs.Redirects)
	}
}

func TestRedirectLoop(t *testing.T) {
	srv := redirectServer(t, map[string]string{"/a": "/b", "/b": "/a"})
	defer srv.Close()
	obs := newProber(t, Config{}).Do(context.Background(), srv.URL+"/a", desktop)
	if !strings.Contains(obs.RedirectError, "loop") {
		t.Fatalf("redirect error = %q, want loop", obs.RedirectError)
	}
}

func TestRedirectTooMany(t *testing.T) {
	routes := map[string]string{"/1": "/2", "/2": "/3", "/3": "/4", "/4": "/5", "/5": "/6", "/6": "/7"}
	srv := redirectServer(t, routes)
	defer srv.Close()
	obs := newProber(t, Config{}).Do(context.Background(), srv.URL+"/1", desktop)
	if !strings.Contains(obs.RedirectError, "more than 5") {
		t.Fatalf("redirect error = %q", obs.RedirectError)
	}
	// Exactly five hops is fine.
	obs = newProber(t, Config{}).Do(context.Background(), srv.URL+"/2", desktop)
	if obs.RedirectError != "" || len(obs.Redirects) != 5 {
		t.Fatalf("five hops: error %q, chain %d", obs.RedirectError, len(obs.Redirects))
	}
}

func TestRedirectToAnotherHost(t *testing.T) {
	srv := redirectServer(t, map[string]string{"/": "https://elsewhere.example/"})
	defer srv.Close()
	obs := newProber(t, Config{}).Do(context.Background(), srv.URL+"/", desktop)
	if !strings.Contains(obs.RedirectError, "another host") {
		t.Fatalf("redirect error = %q", obs.RedirectError)
	}
}

func TestSameSiteApexAndWWW(t *testing.T) {
	u := func(s string) *url.URL { v, _ := url.Parse(s); return v }
	if !SameSite(u("https://example.com/"), u("https://www.example.com/x")) {
		t.Fatal("apex and www must count as one site")
	}
	if !SameSite(u("http://Example.com/"), u("https://example.com/")) {
		t.Fatal("scheme and case must not matter")
	}
	if SameSite(u("https://example.com/"), u("https://shop.example.com/")) {
		t.Fatal("other subdomains are other hosts")
	}
}

func TestCookiesRequireCookieHosts(t *testing.T) {
	p := desktop
	p.Cookie = "session=abc"
	if _, err := New(Config{}, []Profile{p}); !errors.Is(err, ErrCookieHostsRequired) {
		t.Fatalf("got %v, want ErrCookieHostsRequired", err)
	}
}

func TestCookiesOnlyToAllowedHostsOverHTTPS(t *testing.T) {
	var gotCookie atomic.Value
	gotCookie.Store("")
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie.Store(r.Header.Get("Cookie"))
	}))
	defer tlsSrv.Close()
	p := desktop
	p.Cookie = "session=abc"
	tlsURL, _ := url.Parse(tlsSrv.URL)

	allowed := newProber(t, Config{CookieHosts: []string{tlsURL.Hostname()}, Transport: tlsSrv.Client().Transport}, p)
	allowed.Do(context.Background(), tlsSrv.URL, p)
	if gotCookie.Load() != "session=abc" {
		t.Fatalf("allowed https host got cookie %q", gotCookie.Load())
	}

	other := newProber(t, Config{CookieHosts: []string{"other.example"}, Transport: tlsSrv.Client().Transport}, p)
	other.Do(context.Background(), tlsSrv.URL, p)
	if gotCookie.Load() != "" {
		t.Fatalf("host outside --cookie-hosts got cookie %q", gotCookie.Load())
	}
}

// A redirect from https to http on an allowed host must not carry the cookie.
func TestCookiesNeverOverHTTPAfterRedirect(t *testing.T) {
	var plainCookie atomic.Value
	plainCookie.Store("unset")
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainCookie.Store(r.Header.Get("Cookie"))
	}))
	defer plain.Close()
	plainURL, _ := url.Parse(plain.URL)
	// Same hostname (127.0.0.1), different port and scheme.
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "session=abc" {
			t.Errorf("https hop did not get the cookie")
		}
		http.Redirect(w, r, plain.URL+"/", http.StatusFound)
	}))
	defer tlsSrv.Close()

	p := desktop
	p.Cookie = "session=abc"
	tr := tlsSrv.Client().Transport
	prober := newProber(t, Config{CookieHosts: []string{plainURL.Hostname()}, Transport: tr}, p)
	obs := prober.Do(context.Background(), tlsSrv.URL+"/", p)
	if obs.Err != nil || obs.RedirectError != "" {
		t.Fatalf("err %v redirect %q", obs.Err, obs.RedirectError)
	}
	if plainCookie.Load() != "" {
		t.Fatalf("cookie sent over http: %q", plainCookie.Load())
	}
}
