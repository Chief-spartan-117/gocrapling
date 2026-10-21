package spider

import (
	"container/heap"
	"context"
	"sync"
)

type queued struct {
	req *Request
	seq uint64
}

type pq []queued

func (p pq) Len() int { return len(p) }
func (p pq) Less(i, j int) bool {
	if p[i].req.Priority != p[j].req.Priority {
		return p[i].req.Priority > p[j].req.Priority
	}
	return p[i].seq < p[j].seq // FIFO among equals
}
func (p pq) Swap(i, j int) { p[i], p[j] = p[j], p[i] }
func (p *pq) Push(x any)   { *p = append(*p, x.(queued)) }
func (p *pq) Pop() any {
	old := *p
	n := len(old)
	x := old[n-1]
	*p = old[:n-1]
	return x
}

// scheduler is a de-duplicating priority queue that also knows when the
// crawl is finished (queue empty and nothing in flight).
type scheduler struct {
	mu     sync.Mutex
	cond   *sync.Cond
	heap   pq
	seen   map[string]struct{}
	active map[*Request]struct{}
	seq    uint64
	closed bool
}

func newScheduler() *scheduler {
	s := &scheduler{seen: map[string]struct{}{}, active: map[*Request]struct{}{}}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Push enqueues r unless it is a duplicate. It reports whether it queued.
func (s *scheduler) Push(r *Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if !r.DontFilter {
		k := r.Key()
		if _, dup := s.seen[k]; dup {
			return false
		}
		s.seen[k] = struct{}{}
	}
	s.seq++
	heap.Push(&s.heap, queued{r, s.seq})
	s.cond.Signal()
	return true
}

// Pop blocks for the next request. ok=false means the crawl is over: either
// ctx ended or the queue drained with nothing in flight.
func (s *scheduler) Pop(ctx context.Context) (*Request, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if ctx.Err() != nil || s.closed {
			return nil, false
		}
		if s.heap.Len() > 0 {
			r := heap.Pop(&s.heap).(queued).req
			s.active[r] = struct{}{}
			return r, true
		}
		if len(s.active) == 0 {
			s.closed = true
			s.cond.Broadcast()
			return nil, false
		}
		s.cond.Wait()
	}
}

// Done marks r finished.
func (s *scheduler) Done(r *Request) {
	s.mu.Lock()
	delete(s.active, r)
	s.cond.Broadcast()
	s.mu.Unlock()
}

// Wake unblocks waiters, used when the context is cancelled.
func (s *scheduler) Wake() {
	s.mu.Lock()
	s.cond.Broadcast()
	s.mu.Unlock()
}

// Pending returns queued plus in-flight request counts.
func (s *scheduler) Pending() (queued, active int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heap.Len(), len(s.active)
}

// Snapshot returns everything that must be redone after a restart: queued
// requests and those in flight, plus the seen set.
func (s *scheduler) Snapshot() (reqs []*Request, seen []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, q := range s.heap {
		reqs = append(reqs, q.req)
	}
	for r := range s.active {
		reqs = append(reqs, r)
	}
	seen = make([]string, 0, len(s.seen))
	for k := range s.seen {
		seen = append(seen, k)
	}
	return reqs, seen
}

// Restore loads a snapshot. In-flight requests come back as queued.
func (s *scheduler) Restore(reqs []*Request, seen []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range seen {
		s.seen[k] = struct{}{}
	}
	for _, r := range reqs {
		s.seq++
		heap.Push(&s.heap, queued{r, s.seq})
	}
}
