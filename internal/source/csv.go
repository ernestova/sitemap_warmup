// Package source reads the list of URLs to warm.
package source

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

var (
	// ErrEmpty means the list has a header but no URLs.
	ErrEmpty = errors.New("the URL list is empty")
	// ErrUnknownFormat means no url or path column was found.
	ErrUnknownFormat = errors.New(`unrecognized CSV: expected a header with a "url" or "path" column`)
)

// ReadCSV reads the own CSV format: a header row with a "url" column
// (absolute URLs) or a "path" column (resolved against baseURL). Rows keep
// their order, which is their priority; duplicates are dropped.
func ReadCSV(r io.Reader, baseURL string) ([]string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	cr.Comment = '#'
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, ErrEmpty
	}
	if err != nil {
		return nil, fmt.Errorf("reading CSV header: %w", err)
	}
	col, isPath := -1, false
	for i, h := range header {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "url":
			col, isPath = i, false
		case "path":
			if col < 0 {
				col, isPath = i, true
			}
		}
	}
	if col < 0 {
		return nil, ErrUnknownFormat
	}
	var base *url.URL
	if isPath {
		if baseURL == "" {
			return nil, errors.New(`the CSV has a "path" column: --base-url is required`)
		}
		if base, err = parseAbsolute(baseURL); err != nil {
			return nil, fmt.Errorf("--base-url: %w", err)
		}
	}

	seen := map[string]bool{}
	var urls []string
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading CSV: %w", err)
		}
		line, _ := cr.FieldPos(0)
		if col >= len(rec) || strings.TrimSpace(rec[col]) == "" {
			continue
		}
		v := strings.TrimSpace(rec[col])
		var u *url.URL
		if isPath {
			if !strings.HasPrefix(v, "/") {
				return nil, fmt.Errorf("line %d: path %q must start with /", line, v)
			}
			u, err = base.Parse(v)
		} else {
			u, err = parseAbsolute(v)
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		u.Fragment = ""
		s := u.String()
		if !seen[s] {
			seen[s] = true
			urls = append(urls, s)
		}
	}
	if len(urls) == 0 {
		return nil, ErrEmpty
	}
	return urls, nil
}

func parseAbsolute(v string) (*url.URL, error) {
	u, err := url.Parse(v)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%q is not an absolute http(s) URL", v)
	}
	return u, nil
}
