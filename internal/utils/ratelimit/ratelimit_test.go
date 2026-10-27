package ratelimit

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// newLimiter returns a Limiter whose clock reads *clock.
func newLimiter(maxAttempts int, window time.Duration, clock *time.Time) *Limiter {
	l := New(maxAttempts, window)
	l.now = func() time.Time { return *clock }
	return l
}

func TestLimiterBlocksAfterMaxAttempts(t *testing.T) {
	clock := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	l := newLimiter(3, 15*time.Minute, &clock)

	for i := range 3 {
		if ok, _ := l.Check("login:user:kasir1"); !ok {
			t.Fatalf("attempt %d blocked, want it allowed", i+1)
		}
		l.Hit("login:user:kasir1")
		clock = clock.Add(time.Minute)
	}

	ok, wait := l.Check("login:user:kasir1")
	if ok {
		t.Fatal("4th attempt allowed, want it blocked")
	}
	// The first attempt was 3 minutes ago; it leaves the window in 12.
	if wait != 12*time.Minute {
		t.Errorf("retryAfter = %v, want 12m", wait)
	}
	if ok, _ := l.Check("login:user:kasir2"); !ok {
		t.Error("another key is blocked too, want keys independent")
	}
}

func TestLimiterSlidesWithTime(t *testing.T) {
	clock := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	l := newLimiter(2, 10*time.Minute, &clock)
	l.Hit("k") // 08:00
	clock = clock.Add(5 * time.Minute)
	l.Hit("k") // 08:05

	clock = clock.Add(5*time.Minute - time.Second) // 08:09:59
	if ok, _ := l.Check("k"); ok {
		t.Error("allowed at 08:09:59, want blocked until 08:10")
	}
	clock = clock.Add(time.Second) // 08:10: the 08:00 attempt has left
	if ok, _ := l.Check("k"); !ok {
		t.Error("blocked at 08:10, want allowed: only one attempt is still in the window")
	}
	l.Hit("k") // 08:10
	if ok, wait := l.Check("k"); ok || wait != 5*time.Minute {
		t.Errorf("Check = %v, %v; want blocked for 5m, until the 08:05 attempt leaves", ok, wait)
	}
}

func TestLimiterReset(t *testing.T) {
	clock := time.Now()
	l := newLimiter(1, time.Hour, &clock)
	l.Hit("k")
	l.Reset("k")
	if ok, _ := l.Check("k"); !ok {
		t.Error("blocked after Reset")
	}
}

func TestLimiterForgetsIdleKeys(t *testing.T) {
	clock := time.Now()
	l := newLimiter(5, time.Minute, &clock)
	for i := range 100 {
		l.Hit("ip:" + strconv.Itoa(i))
	}
	clock = clock.Add(2 * time.Minute)
	l.Hit("ip:new") // sweeps

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) != 1 {
		t.Errorf("%d keys kept, want only the one hit inside the window", len(l.hits))
	}
}

func TestLimiterKeepsAtMostMaxHits(t *testing.T) {
	clock := time.Now()
	l := newLimiter(3, time.Hour, &clock)
	for range 50 {
		l.Hit("k")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.hits["k"]); n != 3 {
		t.Errorf("stored %d hits, want 3", n)
	}
}

func TestLimiterIsSafeForConcurrentUse(t *testing.T) {
	// Four goroutines hit k0 200 times each: exactly the limit, so k0 is
	// blocked only if no hit was lost.
	l := New(800, time.Minute)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for range 200 {
				key := "k" + strconv.Itoa(g%2)
				l.Check(key)
				l.Hit(key)
			}
		})
	}
	wg.Wait()
	if ok, _ := l.Check("k0"); ok {
		t.Error("k0 isn't blocked after 800 hits against a limit of 800: hits were lost")
	}
}

func TestLimitedError(t *testing.T) {
	err := Limited(90 * time.Second)
	coded, ok := errors.AsType[*apperror.Error](err)
	if !ok || coded.Status() != http.StatusTooManyRequests || coded.Code() != "rate_limited" {
		t.Fatalf("Limited = %v, want a 429 rate_limited error", err)
	}
	if coded.RetryAfter() != 90*time.Second || coded.Details()["retry_after"] != 90 {
		t.Errorf("retry after = %v / %v, want 90s", coded.RetryAfter(), coded.Details())
	}
}
