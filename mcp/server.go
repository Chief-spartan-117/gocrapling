// Package mcp exposes gocrapling as a Model Context Protocol server over
// stdio (newline-delimited JSON-RPC 2.0), so AI agents can fetch pages,
// extract data, take screenshots, capture API traffic and run small crawls.
package mcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sagarshrestha/gocrapling/fetcher"
	"github.com/sagarshrestha/gocrapling/parser"
	"github.com/sagarshrestha/gocrapling/spider"
	"github.com/sagarshrestha/gocrapling/stealth"
)

const protocolVersion = "2024-11-05"

// Options configures the server.
type Options struct {
	// Proxies rotate for HTTP fetches.
	Proxies []string
	// BrowserOptions are used when a tool needs Chrome (started lazily).
	Browser stealth.Options
	// Logger receives diagnostics; never write to stdout.
	Logger *slog.Logger
	// AllowPrivate lets tools reach loopback, private and link-local
	// addresses. Off by default: tool arguments come from an AI agent, and
	// an agent talked into fetching http://169.254.169.254/ would leak cloud
	// credentials. Only web URLs (http, https) are accepted either way.
	AllowPrivate bool
	// StateDir holds saved login files named by the state_file tool
	// argument. Default: <user cache dir>/gocrapling/state.
	StateDir string
	// DefaultMaxChars truncates tool text. Default 20000.
	DefaultMaxChars int
}

// Server holds shared fetcher and browser state.
type Server struct {
	opt Options
	log *slog.Logger
	f   *fetcher.Fetcher

	bmu     sync.Mutex
	browser *stealth.Browser
}

// New builds a Server.
func New(opt Options) *Server {
	if opt.DefaultMaxChars <= 0 {
		opt.DefaultMaxChars = 20000
	}
	if opt.Logger == nil {
		opt.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	var rot fetcher.ProxyRotator
	if len(opt.Proxies) > 0 {
		rot = fetcher.NewPoolRotator(opt.Proxies, false)
	}
	return &Server{opt: opt, log: opt.Logger, f: fetcher.New(fetcher.Options{Proxies: rot, Profile: "chrome", BlockPrivate: !opt.AllowPrivate})}
}

// Close releases the fetcher and browser.
func (s *Server) Close() {
	s.f.Close()
	s.bmu.Lock()
	defer s.bmu.Unlock()
	if s.browser != nil {
		s.browser.Close()
	}
}

func (s *Server) getBrowser() (*stealth.Browser, error) {
	s.bmu.Lock()
	defer s.bmu.Unlock()
	if s.browser != nil {
		return s.browser, nil
	}
	o := s.opt.Browser
	o.SolveChallenge = true
	o.BlockPrivate = !s.opt.AllowPrivate
	b, err := stealth.New(o)
	if err != nil {
		return nil, err
	}
	s.browser = b
	return b, nil
}

// ---- JSON-RPC plumbing ----

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads requests from in and writes responses to out until in ends
// or ctx is cancelled. Requests are handled concurrently.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	var wmu sync.Mutex
	write := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			s.log.Error("marshal response", "err", err)
			return
		}
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = out.Write(append(b, '\n'))
	}

	var wg sync.WaitGroup
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		isNotification := len(req.ID) == 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, rerr := s.handle(ctx, req)
			if isNotification {
				return
			}
			if rerr != nil {
				write(response{JSONRPC: "2.0", ID: req.ID, Error: rerr})
				return
			}
			write(response{JSONRPC: "2.0", ID: req.ID, Result: res})
		}()
		if ctx.Err() != nil {
			break
		}
	}
	wg.Wait()
	if err := sc.Err(); err != nil {
		return err
	}
	return ctx.Err()
}

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gocrapling", "version": "0.1.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		content, err := s.callTool(ctx, p.Name, p.Arguments)
		if err != nil {
			// Tool failures are reported in-band so the model can react.
			return map[string]any{"isError": true, "content": []any{textContent("error: " + err.Error())}}, nil
		}
		return map[string]any{"content": content}, nil
	default:
		if strings.HasPrefix(req.Method, "notifications/") {
			return nil, nil
		}
		return nil, &rpcError{-32601, "method not found: " + req.Method}
	}
}

func textContent(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// ---- tools ----

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func toolDefs() []map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	boolean := func(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
	integer := func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
	return []map[string]any{
		{
			"name": "fetch",
			"description": "Fetch a web page with a real-browser fingerprint (mode http, fast) or a stealth Chrome (mode browser, runs JavaScript). " +
				"Returns Markdown by default, or the result of a CSS selector (supports ::text and ::attr(name)) or XPath.",
			"inputSchema": obj(map[string]any{
				"url":           str("Page URL"),
				"mode":          map[string]any{"type": "string", "enum": []string{"http", "browser"}, "description": "Default http. Use browser for JS-rendered or heavily protected pages."},
				"css":           str("CSS selector; returns matching texts, one per line. Add ::attr(href) for attributes."),
				"xpath":         str("XPath; returns matching element texts, one per line."),
				"main_only":     boolean("Markdown of <main>/<article> only"),
				"raw_html":      boolean("Return raw HTML instead of Markdown"),
				"max_chars":     integer("Truncate output (default 20000)"),
				"ai_targeted":   boolean("Strip hidden elements and invisible characters from the page before returning it, defending against prompt injection. Use for untrusted pages."),
				"wait_selector": str("Browser mode: wait until this CSS selector exists"),
				"network_idle":  boolean("Browser mode: wait for the network to go quiet after load"),
				"actions":       map[string]any{"type": "array", "description": "Browser mode: page actions run after load, in order. Each item: {type: click|fill|press|wait|wait_for|scroll|evaluate, selector, text, key, ms, pixels, to, script}.", "items": map[string]any{"type": "object"}},
				"state_file":    str("Browser mode: name of a saved login state. Cookies are loaded from it before the fetch and saved back after, so logins persist across calls. Plain file name, no path."),
			}, "url"),
		},
		{
			"name":        "screenshot",
			"description": "Full-page PNG screenshot of a URL rendered in stealth Chrome.",
			"inputSchema": obj(map[string]any{"url": str("Page URL")}, "url"),
		},
		{
			"name":        "capture_requests",
			"description": "Load a page in stealth Chrome and return the background XHR/fetch responses (often the JSON API behind a JS app) whose URL matches a regexp.",
			"inputSchema": obj(map[string]any{
				"url":       str("Page URL"),
				"url_regex": str("Only return requests whose URL matches this regexp (default: all)"),
				"max_chars": integer("Truncate output (default 20000)"),
			}, "url"),
		},
		{
			"name": "crawl",
			"description": "Small polite crawl (robots.txt obeyed) that extracts fields with CSS selectors and returns items as JSON. " +
				"Stays on the start host.",
			"inputSchema": obj(map[string]any{
				"start_url":  str("First URL"),
				"fields":     map[string]any{"type": "object", "description": "name -> CSS selector (supports ::text, ::attr(x))", "additionalProperties": map[string]any{"type": "string"}},
				"follow_css": str("CSS selector yielding links to follow, default a::attr(href)"),
				"allow":      str("Regexp links must match to be followed"),
				"max_pages":  integer("Page budget, default 20, max 200"),
				"depth":      integer("Link depth, default 2"),
			}, "start_url", "fields"),
		},
	}
}

func (s *Server) callTool(ctx context.Context, name string, raw json.RawMessage) ([]any, error) {
	switch name {
	case "fetch":
		return s.toolFetch(ctx, raw)
	case "screenshot":
		return s.toolScreenshot(ctx, raw)
	case "capture_requests":
		return s.toolCapture(ctx, raw)
	case "crawl":
		return s.toolCrawl(ctx, raw)
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

// statePath maps a client-supplied state name to a file under StateDir.
// Names with separators or dots-only are rejected so a tool call cannot
// write outside the directory.
func (s *Server) statePath(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("state_file must be a plain file name, got %q", name)
	}
	dir := s.opt.StateDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "gocrapling", "state")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if !strings.HasSuffix(name, ".json") {
		name += ".json"
	}
	return filepath.Join(dir, name), nil
}

// cut shortens text to n characters (default Options.DefaultMaxChars).
func (s *Server) cut(text string, n int) (string, bool) {
	if n <= 0 {
		n = s.opt.DefaultMaxChars
	}
	r := []rune(text)
	if len(r) <= n {
		return text, false
	}
	return string(r[:n]), true
}

func (s *Server) truncate(text string, n int) string {
	if n <= 0 {
		n = s.opt.DefaultMaxChars
	}
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n]) + fmt.Sprintf("\n\n[truncated: %d of %d characters shown]", n, len(r))
}

// FetchArgs are the inputs of the fetch tool and of POST /v1/fetch.
type FetchArgs struct {
	URL      string `json:"url"`
	Mode     string `json:"mode"`
	CSS      string `json:"css"`
	XPath    string `json:"xpath"`
	MainOnly bool   `json:"main_only"`
	RawHTML  bool   `json:"raw_html"`
	MaxChars int    `json:"max_chars"`

	AITargeted   bool            `json:"ai_targeted"`
	WaitSelector string          `json:"wait_selector"`
	NetworkIdle  bool            `json:"network_idle"`
	Actions      json.RawMessage `json:"actions"`
	StateFile    string          `json:"state_file"`
}

// FetchResult is what a fetch produced.
type FetchResult struct {
	Status  int    `json:"status"`
	URL     string `json:"url"`
	Blocked bool   `json:"blocked"`
	// Warning carries a non-fatal error, e.g. a block page that survived retries.
	Warning string `json:"warning,omitempty"`
	// Content is Markdown, raw HTML, or the matches joined by newlines.
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
	// Results lists each match when css or xpath was given.
	Results []string `json:"results,omitempty"`
}

func (s *Server) toolFetch(ctx context.Context, raw json.RawMessage) ([]any, error) {
	var a FetchArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.URL == "" {
		return nil, errors.New("url is required")
	}
	res, err := s.Fetch(ctx, a)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf("HTTP %d %s", res.Status, res.URL)
	if res.Warning != "" {
		header += " (" + res.Warning + ")"
	}
	body := res.Content
	if res.Truncated {
		body += "\n\n[truncated]"
	}
	return []any{textContent(header + "\n\n" + body)}, nil
}

// Fetch downloads and extracts one page.
func (s *Server) Fetch(ctx context.Context, a FetchArgs) (*FetchResult, error) {
	if a.URL == "" {
		return nil, errors.New("url is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	browserOnly := a.WaitSelector != "" || a.NetworkIdle || len(a.Actions) > 0 || a.StateFile != ""
	var resp *fetcher.Response
	var err error
	if a.Mode == "browser" || browserOnly {
		b, berr := s.getBrowser()
		if berr != nil {
			return nil, berr
		}
		fo := stealth.FetchOptions{WaitSelector: a.WaitSelector, WaitIdle: a.NetworkIdle}
		if len(a.Actions) > 0 {
			steps, serr := stealth.ParseSteps(a.Actions)
			if serr != nil {
				return nil, fmt.Errorf("actions: %w", serr)
			}
			if fo.Actions, serr = stealth.Actions(steps); serr != nil {
				return nil, fmt.Errorf("actions: %w", serr)
			}
		}
		statePath := ""
		if a.StateFile != "" {
			if statePath, err = s.statePath(a.StateFile); err != nil {
				return nil, err
			}
			if lerr := b.LoadState(ctx, statePath); lerr != nil {
				s.log.Warn("load state", "err", lerr)
			}
		}
		resp, err = b.Fetch(ctx, a.URL, fo)
		if statePath != "" && resp != nil {
			if serr := b.SaveState(ctx, statePath); serr != nil {
				s.log.Warn("save state", "err", serr)
			}
		}
	} else {
		resp, err = s.f.Get(ctx, a.URL)
	}
	if resp == nil {
		return nil, err
	}
	out := &FetchResult{Status: resp.Status, URL: resp.URL, Blocked: resp.Blocked}
	if err != nil {
		out.Warning = err.Error()
	}

	var body string
	switch {
	case a.RawHTML:
		body = resp.Text()
	default:
		doc, perr := resp.Parse()
		if perr != nil {
			return nil, perr
		}
		switch {
		case a.CSS != "":
			out.Results = doc.Extract(a.CSS)
		case a.XPath != "":
			sel, xerr := doc.XPath(a.XPath)
			if xerr != nil {
				return nil, xerr
			}
			out.Results = sel.Texts()
		default:
			body = doc.MarkdownWith(parser.MarkdownOptions{MainOnly: a.MainOnly, Safe: a.AITargeted})
		}
		if out.Results != nil {
			if a.AITargeted {
				for i := range out.Results {
					out.Results[i] = parser.CleanInvisible(out.Results[i])
				}
			}
			body = strings.Join(out.Results, "\n")
		}
	}
	out.Content, out.Truncated = s.cut(body, a.MaxChars)
	return out, nil
}

// Screenshot renders url in stealth Chrome and returns a full-page PNG.
func (s *Server) Screenshot(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, errors.New("url is required")
	}
	b, err := s.getBrowser()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := b.Fetch(ctx, url, stealth.FetchOptions{Screenshot: true})
	if err != nil {
		return nil, err
	}
	return resp.Screenshot, nil
}

func (s *Server) toolScreenshot(ctx context.Context, raw json.RawMessage) ([]any, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.URL == "" {
		return nil, errors.New("url is required")
	}
	png, err := s.Screenshot(ctx, a.URL)
	if err != nil {
		return nil, err
	}
	return []any{map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(png), "mimeType": "image/png"}}, nil
}

// CaptureArgs are the inputs of capture_requests and POST /v1/capture.
type CaptureArgs struct {
	URL      string `json:"url"`
	Regex    string `json:"url_regex"`
	MaxChars int    `json:"max_chars"`
}

// Captured is one background request a page made.
type Captured struct {
	URL      string `json:"url"`
	Method   string `json:"method"`
	Status   int    `json:"status"`
	MimeType string `json:"mime_type"`
	Body     string `json:"body"`
}

// Capture loads a page in Chrome and returns the XHR/fetch responses it made.
func (s *Server) Capture(ctx context.Context, a CaptureArgs) ([]Captured, error) {
	if a.URL == "" {
		return nil, errors.New("url is required")
	}
	var re *regexp.Regexp
	if a.Regex != "" {
		var err error
		if re, err = regexp.Compile(a.Regex); err != nil {
			return nil, fmt.Errorf("bad url_regex: %w", err)
		}
	}
	b, err := s.getBrowser()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := b.Fetch(ctx, a.URL, stealth.FetchOptions{Record: true, CaptureFilter: re})
	if err != nil {
		return nil, err
	}
	out := make([]Captured, 0, len(resp.Captured))
	for _, c := range resp.Captured {
		out = append(out, Captured{URL: c.URL, Method: c.Method, Status: c.Status, MimeType: c.MimeType, Body: string(c.Body)})
	}
	return out, nil
}

func (s *Server) toolCapture(ctx context.Context, raw json.RawMessage) ([]any, error) {
	var a CaptureArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.URL == "" {
		return nil, errors.New("url is required")
	}
	caps, err := s.Capture(ctx, a)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d captured responses\n", len(caps))
	for _, c := range caps {
		fmt.Fprintf(&sb, "\n# %s %s -> %d %s\n%s\n", c.Method, c.URL, c.Status, c.MimeType, c.Body)
	}
	return []any{textContent(s.truncate(sb.String(), a.MaxChars))}, nil
}

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
func (s *Server) Crawl(ctx context.Context, a CrawlArgs) (*CrawlResult, error) {
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

func (s *Server) toolCrawl(ctx context.Context, raw json.RawMessage) ([]any, error) {
	var a CrawlArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.StartURL == "" || len(a.Fields) == 0 {
		return nil, errors.New("start_url and fields are required")
	}
	res, err := s.Crawl(ctx, a)
	if err != nil {
		return nil, err
	}
	b, _ := json.MarshalIndent(map[string]any{"stats": res.Stats, "items": res.Items}, "", "  ")
	out := s.truncate(string(b), 0)
	if res.Error != "" {
		out += "\n(crawl ended early: " + res.Error + ")"
	}
	return []any{textContent(out)}, nil
}
