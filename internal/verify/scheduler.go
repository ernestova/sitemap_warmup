package verify

import (
	"container/heap"
	"context"
	"time"

	"github.com/ernestova/sitemap_warmup/internal/cdn"
	"github.com/ernestova/sitemap_warmup/internal/probe"
)

// Config holds the scheduling limits. Zero values are not defaults; callers
// pass the documented defaults explicitly.
type Config struct {
	// Concurrency is the number of requests in flight at once.
	Concurrency int
	// RPS is the global request rate shared by first attempts and retries.
	// Zero or less means unlimited.
	RPS           float64
	RetryInterval time.Duration
	// MaxWait is how long, from the first attempt, a pair keeps being
	// retried while it is miss or stale. Zero means a single attempt.
	MaxWait time.Duration
	// RunTimeout caps the whole run; unfinished pairs become incomplete.
	RunTimeout time.Duration
	// MaxErrorRetries is how many times an error is retried.
	MaxErrorRetries int
}

// DefaultMaxErrorRetries is the documented retry limit for errors.
const DefaultMaxErrorRetries = 3

// Pair is one URL requested with one profile.
type Pair struct {
	URL     string
	Profile probe.Profile
}

// Outcome is the final result of one pair.
type Outcome struct {
	Pair
	InitialState cdn.State
	State        cdn.State
	Verdict      Verdict
	Attempts     int
	// First and Last are the first and the latest observation; nil when the
	// pair was never requested.
	First, Last *probe.Observation
	// TimeToHit runs from the start of the first attempt to the start of the
	// attempt that returned hit.
	TimeToHit time.Duration
	GotHit    bool
}

// ProbeFunc performs one request for a pair.
type ProbeFunc func(ctx context.Context, p Pair) probe.Observation

type retryItem struct {
	idx int
	at  time.Time
	seq int
}

type retryHeap []retryItem

func (h retryHeap) Len() int { return len(h) }
func (h retryHeap) Less(i, j int) bool {
	if h[i].at.Equal(h[j].at) {
		return h[i].seq < h[j].seq
	}
	return h[i].at.Before(h[j].at)
}
func (h retryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *retryHeap) Push(x any)   { *h = append(*h, x.(retryItem)) }
func (h *retryHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

type done struct {
	idx int
	obs probe.Observation
}

// Run requests every pair until it reaches a final state. A worker performs
// a single request and is released; pairs waiting for a retry sit in a timed
// heap that holds no worker. First attempts go before retries, and every
// request passes the same global rate limiter.
//
// stopped is nil when every pair finished, context.DeadlineExceeded when
// RunTimeout expired, or the parent context's error when it was canceled.
// Pairs left unfinished are incomplete.
func Run(ctx context.Context, cfg Config, pairs []Pair, do ProbeFunc) (out []Outcome, stopped error) {
	runCtx, cancel := context.WithTimeout(ctx, cfg.RunTimeout)
	defer cancel()

	workers := max(cfg.Concurrency, 1)
	var interval time.Duration
	if cfg.RPS > 0 {
		interval = time.Duration(float64(time.Second) / cfg.RPS)
	}

	out = make([]Outcome, len(pairs))
	errRetries := make([]int, len(pairs))
	for i, p := range pairs {
		out[i] = Outcome{Pair: p, State: cdn.Incomplete}
	}

	work := make(chan int)
	results := make(chan done, workers)
	for range workers {
		go func() {
			for idx := range work {
				results <- done{idx, do(runCtx, pairs[idx])}
			}
		}()
	}

	first := 0 // next pair never requested
	var retries retryHeap
	seq := 0
	var nextSlot time.Time // earliest time the limiter allows a request
	inflight, finished := 0, 0

	finish := func(idx int, v Verdict) {
		out[idx].State = v.State
		out[idx].Verdict = v
		finished++
	}

	handle := func(d done) {
		o := &out[d.idx]
		obs := d.obs
		v := Classify(obs)
		o.Attempts++
		if o.First == nil {
			o.First = &obs
			o.InitialState = v.State
		}
		o.Last = &obs
		o.Verdict = v
		if v.State == cdn.Hit {
			o.GotHit = true
			o.TimeToHit = obs.Start.Sub(o.First.Start)
		}
		if Terminal(v.State) {
			finish(d.idx, v)
			return
		}
		if v.State == cdn.Error {
			errRetries[d.idx]++
			if errRetries[d.idx] > cfg.MaxErrorRetries {
				finish(d.idx, v)
				return
			}
		}
		at := time.Now().Add(cfg.RetryInterval)
		if at.Sub(o.First.Start) > cfg.MaxWait {
			finish(d.idx, v)
			return
		}
		heap.Push(&retries, retryItem{idx: d.idx, at: at, seq: seq})
		seq++
	}

	// next returns the pair to request now, if any is eligible, and
	// otherwise when the next one becomes eligible.
	next := func(now time.Time) (idx int, ok bool, wake time.Time, waiting bool) {
		var due time.Time
		switch {
		case first < len(pairs):
			due = now
		case len(retries) > 0:
			due = retries[0].at
		default:
			return 0, false, time.Time{}, false
		}
		if nextSlot.After(due) {
			due = nextSlot
		}
		if due.After(now) {
			return 0, false, due, true
		}
		if first < len(pairs) {
			first++
			return first - 1, true, time.Time{}, false
		}
		return heap.Pop(&retries).(retryItem).idx, true, time.Time{}, false
	}

	timer := time.NewTimer(time.Hour)
	timer.Stop()

loop:
	for finished < len(pairs) {
		var wake time.Time
		waiting := false
		for inflight < workers {
			now := time.Now()
			idx, ok, w, wt := next(now)
			if !ok {
				wake, waiting = w, wt
				break
			}
			if interval > 0 {
				if nextSlot.Before(now) {
					nextSlot = now
				}
				nextSlot = nextSlot.Add(interval)
			}
			inflight++
			work <- idx
		}
		var timerC <-chan time.Time
		if waiting {
			timer.Reset(time.Until(wake))
			timerC = timer.C
		}
		select {
		case d := <-results:
			inflight--
			if runCtx.Err() == nil {
				handle(d)
			}
		case <-timerC:
		case <-runCtx.Done():
			break loop
		}
		timer.Stop()
	}

	close(work)
	// Requests in flight see the canceled context and return quickly; their
	// observations do not count.
	for ; inflight > 0; inflight-- {
		<-results
	}
	if finished == len(pairs) {
		return out, nil
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, runCtx.Err()
}
