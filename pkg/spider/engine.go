package spider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Chief-spartan-117/gocrapling/pkg/fetcher"
)

// Downloader fetches a request. fetcher.Fetcher and stealth.Browser both
// satisfy it.
type Downloader interface {
	Do(ctx context.Context, req fetcher.Request) (*fetcher.Response, error)
}

// Config tunes the engine.
type Config struct {
	// HTTP is the default downloader. Required.
	HTTP Downloader
	// Sessions are named downloaders chosen per request with
	// Request.Session, e.g. one fetcher per proxy pool or identity.
	Sessions map[string]Downloader
	// Browser handles Request.Browser and block escalation. Optional.
	Browser Downloader
	// EscalateOnBlock retries a blocked HTTP request through Browser.
	EscalateOnBlock bool

	Concurrency          int           // total workers, default 8
	PerDomainConcurrency int           // default 2
	DownloadDelay        time.Duration // min gap between requests to a host
	RandomizeDelay       bool          // scale delay by 0.5-1.5x
	MaxDepth             int           // 0 = unlimited
	MaxRequests          int64         // 0 = unlimited
	RespectRobots        bool
	RobotsUserAgent      string // default "*"-group only
	// AllowStatus lists non-2xx statuses still passed to callbacks.
	AllowStatus []int

	// AutoThrottle adapts per-host delay to latency and blocking. When on,
	// the effective delay is the larger of this and DownloadDelay/Route.Delay.
	AutoThrottle AutoThrottle
	// CacheDir enables development mode: responses are cached on disk and
	// replayed on later runs.
	CacheDir string

	// Middlewares run around every download, in order.
	Middlewares []Middleware
	// MaxHookRetries caps ErrRetry re-queues per request. Default 3.
	MaxHookRetries int
	// Routes override downloader mode and delay per host. First match wins.
	Routes []Route

	Pipelines []Pipeline
	// CheckpointPath enables pause/resume. The file is removed on a
	// complete crawl and kept when interrupted.
	CheckpointPath     string
	CheckpointInterval time.Duration // default 30s

	Logger *slog.Logger
}

// Stats counts crawl events.
type Stats struct {
	Requests       atomic.Int64
	Responses      atomic.Int64
	Items          atomic.Int64
	Errors         atomic.Int64
	Blocked        atomic.Int64
	Escalated      atomic.Int64
	RobotsDenied   atomic.Int64
	Filtered       atomic.Int64 // dedup, offsite, depth
	Non2xx         atomic.Int64
	CallbackErrors atomic.Int64
}

func (s *Stats) String() string {
	return fmt.Sprintf("requests=%d responses=%d items=%d errors=%d blocked=%d escalated=%d robots_denied=%d filtered=%d non2xx=%d callback_errors=%d",
		s.Requests.Load(), s.Responses.Load(), s.Items.Load(), s.Errors.Load(), s.Blocked.Load(),
		s.Escalated.Load(), s.RobotsDenied.Load(), s.Filtered.Load(), s.Non2xx.Load(), s.CallbackErrors.Load())
}

// Engine runs a Spider.
type Engine struct {
	cfg    Config
	log    *slog.Logger
	sched  *scheduler
	robots *robotsCache
	stats  Stats

	pipeMu sync.Mutex
	stream chan any

	domMu   sync.Mutex
	domains map[string]*domainLimiter
}

// NewEngine validates cfg and applies defaults.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.HTTP == nil {
		return nil, errors.New("spider: Config.HTTP is required")
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 8
	}
	if cfg.PerDomainConcurrency <= 0 {
		cfg.PerDomainConcurrency = 2
	}
	if cfg.CheckpointInterval <= 0 {
		cfg.CheckpointInterval = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	cfg.AutoThrottle = cfg.AutoThrottle.withDefaults()
	if cfg.CacheDir != "" {
		var err error
		if cfg.HTTP, err = NewCachingDownloader(cfg.HTTP, cfg.CacheDir); err != nil {
			return nil, err
		}
		if cfg.Browser != nil {
			if cfg.Browser, err = NewCachingDownloader(cfg.Browser, cfg.CacheDir); err != nil {
				return nil, err
			}
		}
	}
	e := &Engine{cfg: cfg, log: cfg.Logger, sched: newScheduler(), domains: map[string]*domainLimiter{}}
	if cfg.RespectRobots {
		e.robots = newRobotsCache(cfg.HTTP, cfg.RobotsUserAgent)
	}
	return e, nil
}

// Stream runs the crawl in the background and yields items as they are
// scraped. The channel closes when the crawl ends; call wait afterwards for
// the final stats and error. Items are delivered with backpressure, so
// consume the channel (or cancel ctx) to avoid stalling the crawl. Pipelines
// still run. Live counters are available from Stats while it runs.
func (e *Engine) Stream(ctx context.Context, sp *Spider) (items <-chan any, wait func() (*Stats, error)) {
	ch := make(chan any, 64)
	e.stream = ch
	var (
		st  *Stats
		err error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(ch)
		st, err = e.Run(ctx, sp)
	}()
	return ch, func() (*Stats, error) { <-done; return st, err }
}

// Stats returns live counters.
func (e *Engine) Stats() *Stats { return &e.stats }

// Run crawls until the queue drains or ctx ends. On cancellation with a
// checkpoint path set, state is saved so a later Run resumes.
func (e *Engine) Run(ctx context.Context, sp *Spider) (*Stats, error) {
	if sp.Callbacks["parse"] == nil {
		return &e.stats, errors.New(`spider: callback "parse" is required`)
	}

	resumed := false
	if e.cfg.CheckpointPath != "" {
		cp, err := loadCheckpoint(e.cfg.CheckpointPath)
		if err != nil {
			return &e.stats, fmt.Errorf("spider: load checkpoint: %w", err)
		}
		if cp != nil && cp.Spider == sp.Name {
			e.sched.Restore(cp.Requests, cp.Seen)
			resumed = true
			e.log.Info("resumed from checkpoint", "queued", len(cp.Requests), "seen", len(cp.Seen))
		}
	}
	if !resumed {
		var starts []*Request
		if sp.StartRequests != nil {
			starts = sp.StartRequests()
		}
		for _, u := range sp.StartURLs {
			starts = append(starts, &Request{URL: u})
		}
		for _, r := range starts {
			e.enqueue(sp, r)
		}
	}

	// Wake blocked workers when the context ends.
	stopWake := context.AfterFunc(ctx, e.sched.Wake)
	defer stopWake()

	var wg sync.WaitGroup
	for i := 0; i < e.cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.worker(ctx, sp)
		}()
	}

	cpStop, cpDone := make(chan struct{}), make(chan struct{})
	if e.cfg.CheckpointPath != "" {
		go e.checkpointLoop(sp, cpStop, cpDone)
	} else {
		close(cpDone)
	}

	wg.Wait()
	// Stop the periodic saver before the final save.
	close(cpStop)
	<-cpDone

	var closeErr error
	for _, p := range e.cfg.Pipelines {
		if err := p.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}

	if e.cfg.CheckpointPath != "" {
		if ctx.Err() != nil {
			if err := e.saveCP(sp); err != nil {
				closeErr = errors.Join(closeErr, err)
			} else {
				e.log.Info("interrupted, checkpoint saved", "path", e.cfg.CheckpointPath)
			}
		} else {
			_ = os.Remove(e.cfg.CheckpointPath)
		}
	}
	e.log.Info("crawl finished", "stats", e.stats.String())
	if ctx.Err() != nil {
		return &e.stats, errors.Join(ctx.Err(), closeErr)
	}
	return &e.stats, closeErr
}

func (e *Engine) checkpointLoop(sp *Spider, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(e.cfg.CheckpointInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := e.saveCP(sp); err != nil {
				e.log.Warn("checkpoint failed", "err", err)
			}
		}
	}
}

func (e *Engine) saveCP(sp *Spider) error {
	reqs, seen := e.sched.Snapshot()
	return saveCheckpoint(e.cfg.CheckpointPath, checkpoint{Spider: sp.Name, Requests: reqs, Seen: seen})
}

// enqueue applies depth/domain filters and schedules.
func (e *Engine) enqueue(sp *Spider, r *Request) {
	if e.cfg.MaxDepth > 0 && r.Depth > e.cfg.MaxDepth {
		e.stats.Filtered.Add(1)
		return
	}
	if !sp.domainAllowed(r.URL) {
		e.stats.Filtered.Add(1)
		return
	}
	if r.Method == "" {
		r.Method = "GET"
	}
	if !e.sched.Push(r) {
		e.stats.Filtered.Add(1)
	}
}

func (e *Engine) worker(ctx context.Context, sp *Spider) {
	for {
		req, ok := e.sched.Pop(ctx)
		if !ok {
			return
		}
		if interrupted := e.process(ctx, sp, req); interrupted {
			// Leave req active so the final checkpoint keeps it.
			return
		}
		e.sched.Done(req)
	}
}

// process handles one request. It returns true when ctx ended mid-request.
func (e *Engine) process(ctx context.Context, sp *Spider, req *Request) (interrupted bool) {
	u, err := url.Parse(req.URL)
	if err != nil || u.Host == "" {
		e.fail(sp, req, fmt.Errorf("invalid url %q", req.URL))
		return false
	}

	if err := e.runRequestHooks(ctx, req); err != nil {
		if errors.Is(err, ErrDrop) {
			e.stats.Filtered.Add(1)
		} else {
			e.fail(sp, req, err)
		}
		return false
	}

	rt := e.route(u.Hostname())
	var crawlDelay time.Duration
	if e.robots != nil {
		allowed, cd := e.robots.Allowed(ctx, req.URL)
		if ctx.Err() != nil {
			return true
		}
		if !allowed {
			e.stats.RobotsDenied.Add(1)
			e.log.Debug("robots.txt denied", "url", req.URL)
			return false
		}
		crawlDelay = cd
	}

	if n := e.stats.Requests.Add(1); e.cfg.MaxRequests > 0 && n > e.cfg.MaxRequests {
		e.stats.Requests.Add(-1)
		e.stats.Filtered.Add(1)
		return false
	}

	delay := e.cfg.DownloadDelay
	if rt.Delay > 0 {
		delay = rt.Delay
	}
	if crawlDelay > delay {
		delay = crawlDelay
	}
	lim := e.limiter(u.Hostname())
	if e.cfg.AutoThrottle.Enabled {
		if d := lim.throttle.current(); d > delay {
			delay = d
		}
	}
	if err := lim.acquire(ctx, delay, e.cfg.RandomizeDelay); err != nil {
		return true
	}
	began := time.Now()
	resp, err := e.download(ctx, req, rt.Mode)
	lim.release()
	if err == nil && resp != nil && sp.IsBlocked != nil && sp.IsBlocked(resp) {
		resp.Blocked = true
		e.stats.Blocked.Add(1)
		if e.cfg.EscalateOnBlock && e.cfg.Browser != nil && !req.Browser && rt.Mode != ModeHTTP && ctx.Err() == nil {
			e.stats.Escalated.Add(1)
			resp, err = e.cfg.Browser.Do(ctx, fetcher.Request{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body})
			if err == nil && resp != nil && sp.IsBlocked(resp) {
				resp.Blocked = true
			} else if resp != nil {
				resp.Blocked = false
			}
		}
		if resp != nil && resp.Blocked {
			if e.requeue(req) {
				return false
			}
			err = fetcher.ErrBlocked
		}
	}
	if e.cfg.AutoThrottle.Enabled && ctx.Err() == nil {
		status, blocked := 0, errors.Is(err, fetcher.ErrBlocked)
		var retryAfter time.Duration
		if resp != nil {
			status, blocked = resp.Status, blocked || resp.Blocked
			retryAfter = parseRetryAfter(resp.Headers.Get("Retry-After"))
		} else if err != nil {
			blocked = false
			status = 599 // transport failure: slow down, do not speed up
		}
		lim.throttle.observe(e.cfg.AutoThrottle, time.Since(began), status, blocked, retryAfter)
	}

	if ctx.Err() != nil {
		return true
	}
	if resp != nil {
		switch herr := e.runResponseHooks(ctx, req, resp); {
		case errors.Is(herr, ErrRetry):
			if e.requeue(req) {
				return false
			}
			e.fail(sp, req, errors.New("retry limit reached in middleware"))
			return false
		case errors.Is(herr, ErrDrop):
			e.stats.Filtered.Add(1)
			return false
		case herr != nil:
			e.fail(sp, req, herr)
			return false
		}
	}
	if err != nil {
		if errors.Is(err, fetcher.ErrBlocked) {
			e.stats.Blocked.Add(1)
		}
		e.fail(sp, req, err)
		return false
	}
	e.stats.Responses.Add(1)

	if resp.Status < 200 || resp.Status >= 300 {
		e.stats.Non2xx.Add(1)
		if !e.statusAllowed(resp.Status) {
			e.fail(sp, req, fmt.Errorf("status %d", resp.Status))
			return false
		}
	}

	name := req.Callback
	if name == "" {
		name = "parse"
	}
	cb := sp.Callbacks[name]
	if cb == nil {
		e.fail(sp, req, fmt.Errorf("unknown callback %q", name))
		return false
	}
	r := &Response{Response: resp, Request: req}
	out := &Emitter{resp: r}
	if err := safeCall(ctx, cb, r, out); err != nil {
		e.stats.CallbackErrors.Add(1)
		e.log.Error("callback failed", "url", req.URL, "callback", name, "err", err)
	}
	for _, it := range out.items {
		e.emitItem(ctx, it)
	}
	for _, nr := range out.reqs {
		e.enqueue(sp, nr)
	}
	return ctx.Err() != nil
}

func (e *Engine) statusAllowed(s int) bool {
	for _, a := range e.cfg.AllowStatus {
		if a == s {
			return true
		}
	}
	return false
}

func safeCall(ctx context.Context, cb Callback, r *Response, out *Emitter) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return cb(ctx, r, out)
}

func (e *Engine) fail(sp *Spider, req *Request, err error) {
	e.stats.Errors.Add(1)
	e.log.Warn("request failed", "url", req.URL, "err", err)
	if sp.OnError != nil {
		sp.OnError(req, err)
	}
}

func (e *Engine) emitItem(ctx context.Context, it any) {
	e.pipeMu.Lock()
	defer e.pipeMu.Unlock()
	e.stats.Items.Add(1)
	if e.stream != nil {
		select {
		case e.stream <- it:
		case <-ctx.Done():
		}
	}
	for _, p := range e.cfg.Pipelines {
		if err := p.Process(ctx, it); err != nil {
			e.log.Error("pipeline failed", "err", err)
		}
	}
}

func (e *Engine) download(ctx context.Context, req *Request, mode Mode) (*fetcher.Response, error) {
	fr := fetcher.Request{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body, Proxy: req.Proxy}
	if req.Session != "" {
		dl, ok := e.cfg.Sessions[req.Session]
		if !ok {
			return nil, fmt.Errorf("spider: unknown session %q", req.Session)
		}
		return dl.Do(ctx, fr)
	}
	if req.Browser || mode == ModeBrowser {
		if e.cfg.Browser == nil {
			return nil, errors.New("spider: browser requested but Config.Browser is nil")
		}
		return e.cfg.Browser.Do(ctx, fr)
	}
	resp, err := e.cfg.HTTP.Do(ctx, fr)
	if errors.Is(err, fetcher.ErrBlocked) && mode != ModeHTTP && e.cfg.EscalateOnBlock && e.cfg.Browser != nil && ctx.Err() == nil {
		e.stats.Escalated.Add(1)
		e.log.Info("blocked over HTTP, escalating to browser", "url", req.URL)
		return e.cfg.Browser.Do(ctx, fr)
	}
	return resp, err
}

// ---- per-domain politeness ----

type domainLimiter struct {
	sem      chan struct{}
	mu       sync.Mutex
	next     time.Time
	throttle *hostThrottle
}

func (e *Engine) limiter(host string) *domainLimiter {
	e.domMu.Lock()
	defer e.domMu.Unlock()
	l, ok := e.domains[host]
	if !ok {
		l = &domainLimiter{
			sem:      make(chan struct{}, e.cfg.PerDomainConcurrency),
			throttle: &hostThrottle{delay: e.cfg.AutoThrottle.StartDelay},
		}
		e.domains[host] = l
	}
	return l
}

func (l *domainLimiter) acquire(ctx context.Context, delay time.Duration, randomize bool) error {
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if delay <= 0 {
		return nil
	}
	if randomize {
		delay = time.Duration(float64(delay) * (0.5 + rand.Float64()))
	}
	// Reserve a start slot so concurrent workers space out.
	l.mu.Lock()
	start := time.Now()
	if l.next.After(start) {
		start = l.next
	}
	l.next = start.Add(delay)
	l.mu.Unlock()
	if wait := time.Until(start); wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			<-l.sem
			return ctx.Err()
		}
	}
	return nil
}

func (l *domainLimiter) release() { <-l.sem }

// parseRetryAfter reads seconds or an HTTP date; unparseable means 0.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
