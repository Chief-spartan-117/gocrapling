package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Handler serves MCP over HTTP: POST a JSON-RPC message (or batch) to the
// handler and receive the JSON-RPC response. When token is not empty every
// request must carry "Authorization: Bearer <token>".
func (s *Server) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") ||
				subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="gocrapling"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
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
				writeRPC(w, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
				return
			}
			var outs []response
			for _, m := range msgs {
				if resp, ok := s.dispatch(r.Context(), m); ok {
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
		resp, ok := s.dispatch(r.Context(), body)
		if !ok {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, resp)
	})
	return mux
}

// dispatch handles one raw message; ok is false for notifications.
func (s *Server) dispatch(ctx context.Context, raw []byte) (response, bool) {
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}, true
	}
	if len(req.ID) == 0 {
		_, _ = s.handle(ctx, req)
		return response{}, false
	}
	res, rerr := s.handle(ctx, req)
	if rerr != nil {
		return response{JSONRPC: "2.0", ID: req.ID, Error: rerr}, true
	}
	return response{JSONRPC: "2.0", ID: req.ID, Result: res}, true
}

func writeRPC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ServeHTTP listens on addr until ctx ends.
func (s *Server) ServeHTTP(ctx context.Context, addr, token string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(token), ReadHeaderTimeout: 10 * time.Second}
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
