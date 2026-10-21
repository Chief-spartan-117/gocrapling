package spider

import (
	"sync"
	"time"
)

// AutoThrottle adapts each host's delay to its observed latency and to
// blocking signals, like Scrapy's extension of the same name.
type AutoThrottle struct {
	Enabled bool
	// StartDelay is the initial per-host delay. Default 1s.
	StartDelay time.Duration
	// MinDelay and MaxDelay clamp the adaptive delay. Defaults 100ms / 60s.
	MinDelay time.Duration
	MaxDelay time.Duration
	// TargetConcurrency is the average number of parallel requests to aim
	// for per host. Default 1.0 (polite).
	TargetConcurrency float64
}

func (a AutoThrottle) withDefaults() AutoThrottle {
	if a.StartDelay <= 0 {
		a.StartDelay = time.Second
	}
	if a.MinDelay <= 0 {
		a.MinDelay = 100 * time.Millisecond
	}
	if a.MaxDelay <= 0 {
		a.MaxDelay = time.Minute
	}
	if a.TargetConcurrency <= 0 {
		a.TargetConcurrency = 1
	}
	return a
}

type hostThrottle struct {
	mu    sync.Mutex
	delay time.Duration
}

// observe updates the delay after one response. latency is the download
// time; retryAfter is the Retry-After header (0 if absent); blocked marks 403/429/503 or an anti-bot page.
func (h *hostThrottle) observe(cfg AutoThrottle, latency time.Duration, status int, blocked bool, retryAfter time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	defer func() { // a server-stated wait is a floor
		if retryAfter > h.delay {
			h.delay = clamp(retryAfter, cfg.MinDelay, cfg.MaxDelay)
		}
	}()
	if blocked || status == 429 || status == 503 {
		h.delay = clamp(h.delay*2, cfg.MinDelay, cfg.MaxDelay)
		return
	}
	target := time.Duration(float64(latency) / cfg.TargetConcurrency)
	next := (h.delay + target) / 2
	// Errors and redirects must not speed us up.
	if (status < 200 || status >= 300) && next < h.delay {
		next = h.delay
	}
	h.delay = clamp(next, cfg.MinDelay, cfg.MaxDelay)
}

func (h *hostThrottle) current() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.delay
}

func clamp(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}
