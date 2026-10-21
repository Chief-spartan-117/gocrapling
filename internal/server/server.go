// Package server composes the HTTP surface: the MCP endpoint (/mcp), the
// REST API (/v1) and a health probe (/healthz), behind one authentication
// and request-validation layer.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Chief-spartan-117/gocrapling/internal/mcp"
	"github.com/Chief-spartan-117/gocrapling/internal/rest"
	"github.com/Chief-spartan-117/gocrapling/internal/service"
)

// Handler returns the full HTTP API. When token is not empty every request
// to /mcp and /v1 must carry "Authorization: Bearer <token>".
func Handler(svc *service.Service, m *mcp.Server, token string) http.Handler {
	mux := http.NewServeMux()
	// Liveness probe for container health checks. Unauthenticated and
	// carries no information.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok\n") })

	api := rest.Handler(svc)
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r, token, r.Method == http.MethodPost) {
			return
		}
		api.ServeHTTP(w, r)
	})

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r, token, true) {
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		body = []byte(strings.TrimSpace(string(body)))
		if len(body) > 0 && body[0] == '[' {
			var msgs []json.RawMessage
			if err := json.Unmarshal(body, &msgs); err != nil {
				writeRPC(w, mcp.Response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcp.RPCError{Code: -32700, Message: "parse error"}})
				return
			}
			var outs []mcp.Response
			for _, msg := range msgs {
				if resp, ok := m.Dispatch(r.Context(), msg); ok {
					outs = append(outs, resp)
				}
			}
			if len(outs) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			writeRPC(w, outs)
			return
		}
		resp, ok := m.Dispatch(r.Context(), body)
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, resp)
	})
	return mux
}

// authorize applies the shared checks for every authenticated endpoint:
// bearer token (constant time), or, without a token, a local-caller check,
// and a JSON content type. It writes the error response itself.
func authorize(w http.ResponseWriter, r *http.Request, token string, needJSON bool) bool {
	if token != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") ||
			subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gocrapling"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
	}
	if token == "" {
		// No bearer token: the only defence is that the caller is a local
		// program. Refuse what a web page in the user's browser could send
		// (cross-origin form posts, DNS-rebinding hostnames).
		if r.Header.Get("Origin") != "" {
			http.Error(w, "cross-origin requests are not allowed", http.StatusForbidden)
			return false
		}
		if !loopbackHost(r.Host) {
			http.Error(w, "unexpected Host header", http.StatusForbidden)
			return false
		}
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); needJSON && mt != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

// loopbackHost reports whether a Host header names the local machine.
func loopbackHost(hostport string) bool {
	h := hostport
	if hh, _, err := net.SplitHostPort(hostport); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func writeRPC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ListenAndServe serves h on addr until ctx ends.
func ListenAndServe(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{
		Addr: addr, Handler: h,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10,
		// No WriteTimeout: a tool call may legitimately run for minutes.
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
