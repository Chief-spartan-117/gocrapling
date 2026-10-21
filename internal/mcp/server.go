// Package mcp speaks the Model Context Protocol (JSON-RPC 2.0) so AI agents
// can use the scraping service as tools. It handles one message at a time
// through Dispatch (used by the HTTP server) or newline-delimited streams
// through Serve (stdio).
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
	"strings"
	"sync"

	"github.com/Chief-spartan-117/gocrapling/internal/service"
)

// Server exposes a service.Service as MCP tools.
type Server struct {
	svc *service.Service
	log *slog.Logger
}

// New builds a Server over svc. Log lines go to log (never stdout, which
// carries the protocol); nil discards them.
func New(svc *service.Service, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{svc: svc, log: log}
}

// Dispatch handles one raw JSON-RPC message. ok is false for notifications,
// which get no response.
func (s *Server) Dispatch(ctx context.Context, raw []byte) (resp Response, ok bool) {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return Response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &RPCError{-32700, "parse error"}}, true
	}
	if len(req.ID) == 0 {
		_, _ = s.handle(ctx, req)
		return Response{}, false
	}
	res, rerr := s.handle(ctx, req)
	if rerr != nil {
		return Response{JSONRPC: "2.0", ID: req.ID, Error: rerr}, true
	}
	return Response{JSONRPC: "2.0", ID: req.ID, Result: res}, true
}

// Version is reported to clients in the initialize response. The binary sets
// it at start-up.
var Version = "dev"

const protocolVersion = "2024-11-05"

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
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
			write(Response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &RPCError{-32700, "parse error"}})
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
				write(Response{JSONRPC: "2.0", ID: req.ID, Error: rerr})
				return
			}
			write(Response{JSONRPC: "2.0", ID: req.ID, Result: res})
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

func (s *Server) handle(ctx context.Context, req request) (any, *RPCError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gocrapling", "version": Version},
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
			return nil, &RPCError{-32602, "invalid params"}
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
		return nil, &RPCError{-32601, "method not found: " + req.Method}
	}
}

func textContent(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

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

func (s *Server) toolFetch(ctx context.Context, raw json.RawMessage) ([]any, error) {
	var a service.FetchArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.URL == "" {
		return nil, errors.New("url is required")
	}
	res, err := s.svc.Fetch(ctx, a)
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

func (s *Server) toolScreenshot(ctx context.Context, raw json.RawMessage) ([]any, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.URL == "" {
		return nil, errors.New("url is required")
	}
	png, err := s.svc.Screenshot(ctx, a.URL)
	if err != nil {
		return nil, err
	}
	return []any{map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(png), "mimeType": "image/png"}}, nil
}

func (s *Server) toolCapture(ctx context.Context, raw json.RawMessage) ([]any, error) {
	var a service.CaptureArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.URL == "" {
		return nil, errors.New("url is required")
	}
	caps, err := s.svc.Capture(ctx, a)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d captured responses\n", len(caps))
	for _, c := range caps {
		fmt.Fprintf(&sb, "\n# %s %s -> %d %s\n%s\n", c.Method, c.URL, c.Status, c.MimeType, c.Body)
	}
	return []any{textContent(s.svc.Truncate(sb.String(), a.MaxChars))}, nil
}

func (s *Server) toolCrawl(ctx context.Context, raw json.RawMessage) ([]any, error) {
	var a service.CrawlArgs
	if err := json.Unmarshal(raw, &a); err != nil || a.StartURL == "" || len(a.Fields) == 0 {
		return nil, errors.New("start_url and fields are required")
	}
	res, err := s.svc.Crawl(ctx, a)
	if err != nil {
		return nil, err
	}
	b, _ := json.MarshalIndent(map[string]any{"stats": res.Stats, "items": res.Items}, "", "  ")
	out := s.svc.Truncate(string(b), 0)
	if res.Error != "" {
		out += "\n(crawl ended early: " + res.Error + ")"
	}
	return []any{textContent(out)}, nil
}
