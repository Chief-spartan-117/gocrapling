package stealth

import (
	"context"
	"regexp"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/sagarshrestha/gocrapling/fetcher"
)

const maxCaptureBody = 4 << 20

// capturer records background XHR/fetch responses of one tab.
type capturer struct {
	tab    context.Context
	filter *regexp.Regexp

	mu       sync.Mutex
	pending  map[network.RequestID]*fetcher.Capture
	inflight int
	done     []fetcher.Capture
	wg       sync.WaitGroup
}

func newCapturer(tab context.Context, filter *regexp.Regexp) *capturer {
	return &capturer{tab: tab, filter: filter, pending: map[network.RequestID]*fetcher.Capture{}}
}

func isAPI(t network.ResourceType) bool {
	return t == network.ResourceTypeXHR || t == network.ResourceTypeFetch
}

func (c *capturer) sent(e *network.EventRequestWillBeSent) {
	if !isAPI(e.Type) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, redirect := c.pending[e.RequestID]; redirect {
		c.pending[e.RequestID].URL = e.Request.URL // same id across redirects
		return
	}
	c.pending[e.RequestID] = &fetcher.Capture{URL: e.Request.URL, Method: e.Request.Method}
	c.inflight++
}

func (c *capturer) response(e *network.EventResponseReceived) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pending[e.RequestID]; ok {
		p.Status = int(e.Response.Status)
		p.MimeType = e.Response.MimeType
	}
}

func (c *capturer) finished(e *network.EventLoadingFinished) {
	c.mu.Lock()
	p, ok := c.pending[e.RequestID]
	if ok {
		delete(c.pending, e.RequestID)
		c.inflight--
	}
	c.mu.Unlock()
	if !ok || (c.filter != nil && !c.filter.MatchString(p.URL)) {
		return
	}
	c.wg.Add(1)
	go func() { // CDP calls must not run on the event goroutine
		defer c.wg.Done()
		var body []byte
		err := chromedp.Run(c.tab, chromedp.ActionFunc(func(ctx context.Context) error {
			b, err := network.GetResponseBody(e.RequestID).Do(ctx)
			body = b
			return err
		}))
		if err != nil {
			return // body evicted or request cancelled
		}
		if len(body) > maxCaptureBody {
			body = body[:maxCaptureBody]
		}
		p.Body = body
		c.mu.Lock()
		c.done = append(c.done, *p)
		c.mu.Unlock()
	}()
}

func (c *capturer) failed(e *network.EventLoadingFailed) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.pending[e.RequestID]; ok {
		delete(c.pending, e.RequestID)
		c.inflight--
	}
}

// settle waits until no API request has been in flight for quiet, or
// max elapses, then waits for body downloads.
func (c *capturer) settle(ctx context.Context, quiet, max time.Duration) error {
	deadline := time.Now().Add(max)
	idleSince := time.Time{}
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := c.inflight
		c.mu.Unlock()
		switch {
		case n > 0:
			idleSince = time.Time{}
		case idleSince.IsZero():
			idleSince = time.Now()
		case time.Since(idleSince) >= quiet:
			c.wg.Wait()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	c.wg.Wait()
	return nil
}

func (c *capturer) results() []fetcher.Capture {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]fetcher.Capture(nil), c.done...)
}
