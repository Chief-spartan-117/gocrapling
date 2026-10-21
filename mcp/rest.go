package mcp

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/sagarshrestha/gocrapling/netguard"
)

//go:embed openapi.json
var openapiJSON []byte

// restHandler serves the JSON API under /v1. It shares the MCP server's
// fetcher, browser, SSRF guard and authentication.
func (s *Server) restHandler(token string) http.Handler {
	mux := http.NewServeMux()
	post := func(path string, h func(w http.ResponseWriter, r *http.Request, body []byte)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if !authorize(w, r, token, r.Method == http.MethodPost) {
				return
			}
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				apiError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST only")
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
			if err != nil {
				apiError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body over 1 MiB")
				return
			}
			h(w, r, body)
		})
	}

	mux.HandleFunc("/v1/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r, token, false) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openapiJSON)
	})

	post("/v1/fetch", func(w http.ResponseWriter, r *http.Request, body []byte) {
		var a FetchArgs
		if !decode(w, body, &a) || !validURL(w, a.URL) {
			return
		}
		res, err := s.Fetch(r.Context(), a)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	post("/v1/screenshot", func(w http.ResponseWriter, r *http.Request, body []byte) {
		var a struct {
			URL string `json:"url"`
		}
		if !decode(w, body, &a) || !validURL(w, a.URL) {
			return
		}
		png, err := s.Screenshot(r.Context(), a.URL)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mime_type": "image/png", "data": base64.StdEncoding.EncodeToString(png)})
	})
	post("/v1/capture", func(w http.ResponseWriter, r *http.Request, body []byte) {
		var a CaptureArgs
		if !decode(w, body, &a) || !validURL(w, a.URL) {
			return
		}
		caps, err := s.Capture(r.Context(), a)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"captured": caps})
	})
	post("/v1/crawl", func(w http.ResponseWriter, r *http.Request, body []byte) {
		var a CrawlArgs
		if !decode(w, body, &a) || !validURL(w, a.StartURL) {
			return
		}
		if len(a.Fields) == 0 {
			apiError(w, http.StatusBadRequest, "invalid_request", "fields is required")
			return
		}
		res, err := s.Crawl(r.Context(), a)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	return mux
}

func decode(w http.ResponseWriter, body []byte, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request", "bad JSON body: "+err.Error())
		return false
	}
	return true
}

func validURL(w http.ResponseWriter, raw string) bool {
	u, err := url.Parse(raw)
	if raw == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		apiError(w, http.StatusBadRequest, "invalid_url", "url must be an absolute http or https URL")
		return false
	}
	return true
}

// fail maps an error to a status: the SSRF guard is a policy refusal (403),
// everything else is an upstream failure (502).
func fail(w http.ResponseWriter, err error) {
	if errors.Is(err, netguard.ErrBlocked) {
		apiError(w, http.StatusForbidden, "target_not_allowed", err.Error())
		return
	}
	apiError(w, http.StatusBadGateway, "fetch_failed", err.Error())
}

func apiError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
