package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/spider"
)

// CrawlArgs are the inputs of the crawl tool and POST /v1/crawl.
type CrawlArgs struct {
	StartURL  string            `json:"start_url"`
	Fields    map[string]string `json:"fields"`
	FollowCSS string            `json:"follow_css"`
	Allow     string            `json:"allow"`
	MaxPages  int64             `json:"max_pages"`
	Depth     int               `json:"depth"`
}

// CrawlResult is what a crawl produced. Error is set when it ended early;
// Items still holds what was scraped until then.
type CrawlResult struct {
	Stats string `json:"stats"`
	Items []any  `json:"items"`
	Error string `json:"error,omitempty"`
}

// Crawl runs a small polite crawl (robots.txt obeyed, same host only).
func (s *Service) Crawl(ctx context.Context, a CrawlArgs) (*CrawlResult, error) {
	if a.StartURL == "" || len(a.Fields) == 0 {
		return nil, errors.New("start_url and fields are required")
	}
	if a.MaxPages <= 0 {
		a.MaxPages = 20
	}
	if a.MaxPages > 200 {
		a.MaxPages = 200
	}
	if a.Depth <= 0 {
		a.Depth = 2
	}
	le := spider.LinkExtractor{Restrict: a.FollowCSS}
	if a.Allow != "" {
		re, err := regexp.Compile(a.Allow)
		if err != nil {
			return nil, fmt.Errorf("bad allow regexp: %w", err)
		}
		le.Allow = []*regexp.Regexp{re}
	}

	sp := &spider.Spider{Name: "mcp", StartURLs: []string{a.StartURL}, Callbacks: map[string]spider.Callback{
		"parse": func(ctx context.Context, r *spider.Response, o *spider.Emitter) error {
			doc, err := r.Doc()
			if err != nil {
				return err
			}
			item := map[string]any{"url": r.URL}
			for name, sel := range a.Fields {
				switch v := doc.Extract(sel); len(v) {
				case 0:
					item[name] = nil
				case 1:
					item[name] = v[0]
				default:
					item[name] = v
				}
			}
			o.Item(item)
			return o.FollowLinks(le, "parse")
		},
	}}
	if i := strings.Index(a.StartURL, "://"); i >= 0 {
		sp.AllowedDomains = []string{strings.SplitN(a.StartURL[i+3:], "/", 2)[0]}
	}

	var mu sync.Mutex
	items := []any{}
	eng, err := spider.NewEngine(spider.Config{
		HTTP: s.f, Concurrency: 4, PerDomainConcurrency: 2,
		DownloadDelay: 300 * time.Millisecond, RandomizeDelay: true,
		MaxDepth: a.Depth, MaxRequests: a.MaxPages, RespectRobots: true, RobotsUserAgent: "gocrapling",
		AutoThrottle: spider.AutoThrottle{Enabled: true},
		Pipelines: []spider.Pipeline{spider.FuncPipeline(func(_ context.Context, it any) error {
			mu.Lock()
			items = append(items, it)
			mu.Unlock()
			return nil
		})},
		Logger: s.log,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	stats, runErr := eng.Run(ctx, sp)
	res := &CrawlResult{Stats: stats.String(), Items: items}
	if runErr != nil {
		res.Error = runErr.Error()
	}
	return res, nil
}
