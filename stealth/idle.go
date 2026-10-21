package stealth

import (
	"context"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
)

// idleTracker counts in-flight requests of every type so a fetch can wait
// for the page to go network-quiet.
type idleTracker struct {
	mu       sync.Mutex
	inflight map[network.RequestID]struct{}
}

func newIdleTracker() *idleTracker {
	return &idleTracker{inflight: map[network.RequestID]struct{}{}}
}

func (t *idleTracker) started(id network.RequestID) {
	t.mu.Lock()
	t.inflight[id] = struct{}{}
	t.mu.Unlock()
}

func (t *idleTracker) done(id network.RequestID) {
	t.mu.Lock()
	delete(t.inflight, id)
	t.mu.Unlock()
}

func (t *idleTracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.inflight)
}

// wait blocks until nothing has been in flight for quiet, or max passes.
// Hitting max is not an error: long-polling pages never go idle.
func (t *idleTracker) wait(ctx context.Context, quiet, max time.Duration) error {
	deadline := time.Now().Add(max)
	var idleSince time.Time
	for time.Now().Before(deadline) {
		switch {
		case t.count() > 0:
			idleSince = time.Time{}
		case idleSince.IsZero():
			idleSince = time.Now()
		case time.Since(idleSince) >= quiet:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}
