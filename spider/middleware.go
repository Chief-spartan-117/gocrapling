package spider

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sagarshrestha/gocrapling/fetcher"
)

var (
	// ErrDrop returned from a hook discards the request silently.
	ErrDrop = errors.New("spider: drop request")
	// ErrRetry returned from OnResponse re-queues the request, at most
	// Config.MaxHookRetries times.
	ErrRetry = errors.New("spider: retry request")
)

// Middleware hooks into the request lifecycle. Both funcs are optional.
// Hooks run in Config.Middlewares order and must be safe for concurrent use.
// OnResponse is also the place to plug in a CAPTCHA-solving service: detect
// the challenge, solve it, set cookies or headers on req, return ErrRetry.
type Middleware struct {
	Name string
	// OnRequest may mutate req (headers, meta, proxy-affecting fields).
	// Return ErrDrop to skip it; any other error fails the request.
	OnRequest func(ctx context.Context, req *Request) error
	// OnResponse sees every response, including blocked ones. Return
	// ErrRetry to try again, ErrDrop to discard, nil to continue.
	OnResponse func(ctx context.Context, req *Request, resp *fetcher.Response) error
}

// Mode picks the downloader for a host.
type Mode int

const (
	// ModeAuto uses HTTP, escalating to the browser if configured.
	ModeAuto Mode = iota
	// ModeHTTP never uses the browser for this host.
	ModeHTTP
	// ModeBrowser always uses the browser for this host.
	ModeBrowser
)

// Route overrides crawl behaviour for one host (and its subdomains).
type Route struct {
	Host  string
	Mode  Mode
	Delay time.Duration // overrides Config.DownloadDelay when > 0
}

func (e *Engine) route(host string) Route {
	host = strings.ToLower(host)
	for _, r := range e.cfg.Routes {
		h := strings.ToLower(strings.TrimPrefix(r.Host, "."))
		if host == h || strings.HasSuffix(host, "."+h) {
			return r
		}
	}
	return Route{}
}

func (e *Engine) runRequestHooks(ctx context.Context, req *Request) error {
	for _, m := range e.cfg.Middlewares {
		if m.OnRequest == nil {
			continue
		}
		if err := m.OnRequest(ctx, req); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) runResponseHooks(ctx context.Context, req *Request, resp *fetcher.Response) error {
	for _, m := range e.cfg.Middlewares {
		if m.OnResponse == nil {
			continue
		}
		if err := m.OnResponse(ctx, req, resp); err != nil {
			return err
		}
	}
	return nil
}

const retryMeta = "_hook_retries"

// requeue schedules a copy of req again, bypassing de-duplication.
func (e *Engine) requeue(req *Request) bool {
	n, _ := req.Meta[retryMeta].(float64) // JSON round-trips numbers as float64
	if i, ok := req.Meta[retryMeta].(int); ok {
		n = float64(i)
	}
	max := e.cfg.MaxHookRetries
	if max <= 0 {
		max = 3
	}
	if int(n) >= max {
		return false
	}
	cp := *req
	cp.Meta = make(map[string]any, len(req.Meta)+1)
	for k, v := range req.Meta {
		cp.Meta[k] = v
	}
	cp.Meta[retryMeta] = int(n) + 1
	cp.DontFilter = true
	return e.sched.Push(&cp)
}
