// Package ratelimit counts attempts per key over a sliding window. It guards
// the endpoints an attacker would hammer — sign-in, password reset, email
// verification — keyed by client IP and by account.
//
// Counts live in memory: the API runs as a single process. A second API
// instance would need a shared store, or each instance would allow its own
// share of attempts.
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// ErrLimited is the rate_limited error; Limited adds how long to wait.
var ErrLimited = httpx.NewError(http.StatusTooManyRequests, "rate_limited")

// Limited returns ErrLimited telling the client to wait retryAfter.
func Limited(retryAfter time.Duration) error {
	return ErrLimited.WithRetryAfter(retryAfter)
}

// Limiter allows at most max attempts per key in any window-long stretch of
// time. It is safe for concurrent use.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu    sync.Mutex
	hits  map[string][]time.Time // oldest first, at most max
	swept time.Time
}

// New returns a Limiter that allows max attempts per key per window.
func New(maxAttempts int, window time.Duration) *Limiter {
	return &Limiter{
		max:    maxAttempts,
		window: window,
		now:    time.Now,
		hits:   make(map[string][]time.Time),
	}
}

// Check reports whether key may make another attempt and, when it may not,
// how long until it can. Checking doesn't count as an attempt; Hit does.
func (l *Limiter) Check(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	hits := l.recent(key, now)
	if len(hits) < l.max {
		return true, 0
	}
	// Blocked until the oldest attempt in the window falls out of it.
	return false, hits[0].Add(l.window).Sub(now)
}

// Hit records an attempt by key.
func (l *Limiter) Hit(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	hits := append(l.recent(key, now), now)
	if len(hits) > l.max {
		hits = hits[len(hits)-l.max:] // older ones can't matter anymore
	}
	l.hits[key] = hits
	l.sweep(now)
}

// Reset forgets key's attempts, e.g. an account's failed sign-ins once it
// signs in.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

// recent returns key's attempts still inside the window, dropping the rest.
func (l *Limiter) recent(key string, now time.Time) []time.Time {
	hits := l.hits[key]
	cutoff := now.Add(-l.window)
	i := 0
	for i < len(hits) && !hits[i].After(cutoff) {
		i++
	}
	if i == len(hits) {
		delete(l.hits, key)
		return nil
	}
	hits = hits[i:]
	l.hits[key] = hits
	return hits
}

// sweep drops keys with no attempts left in the window, at most once per
// window, so keys that are never seen again don't pile up.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.swept) < l.window {
		return
	}
	l.swept = now
	for key := range l.hits {
		l.recent(key, now)
	}
}
