package source

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxSitemapBytes is the largest uncompressed sitemap accepted, the limit
// of the sitemaps.org protocol.
const MaxSitemapBytes = 50 << 20

// Fetcher gets sitemaps and robots.txt files. Every request sends
// UserAgent.
type Fetcher struct {
	Client    *http.Client
	UserAgent string
}

func (f Fetcher) get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

// ReadSitemap returns the page URLs of a sitemap in document order. A
// sitemap index is followed depth first, nested indexes included, and each
// sitemap is read once. Gzip-compressed sitemaps (.gz) are detected by
// their content. Duplicates are dropped. With limit > 0, reading stops once
// limit URLs are found.
func (f Fetcher) ReadSitemap(ctx context.Context, sitemapURL string, limit int) ([]string, error) {
	if _, err := parseAbsolute(sitemapURL); err != nil {
		return nil, fmt.Errorf("--from-sitemap: %w", err)
	}
	w := &sitemapWalk{f: f, limit: limit, visited: map[string]bool{}, seen: map[string]bool{}}
	if err := w.visit(ctx, sitemapURL); err != nil && !errors.Is(err, errLimit) {
		return nil, err
	}
	if len(w.urls) == 0 {
		return nil, ErrEmpty
	}
	return w.urls, nil
}

var errLimit = errors.New("limit reached")

type sitemapWalk struct {
	f       Fetcher
	limit   int
	visited map[string]bool
	seen    map[string]bool
	urls    []string
}

// sitemapDoc matches both <urlset> and <sitemapindex>, in any namespace.
type sitemapDoc struct {
	XMLName  xml.Name
	URLs     []sitemapLoc `xml:"url"`
	Sitemaps []sitemapLoc `xml:"sitemap"`
}

type sitemapLoc struct {
	Loc string `xml:"loc"`
}

func (w *sitemapWalk) visit(ctx context.Context, sitemapURL string) error {
	if w.visited[sitemapURL] {
		return nil
	}
	w.visited[sitemapURL] = true
	doc, err := w.fetch(ctx, sitemapURL)
	if err != nil {
		return fmt.Errorf("sitemap %s: %w", sitemapURL, err)
	}
	switch doc.XMLName.Local {
	case "sitemapindex":
		for _, s := range doc.Sitemaps {
			loc := strings.TrimSpace(s.Loc)
			if _, err := parseAbsolute(loc); err != nil {
				return fmt.Errorf("sitemap %s: %w", sitemapURL, err)
			}
			if err := w.visit(ctx, loc); err != nil {
				return err
			}
		}
	case "urlset":
		for _, e := range doc.URLs {
			u, err := parseAbsolute(strings.TrimSpace(e.Loc))
			if err != nil {
				return fmt.Errorf("sitemap %s: %w", sitemapURL, err)
			}
			u.Fragment = ""
			s := u.String()
			if w.seen[s] {
				continue
			}
			w.seen[s] = true
			w.urls = append(w.urls, s)
			if w.limit > 0 && len(w.urls) >= w.limit {
				return errLimit
			}
		}
	default:
		return fmt.Errorf("not a sitemap: root element <%s>, want <urlset> or <sitemapindex>", doc.XMLName.Local)
	}
	return nil
}

func (w *sitemapWalk) fetch(ctx context.Context, sitemapURL string) (*sitemapDoc, error) {
	resp, err := w.f.get(ctx, sitemapURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body := bufio.NewReader(resp.Body)
	var r io.Reader = body
	if magic, _ := body.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxSitemapBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSitemapBytes {
		return nil, fmt.Errorf("larger than %d MB uncompressed", MaxSitemapBytes>>20)
	}
	var doc sitemapDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid XML: %w", err)
	}
	return &doc, nil
}
