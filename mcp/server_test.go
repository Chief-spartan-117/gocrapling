package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rpc(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := s.Serve(ctx, strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad json line %q: %v", l, err)
		}
		res = append(res, m)
	}
	return res
}

func byID(rs []map[string]any, id float64) map[string]any {
	for _, r := range rs {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

func text(r map[string]any) string {
	c := r["result"].(map[string]any)["content"].([]any)
	return c[0].(map[string]any)["text"].(string)
}

func TestProtocolAndTools(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			return
		}
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<html><body><main><h1>Home</h1><p>hello <a href="/b">b</a></p></main></body></html>`))
		case "/b":
			w.Write([]byte(`<html><body><main><h1>Bee</h1></main></body></html>`))
		}
	}))
	defer srv.Close()
	s := New(Options{AllowPrivate: true})
	defer s.Close()

	args := func(m map[string]any) string { b, _ := json.Marshal(m); return string(b) }
	rs := rpc(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fetch","arguments":`+args(map[string]any{"url": srv.URL + "/", "main_only": true})+`}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fetch","arguments":`+args(map[string]any{"url": srv.URL + "/", "css": "a::attr(href)"})+`}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"crawl","arguments":`+args(map[string]any{"start_url": srv.URL + "/", "fields": map[string]string{"h": "h1::text"}, "max_pages": 5})+`}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"bogus"}`,
	)
	if len(rs) != 7 { // the notification gets no reply
		t.Fatalf("got %d responses: %v", len(rs), rs)
	}
	if v := byID(rs, 1)["result"].(map[string]any)["protocolVersion"]; v != protocolVersion {
		t.Fatalf("protocolVersion %v", v)
	}
	tools := byID(rs, 2)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 4 {
		t.Fatalf("tools = %d", len(tools))
	}
	if md := text(byID(rs, 3)); !strings.Contains(md, "# Home") || !strings.Contains(md, "[b]("+srv.URL+"/b)") {
		t.Fatalf("markdown: %s", md)
	}
	if got := text(byID(rs, 4)); !strings.HasSuffix(got, "/b") {
		t.Fatalf("css: %s", got)
	}
	crawl := text(byID(rs, 5))
	if !strings.Contains(crawl, `"h": "Home"`) || !strings.Contains(crawl, `"h": "Bee"`) {
		t.Fatalf("crawl: %s", crawl)
	}
	if e := byID(rs, 6)["result"].(map[string]any); e["isError"] != true {
		t.Fatalf("unknown tool should be in-band error: %v", e)
	}
	if e := byID(rs, 7)["error"].(map[string]any); e["code"] != float64(-32601) {
		t.Fatalf("method not found: %v", e)
	}
}
