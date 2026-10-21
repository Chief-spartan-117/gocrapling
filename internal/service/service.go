// Package service holds the scraping operations (fetch, screenshot, capture,
// crawl) behind every server interface. The MCP, REST and CLI front ends call
// it; it knows nothing about JSON-RPC or HTTP routing.
package service

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Chief-spartan-117/gocrapling/pkg/fetcher"
	"github.com/Chief-spartan-117/gocrapling/pkg/stealth"
)

// Options configures the service.
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

// Service holds shared fetcher and browser state.
type Service struct {
	opt Options
	log *slog.Logger
	f   *fetcher.Fetcher

	bmu     sync.Mutex
	browser *stealth.Browser
}

// New builds a Service.
func New(opt Options) *Service {
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
	return &Service{opt: opt, log: opt.Logger, f: fetcher.New(fetcher.Options{Proxies: rot, Profile: "chrome", BlockPrivate: !opt.AllowPrivate})}
}

// Close releases the fetcher and browser.
func (s *Service) Close() {
	s.f.Close()
	s.bmu.Lock()
	defer s.bmu.Unlock()
	if s.browser != nil {
		s.browser.Close()
	}
}

func (s *Service) getBrowser() (*stealth.Browser, error) {
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

// statePath maps a client-supplied state name to a file under StateDir.
// Names with separators or dots-only are rejected so a tool call cannot
// write outside the directory.
func (s *Service) statePath(name string) (string, error) {
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
func (s *Service) cut(text string, n int) (string, bool) {
	if n <= 0 {
		n = s.opt.DefaultMaxChars
	}
	r := []rune(text)
	if len(r) <= n {
		return text, false
	}
	return string(r[:n]), true
}

func (s *Service) Truncate(text string, n int) string {
	if n <= 0 {
		n = s.opt.DefaultMaxChars
	}
	r := []rune(text)
	if len(r) <= n {
		return text
	}
	return string(r[:n]) + fmt.Sprintf("\n\n[truncated: %d of %d characters shown]", n, len(r))
}
