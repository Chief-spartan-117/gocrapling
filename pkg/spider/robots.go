package spider

import (
	"bufio"
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/fetcher"
)

type rule struct {
	allow bool
	path  string
	re    *regexp.Regexp
}

type robotsRules struct {
	rules      []rule
	crawlDelay time.Duration
}

// allowed applies longest-match precedence, Allow winning ties (RFC 9309).
func (r *robotsRules) allowed(path string) bool {
	bestLen, allow := -1, true
	for _, ru := range r.rules {
		if ru.path == "" {
			continue
		}
		if !ru.re.MatchString(path) {
			continue
		}
		switch {
		case len(ru.path) > bestLen:
			bestLen, allow = len(ru.path), ru.allow
		case len(ru.path) == bestLen && ru.allow:
			allow = true
		}
	}
	return allow
}

// compileRobots turns a robots path pattern (* wildcard, $ end anchor)
// into a regexp.
func compileRobots(pattern string) *regexp.Regexp {
	anchored := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	expr := "^" + strings.Join(parts, ".*")
	if anchored {
		expr += "$"
	}
	return regexp.MustCompile(expr)
}

func parseRobots(body, agent string) *robotsRules {
	agent = strings.ToLower(agent)
	type group struct {
		agents []string
		rules  []rule
		delay  time.Duration
	}
	var groups []*group
	var cur *group
	lastWasAgent := false
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if !lastWasAgent || cur == nil {
				cur = &group{}
				groups = append(groups, cur)
			}
			cur.agents = append(cur.agents, strings.ToLower(v))
			lastWasAgent = true
			continue
		case "allow", "disallow":
			if cur != nil {
				cur.rules = append(cur.rules, rule{allow: k == "allow", path: v, re: compileRobots(v)})
			}
		case "request-rate":
			// "requests/seconds", e.g. 1/10 = one request every 10s
			if n, sec, ok := strings.Cut(strings.Fields(v + " ")[0], "/"); ok {
				rq, e1 := strconv.ParseFloat(n, 64)
				sc, e2 := strconv.ParseFloat(strings.TrimSuffix(sec, "s"), 64)
				if cur != nil && e1 == nil && e2 == nil && rq > 0 && sc > 0 && sc/rq < 300 {
					if d := time.Duration(sc / rq * float64(time.Second)); d > cur.delay {
						cur.delay = d
					}
				}
			}
		case "crawl-delay":
			if cur != nil {
				if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f < 300 {
					cur.delay = time.Duration(f * float64(time.Second))
				}
			}
		}
		lastWasAgent = false
	}
	var specific, star *group
	for _, g := range groups {
		for _, a := range g.agents {
			switch {
			case a == "*":
				star = g
			case a != "" && strings.Contains(agent, a):
				specific = g
			}
		}
	}
	g := specific
	if g == nil {
		g = star
	}
	if g == nil {
		return &robotsRules{}
	}
	return &robotsRules{rules: g.rules, crawlDelay: g.delay}
}

// robotsCache fetches and caches robots.txt per host.
type robotsCache struct {
	dl    Downloader
	agent string
	mu    sync.Mutex
	hosts map[string]*robotsEntry
}

type robotsEntry struct {
	once  sync.Once
	rules *robotsRules
}

func newRobotsCache(dl Downloader, agent string) *robotsCache {
	return &robotsCache{dl: dl, agent: agent, hosts: map[string]*robotsEntry{}}
}

func (c *robotsCache) rulesFor(ctx context.Context, u *url.URL) *robotsRules {
	key := u.Scheme + "://" + u.Host
	c.mu.Lock()
	e, ok := c.hosts[key]
	if !ok {
		e = &robotsEntry{}
		c.hosts[key] = e
	}
	c.mu.Unlock()
	e.once.Do(func() {
		e.rules = &robotsRules{} // fail open
		resp, err := c.dl.Do(ctx, fetcher.Request{URL: key + "/robots.txt"})
		if err != nil || resp == nil || resp.Status != 200 {
			return
		}
		e.rules = parseRobots(string(resp.Body), c.agent)
	})
	return e.rules
}

// Allowed reports whether u may be fetched. It also returns any
// Crawl-delay the host asks for.
func (c *robotsCache) Allowed(ctx context.Context, rawURL string) (bool, time.Duration) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return true, 0
	}
	r := c.rulesFor(ctx, u)
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return r.allowed(p), r.crawlDelay
}
