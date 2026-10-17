package source

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// RobotsToken is the product token matched against robots.txt groups. The
// "sitewarm" group applies; without one, the "*" group does.
const RobotsToken = "sitewarm"

// maxRobotsBytes is how much of a robots.txt is parsed, the minimum that
// RFC 9309 requires; the rest is ignored.
const maxRobotsBytes = 500 << 10

// Robots checks URLs against the robots.txt of their host, fetched once per
// scheme and host. It is safe for concurrent use.
//
//   - 2xx: the rules apply.
//   - 404: everything is allowed.
//   - Any other status, a timeout or a network error: robots.txt is
//     unavailable and every URL of that host is skipped, with a warning.
type Robots struct {
	f        Fetcher
	mu       sync.Mutex
	hosts    map[string]*robotsHost
	warnings []string
}

type robotsHost struct {
	rules       []robotsRule // nil allows everything
	unavailable bool
}

type robotsRule struct {
	allow   bool
	pattern string
}

// NewRobots returns a checker that fetches with f.
func (f Fetcher) NewRobots() *Robots {
	return &Robots{f: f, hosts: map[string]*robotsHost{}}
}

// Allowed reports whether rawURL may be requested.
func (r *Robots) Allowed(ctx context.Context, rawURL string) (bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false, err
	}
	key := u.Scheme + "://" + u.Host
	r.mu.Lock()
	h, ok := r.hosts[key]
	r.mu.Unlock()
	if !ok {
		h = r.load(ctx, u)
		r.mu.Lock()
		if prev, dup := r.hosts[key]; dup {
			h = prev
		} else {
			r.hosts[key] = h
		}
		r.mu.Unlock()
	}
	if h.unavailable {
		return false, nil
	}
	return allowed(h.rules, robotsPath(u)), nil
}

// Warnings lists one message per host whose robots.txt was unavailable.
func (r *Robots) Warnings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warnings...)
}

func (r *Robots) warn(format string, args ...any) {
	r.mu.Lock()
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

func (r *Robots) load(ctx context.Context, u *url.URL) *robotsHost {
	robotsURL := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/robots.txt"}).String()
	resp, err := r.f.get(ctx, robotsURL)
	if err != nil {
		r.warn("%s could not be fetched (%v); its sitemap URLs are skipped-robots", robotsURL, err)
		return &robotsHost{unavailable: true}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return &robotsHost{}
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		r.warn("%s returned HTTP %d; its sitemap URLs are skipped-robots", robotsURL, resp.StatusCode)
		return &robotsHost{unavailable: true}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRobotsBytes))
	if err != nil {
		r.warn("%s could not be read (%v); its sitemap URLs are skipped-robots", robotsURL, err)
		return &robotsHost{unavailable: true}
	}
	return &robotsHost{rules: parseRobots(data, RobotsToken)}
}

// parseRobots returns the rules of the groups for token, or of the "*"
// groups when none names token. Groups for the same agent are merged.
func parseRobots(data []byte, token string) []robotsRule {
	type group struct {
		agents []string
		rules  []robotsRule
	}
	var groups []*group
	lastWasAgent := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), maxRobotsBytes)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "user-agent":
			if !lastWasAgent || len(groups) == 0 {
				groups = append(groups, &group{})
			}
			g := groups[len(groups)-1]
			g.agents = append(g.agents, strings.ToLower(value))
			lastWasAgent = true
		case "allow", "disallow":
			lastWasAgent = false
			if len(groups) == 0 || value == "" {
				continue
			}
			g := groups[len(groups)-1]
			g.rules = append(g.rules, robotsRule{allow: key == "allow", pattern: value})
		}
	}
	collect := func(agent string) (rules []robotsRule, found bool) {
		for _, g := range groups {
			for _, a := range g.agents {
				if a == agent {
					rules = append(rules, g.rules...)
					found = true
					break
				}
			}
		}
		return rules, found
	}
	if rules, found := collect(strings.ToLower(token)); found {
		return rules
	}
	rules, _ := collect("*")
	return rules
}

// allowed applies the longest matching rule; on a tie, allow wins. No
// matching rule allows.
func allowed(rules []robotsRule, path string) bool {
	best, ok := -1, true
	for _, r := range rules {
		if !matchRobots(r.pattern, path) {
			continue
		}
		n := len(r.pattern)
		if n > best || (n == best && r.allow) {
			best, ok = n, r.allow
		}
	}
	return ok
}

// matchRobots matches a path against a pattern with the * and $ operators
// of RFC 9309.
func matchRobots(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	if anchored {
		pattern = strings.TrimSuffix(pattern, "$")
	}
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(path, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for i := 1; i < len(parts); i++ {
		if anchored && i == len(parts)-1 {
			return strings.HasSuffix(path[pos:], parts[i])
		}
		j := strings.Index(path[pos:], parts[i])
		if j < 0 {
			return false
		}
		pos += j + len(parts[i])
	}
	return !anchored || pos == len(path)
}

func robotsPath(u *url.URL) string {
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}
