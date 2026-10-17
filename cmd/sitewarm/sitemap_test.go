package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func sitemapServer(robotsStatus int) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.WriteHeader(robotsStatus)
		case "/sitemap.xml":
			fmt.Fprintf(w, `<urlset><url><loc>%s/a</loc></url></urlset>`, srv.URL)
		default:
			w.Header().Set("Cf-Cache-Status", "HIT")
			w.Write([]byte("<html><head></head></html>"))
		}
	}))
	return srv
}

func TestFormatCSV(t *testing.T) {
	srv := sitemapServer(http.StatusNotFound)
	defer srv.Close()
	h := newHarness()
	code := run(context.Background(), []string{"scan", "--from-sitemap", srv.URL + "/sitemap.xml", "--rps", "0", "--format", "csv"}, h.env())
	if code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, h.stderr.String())
	}
	want := "url,status,state\n" + srv.URL + "/a,200,hit\n" + srv.URL + "/a,200,hit\n"
	if h.stdout.String() != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", h.stdout.String(), want)
	}
}

func TestRobots5xxFailsSitemapRun(t *testing.T) {
	srv := sitemapServer(http.StatusServiceUnavailable)
	defer srv.Close()
	h := newHarness()
	if code := run(context.Background(), []string{"scan", "--from-sitemap", srv.URL + "/sitemap.xml", "--rps", "0"}, h.env()); code != 1 {
		t.Fatalf("exit %d, want 1; stdout: %s", code, h.stdout.String())
	}
	if !strings.Contains(h.stdout.String(), "no URL was verified") {
		t.Fatalf("stdout: %s", h.stdout.String())
	}
}

// SIGINT while the sitemap is read exits 130 and still writes the report.
func TestSignalWhileReadingSitemap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	h := newHarness()
	reportPath := filepath.Join(t.TempDir(), "r.json")
	time.AfterFunc(100*time.Millisecond, func() { h.signals <- syscall.SIGINT })
	code := run(context.Background(), []string{"scan", "--from-sitemap", srv.URL + "/sitemap.xml", "--report", reportPath}, h.env())
	if code != 130 {
		t.Fatalf("exit %d, want 130; stderr: %s", code, h.stderr.String())
	}
	if r := readReport(t, reportPath); r["stopped"] != "SIGINT" {
		t.Fatalf("stopped = %v", r["stopped"])
	}
}

func TestIgnoreRobotsWarns(t *testing.T) {
	srv := sitemapServer(http.StatusServiceUnavailable)
	defer srv.Close()
	h := newHarness()
	if code := run(context.Background(), []string{"scan", "--from-sitemap", srv.URL + "/sitemap.xml", "--rps", "0", "--ignore-robots"}, h.env()); code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, h.stderr.String())
	}
	if !strings.Contains(h.stderr.String(), "warning: --ignore-robots set; robots.txt is not checked") {
		t.Fatalf("stderr: %q", h.stderr.String())
	}
}
