package probe

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// MaxParseBytes is how much decoded HTML is scanned for meta robots. The
// rest of the body is still downloaded and discarded so the CDN finishes
// caching the response.
const MaxParseBytes = 256 << 10

// Indexability is what the response says about being indexed.
type Indexability string

const (
	Indexable Indexability = "indexable"
	NoIndex   Indexability = "noindex"
	// IndexabilityError means the body could not be decoded.
	IndexabilityError Indexability = "indexability-error"
	// IndexabilityInconclusive means </head> was not found within MaxParseBytes.
	IndexabilityInconclusive Indexability = "indexability-inconclusive"
	// NotHTML responses are judged by X-Robots-Tag only.
	NotHTML Indexability = "not-html"
)

// bodyResult is what reading one response body produced.
type bodyResult struct {
	Indexability Indexability
	Detail       string
	// WireBytes is the size of the body as received, before decoding.
	WireBytes int64
	// Err is set when the body could not be fully downloaded.
	Err error
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// readBody evaluates indexability from the first MaxParseBytes of decoded
// HTML, then drains the rest of the body. analyze is false for responses
// whose indexability does not matter (non-2xx, redirect hops).
func readBody(resp *http.Response, analyze bool) bodyResult {
	raw := &countingReader{r: resp.Body}
	var res bodyResult
	if analyze {
		res.Indexability, res.Detail = indexability(resp, raw)
	}
	if _, err := io.Copy(io.Discard, raw); err != nil {
		res.Err = fmt.Errorf("reading body: %w", err)
	}
	res.WireBytes = raw.n
	return res
}

func indexability(resp *http.Response, body io.Reader) (Indexability, string) {
	if tag := resp.Header.Values("X-Robots-Tag"); xRobotsNoIndex(tag) {
		return NoIndex, "X-Robots-Tag: " + strings.Join(tag, ", ")
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType != "text/html" && mediaType != "application/xhtml+xml" {
		return NotHTML, ""
	}
	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	decoded, err := decode(resp.Header.Values("Content-Encoding"), body, &closers)
	if err != nil {
		return IndexabilityError, err.Error()
	}
	buf, err := io.ReadAll(io.LimitReader(decoded, MaxParseBytes))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return IndexabilityError, "decoding body: " + err.Error()
	}
	return scanHead(buf)
}

// decode wraps r with a decoder for each Content-Encoding, applied in
// reverse order of how they were applied by the server. Decoders that hold
// resources are appended to closers.
func decode(encodings []string, r io.Reader, closers *[]io.Closer) (io.Reader, error) {
	var list []string
	for _, v := range encodings {
		for _, e := range strings.Split(v, ",") {
			if e = strings.ToLower(strings.TrimSpace(e)); e != "" && e != "identity" {
				list = append(list, e)
			}
		}
	}
	for i := len(list) - 1; i >= 0; i-- {
		var err error
		switch list[i] {
		case "gzip", "x-gzip":
			r, err = gzip.NewReader(r)
		case "deflate":
			r, err = newDeflateReader(r)
		case "br":
			r = brotli.NewReader(r)
		case "zstd":
			var d *zstd.Decoder
			d, err = zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
			if err == nil {
				rc := d.IOReadCloser()
				*closers = append(*closers, rc)
				r = rc
			}
		default:
			return nil, fmt.Errorf("unsupported Content-Encoding %q", list[i])
		}
		if err != nil {
			return nil, fmt.Errorf("decoding %s: %w", list[i], err)
		}
	}
	return r, nil
}

// newDeflateReader accepts both zlib-wrapped deflate (what HTTP specifies)
// and raw deflate (what some servers send).
func newDeflateReader(r io.Reader) (io.Reader, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(2)
	if err != nil {
		return nil, err
	}
	// A zlib header has CM=8 and a check value making it a multiple of 31.
	if head[0]&0x0f == 8 && (uint16(head[0])<<8|uint16(head[1]))%31 == 0 {
		return zlib.NewReader(br)
	}
	return flate.NewReader(br), nil
}

// scanHead looks for meta robots inside <head>. Reaching <body> also ends
// the head, as it does for browsers.
func scanHead(buf []byte) (Indexability, string) {
	z := html.NewTokenizer(bytes.NewReader(buf))
	for {
		switch z.Next() {
		case html.ErrorToken:
			// End of the scanned window without leaving <head>.
			return IndexabilityInconclusive, fmt.Sprintf("</head> not found in the first %d KB", MaxParseBytes>>10)
		case html.EndTagToken:
			if name, _ := z.TagName(); atom.Lookup(name) == atom.Head {
				return Indexable, ""
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch atom.Lookup(name) {
			case atom.Body:
				return Indexable, ""
			case atom.Meta:
				if !hasAttr {
					continue
				}
				var metaName, content string
				for {
					k, v, more := z.TagAttr()
					switch strings.ToLower(string(k)) {
					case "name":
						metaName = strings.ToLower(strings.TrimSpace(string(v)))
					case "content":
						content = string(v)
					}
					if !more {
						break
					}
				}
				if (metaName == "robots" || metaName == "googlebot") && hasNoIndex(content) {
					return NoIndex, fmt.Sprintf(`<meta name=%q content=%q>`, metaName, content)
				}
			}
		}
	}
}

// hasNoIndex reports whether a meta robots directive list contains noindex
// or none.
func hasNoIndex(directives string) bool {
	for _, d := range strings.Split(directives, ",") {
		if isNoIndex(strings.ToLower(strings.TrimSpace(d))) {
			return true
		}
	}
	return false
}

func isNoIndex(d string) bool { return d == "noindex" || d == "none" }

// valued are the robots directives written as "name: value", which must not
// be read as a user-agent prefix.
var valued = map[string]bool{
	"unavailable_after": true, "max-snippet": true,
	"max-image-preview": true, "max-video-preview": true,
}

// xRobotsNoIndex reports whether X-Robots-Tag headers carry noindex for all
// crawlers or for Googlebot, the same scope as the meta check. Each header
// is read on its own; a "bot:" prefix scopes the directives after it, so
// "otherbot: noindex" does not count.
func xRobotsNoIndex(values []string) bool {
	for _, v := range values {
		scope := ""
		for _, d := range strings.Split(v, ",") {
			d = strings.ToLower(strings.TrimSpace(d))
			if name, after, ok := strings.Cut(d, ":"); ok && !valued[strings.TrimSpace(name)] {
				scope, d = strings.TrimSpace(name), strings.TrimSpace(after)
			}
			if (scope == "" || scope == "googlebot") && isNoIndex(d) {
				return true
			}
		}
	}
	return false
}
