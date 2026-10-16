package verify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/clarivo-co/sitewarm/internal/cdn"
	"github.com/clarivo-co/sitewarm/internal/probe"
)

// defaults mirrors the documented CLI defaults.
var defaults = Config{
	Concurrency:     4,
	RPS:             5,
	RetryInterval:   10 * time.Second,
	MaxWait:         5 * time.Minute,
	RunTimeout:      15 * time.Minute,
	MaxErrorRetries: DefaultMaxErrorRetries,
}

func pairs(n int) []Pair {
	ps := make([]Pair, n)
	for i := range ps {
		ps[i] = Pair{URL: fmt.Sprintf("https://ex.com/%d", i), Profile: probe.Profile{Name: "desktop"}}
	}
	return ps
}

func respond(cacheStatus string) probe.Observation {
	return probe.Observation{
		Start:  time.Now(),
		Status: 200,
		Header: http.Header{"Cf-Cache-Status": {cacheStatus}},
	}
}

// TestSchedulerNeverHitEndsInMiss: 100 pairs against a server that never
// returns HIT all end in miss before --run-timeout, not in incomplete.
// Retries wait in the timed heap without holding workers.
func TestSchedulerNeverHitEndsInMiss(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var mu sync.Mutex
		requests := 0
		out, stopped := Run(context.Background(), defaults, pairs(100), func(ctx context.Context, p Pair) probe.Observation {
			mu.Lock()
			requests++
			mu.Unlock()
			return respond("MISS")
		})
		elapsed := time.Since(start)
		if stopped != nil {
			t.Fatalf("stopped = %v after %v, want every pair finished", stopped, elapsed)
		}
		for _, o := range out {
			if o.State != cdn.Miss {
				t.Fatalf("%s ended %s, want miss", o.URL, o.State)
			}
			if o.Attempts < 2 {
				t.Fatalf("%s had %d attempts, want retries", o.URL, o.Attempts)
			}
		}
		if elapsed >= defaults.RunTimeout {
			t.Fatalf("took %v, want < %v", elapsed, defaults.RunTimeout)
		}
		// The global limiter holds: never more than RPS requests per second.
		if max := int(elapsed.Seconds()*defaults.RPS) + 1; requests > max {
			t.Fatalf("%d requests in %v exceeds %v rps", requests, elapsed, defaults.RPS)
		}
		t.Logf("100 pairs: %d requests, all miss after %v (virtual)", requests, elapsed)
	})
}

func TestSchedulerRetriesUntilHit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		calls := map[string]int{}
		out, stopped := Run(context.Background(), defaults, pairs(3), func(ctx context.Context, p Pair) probe.Observation {
			mu.Lock()
			calls[p.URL]++
			n := calls[p.URL]
			mu.Unlock()
			if n < 3 {
				return respond("MISS")
			}
			return respond("HIT")
		})
		if stopped != nil {
			t.Fatal(stopped)
		}
		for _, o := range out {
			if o.State != cdn.Hit || o.InitialState != cdn.Miss || o.Attempts != 3 || !o.GotHit {
				t.Fatalf("%+v", o)
			}
			// Two retry intervals, plus limiter spacing.
			if o.TimeToHit < 2*defaults.RetryInterval || o.TimeToHit > 2*defaults.RetryInterval+2*time.Second {
				t.Fatalf("time to hit = %v", o.TimeToHit)
			}
		}
	})
}

func TestSchedulerErrorRetriedThreeTimes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		out, _ := Run(context.Background(), defaults, pairs(1), func(ctx context.Context, p Pair) probe.Observation {
			return probe.Observation{Start: time.Now(), Err: errors.New("connection refused")}
		})
		if out[0].State != cdn.Error || out[0].Attempts != 1+DefaultMaxErrorRetries {
			t.Fatalf("state %s after %d attempts, want error after %d", out[0].State, out[0].Attempts, 1+DefaultMaxErrorRetries)
		}
	})
}

func TestSchedulerTerminalStatesStopImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, raw := range []string{"BYPASS", "NONE"} {
			out, _ := Run(context.Background(), defaults, pairs(1), func(ctx context.Context, p Pair) probe.Observation {
				return respond(raw)
			})
			if out[0].Attempts != 1 {
				t.Fatalf("%s: %d attempts, want 1", raw, out[0].Attempts)
			}
		}
	})
}

func TestSchedulerMaxWaitZeroIsSingleRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := defaults
		cfg.MaxWait = 0
		out, _ := Run(context.Background(), cfg, pairs(2), func(ctx context.Context, p Pair) probe.Observation {
			return respond("MISS")
		})
		for _, o := range out {
			if o.State != cdn.Miss || o.Attempts != 1 {
				t.Fatalf("%+v", o)
			}
		}
	})
}

// First attempts go before due retries.
func TestSchedulerFirstAttemptsHavePriority(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := defaults
		cfg.Concurrency = 1
		cfg.RPS = 1
		cfg.RetryInterval = time.Second
		var mu sync.Mutex
		var order []string
		Run(context.Background(), cfg, pairs(5), func(ctx context.Context, p Pair) probe.Observation {
			mu.Lock()
			order = append(order, p.URL)
			mu.Unlock()
			return respond("MISS")
		})
		for i := range 5 {
			if want := fmt.Sprintf("https://ex.com/%d", i); order[i] != want {
				t.Fatalf("request %d = %s, want %s (first attempts first): %v", i, order[i], want, order[:6])
			}
		}
	})
}

func TestSchedulerRunTimeoutMarksIncomplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := defaults
		cfg.RunTimeout = 30 * time.Second
		out, stopped := Run(context.Background(), cfg, pairs(300), func(ctx context.Context, p Pair) probe.Observation {
			return respond("MISS")
		})
		if !errors.Is(stopped, context.DeadlineExceeded) {
			t.Fatalf("stopped = %v, want deadline exceeded", stopped)
		}
		var requested, never int
		for _, o := range out {
			if o.State != cdn.Incomplete {
				t.Fatalf("%s = %s, want incomplete", o.URL, o.State)
			}
			if o.Attempts > 0 {
				requested++
			} else {
				never++
			}
		}
		if requested == 0 || never == 0 {
			t.Fatalf("want both retried and never-requested pairs, got %d/%d", requested, never)
		}
	})
}

func TestSchedulerCancelMarksIncomplete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			time.Sleep(3 * time.Second)
			cancel()
		}()
		out, stopped := Run(ctx, defaults, pairs(50), func(ctx context.Context, p Pair) probe.Observation {
			return respond("MISS")
		})
		if !errors.Is(stopped, context.Canceled) {
			t.Fatalf("stopped = %v, want canceled", stopped)
		}
		for _, o := range out {
			if o.State != cdn.Incomplete {
				t.Fatalf("%s = %s", o.URL, o.State)
			}
		}
	})
}

// A retry held back by first attempts past --max-wait is not sent: the pair
// ends with its last verdict and no request starts after the window.
func TestSchedulerMaxWaitBoundsQueuedRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := defaults
		cfg.Concurrency = 1
		cfg.RPS = 1
		cfg.RetryInterval = time.Second
		cfg.MaxWait = 3 * time.Second
		var mu sync.Mutex
		starts := map[string][]time.Time{}
		out, stopped := Run(context.Background(), cfg, pairs(20), func(ctx context.Context, p Pair) probe.Observation {
			mu.Lock()
			starts[p.URL] = append(starts[p.URL], time.Now())
			mu.Unlock()
			return respond("MISS")
		})
		if stopped != nil {
			t.Fatalf("stopped = %v", stopped)
		}
		for _, o := range out {
			if o.State != cdn.Miss {
				t.Fatalf("%s = %s, want miss", o.URL, o.State)
			}
			s := starts[o.URL]
			if last := s[len(s)-1].Sub(s[0]); last > cfg.MaxWait {
				t.Fatalf("%s retried %v after its first attempt, max-wait %v", o.URL, last, cfg.MaxWait)
			}
		}
		// 20 first attempts at 1 rps take 19 s, so pair 0's retry expired in
		// the queue.
		if n := out[0].Attempts; n != 1 {
			t.Fatalf("pair 0 had %d attempts, want 1", n)
		}
	})
}
