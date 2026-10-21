package server

import (
	"net/http"

	"github.com/Chief-spartan-117/gocrapling/internal/mcp"
	"github.com/Chief-spartan-117/gocrapling/internal/service"
)

// testServer is a service that can build its HTTP handler, for tests.
type testServer struct{ *service.Service }

func newTestServer(opt service.Options) *testServer { return &testServer{service.New(opt)} }

func (s *testServer) Handler(token string) http.Handler {
	return Handler(s.Service, mcp.New(s.Service, nil), token)
}
