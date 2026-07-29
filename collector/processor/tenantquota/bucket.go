// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantquota // import "github.com/sag-solutions/otel-fleet/collector/processor/tenantquota"

import (
	"sync"
	"time"
)

const (
	// idleTimeout is how long a tenant's bucket may go unused before the
	// lazy GC removes it. A GC'd tenant simply gets a fresh (full) bucket on
	// its next request.
	idleTimeout = 10 * time.Minute
	// gcInterval is how often (at most) the registry sweeps idle buckets.
	// The sweep runs lazily on the take() path; no background goroutine.
	gcInterval = 10 * time.Minute
)

// bucket is a token bucket: capacity = limit*burstSeconds tokens, refilled at
// limit tokens/sec. It starts full so a new tenant can immediately burst.
type bucket struct {
	tokens   float64
	capacity float64
	rate     float64 // tokens (items) per second
	lastSeen time.Time
}

// registry holds one token bucket per tenant, created lazily and GC'd after
// idleTimeout without traffic. A single mutex guards the map and the buckets;
// the critical section is a few float ops, so contention is negligible
// compared to pdata processing.
type registry struct {
	burstSeconds float64
	now          func() time.Time // swappable in tests

	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

func newRegistry(burstSeconds float64) *registry {
	return &registry{
		burstSeconds: burstSeconds,
		now:          time.Now,
		buckets:      make(map[string]*bucket),
	}
}

// take attempts to consume n tokens from the tenant's bucket, where the
// bucket refills at limit tokens/sec up to limit*burstSeconds. It is
// all-or-nothing: if fewer than n tokens are available NOTHING is consumed
// and allowed=false is returned together with a hint of how long the caller
// should wait before retrying. A batch larger than the bucket capacity can
// never fit and is always rejected (retryAfter then reflects the deficit at
// the current rate, so clients back off hard rather than deadlock).
//
// limit may change between calls (the tenantauth cache refreshes it); the
// bucket is resized in place, clamping stored tokens to the new capacity.
func (r *registry) take(tenant string, limit int64, n int) (allowed bool, retryAfter time.Duration) {
	now := r.now()
	rate := float64(limit)
	capacity := rate * r.burstSeconds

	r.mu.Lock()
	defer r.mu.Unlock()

	r.maybeGC(now)

	b, ok := r.buckets[tenant]
	if !ok {
		b = &bucket{tokens: capacity, capacity: capacity, rate: rate, lastSeen: now}
		r.buckets[tenant] = b
	}

	// Refill for the elapsed time, then apply any limit change.
	if dt := now.Sub(b.lastSeen).Seconds(); dt > 0 {
		b.tokens += dt * b.rate
	}
	b.rate = rate
	b.capacity = capacity
	if b.tokens > capacity {
		b.tokens = capacity
	}
	b.lastSeen = now

	need := float64(n)
	if need <= b.tokens {
		b.tokens -= need
		return true, 0
	}
	return false, retryHint(need-b.tokens, rate)
}

// retryHint converts a token deficit into a client-facing retry delay,
// clamped to [100ms, 30s].
func retryHint(deficit, rate float64) time.Duration {
	d := time.Duration(deficit / rate * float64(time.Second))
	if d < 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// maybeGC removes buckets idle for longer than idleTimeout. Called with r.mu
// held, at most every gcInterval.
func (r *registry) maybeGC(now time.Time) {
	if now.Sub(r.lastGC) < gcInterval {
		return
	}
	r.lastGC = now
	for tenant, b := range r.buckets {
		if now.Sub(b.lastSeen) >= idleTimeout {
			delete(r.buckets, tenant)
		}
	}
}

// size reports the number of live buckets (test helper).
func (r *registry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buckets)
}
