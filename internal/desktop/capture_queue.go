package desktop

import (
	"context"
	"sync"
)

const captureQueueLimit = 64

type captureWaiter struct {
	ready    chan struct{}
	priority bool
	granted  bool
}

// One native capture lane, with bounded FIFO queues. Interactive observations
// go before the next preview, while a burst bound keeps viewers progressing too.
type captureQueue struct {
	mu      sync.Mutex
	active  bool
	waiting []*captureWaiter
	burst   int
}

func (q *captureQueue) acquire(ctx context.Context, priority bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := &captureWaiter{ready: make(chan struct{}), priority: priority}
	q.mu.Lock()
	if !q.active {
		q.active, w.granted = true, true
		if priority {
			q.burst = 1
		} else {
			q.burst = 0
		}
		close(w.ready)
	} else {
		if len(q.waiting) >= captureQueueLimit {
			q.mu.Unlock()
			return nil, failure("busy")
		}
		q.waiting = append(q.waiting, w)
	}
	q.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { q.release(w) }) }
	select {
	case <-w.ready:
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		return release, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

func (q *captureQueue) release(w *captureWaiter) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !w.granted {
		for i, queued := range q.waiting {
			if queued == w {
				copy(q.waiting[i:], q.waiting[i+1:])
				q.waiting[len(q.waiting)-1] = nil
				q.waiting = q.waiting[:len(q.waiting)-1]
				break
			}
		}
		return
	}
	if len(q.waiting) == 0 {
		q.active, q.burst = false, 0
		return
	}
	high, low := -1, -1
	for i, queued := range q.waiting {
		if queued.priority && high < 0 {
			high = i
		}
		if !queued.priority && low < 0 {
			low = i
		}
	}
	next := low
	if high >= 0 && (q.burst < 3 || low < 0) {
		next = high
		q.burst++
	} else {
		q.burst = 0
	}
	queued := q.waiting[next]
	copy(q.waiting[next:], q.waiting[next+1:])
	q.waiting[len(q.waiting)-1] = nil
	q.waiting = q.waiting[:len(q.waiting)-1]
	queued.granted = true
	close(queued.ready)
}
