// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantquota

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock drives a registry deterministically.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestRegistry(burstSeconds float64) (*registry, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1700000000, 0)}
	r := newRegistry(burstSeconds)
	r.now = clk.now
	return r, clk
}

func TestBucketBurstThenRefill(t *testing.T) {
	// limit 10 items/sec, burst 2s => capacity 20, starts full.
	r, clk := newTestRegistry(2)

	ok, _ := r.take("a", 10, 20)
	assert.True(t, ok, "full burst must be admitted")

	ok, retry := r.take("a", 10, 1)
	assert.False(t, ok, "bucket is empty")
	assert.Equal(t, 100*time.Millisecond, retry, "deficit 1 at 10/s = 100ms")

	// 500ms at 10/s refills 5 tokens.
	clk.advance(500 * time.Millisecond)
	ok, _ = r.take("a", 10, 5)
	assert.True(t, ok)
	ok, _ = r.take("a", 10, 1)
	assert.False(t, ok, "refilled tokens were consumed exactly")

	// Refill never exceeds capacity.
	clk.advance(time.Hour)
	ok, _ = r.take("a", 10, 20)
	assert.True(t, ok)
	ok, _ = r.take("a", 10, 1)
	assert.False(t, ok, "capacity is capped at limit*burst_seconds")
}

func TestBucketRejectionConsumesNothing(t *testing.T) {
	r, _ := newTestRegistry(2) // limit 10 => capacity 20

	ok, _ := r.take("a", 10, 15)
	require.True(t, ok) // 5 tokens left

	ok, retry := r.take("a", 10, 10)
	require.False(t, ok, "10 > 5 remaining")
	assert.Equal(t, 500*time.Millisecond, retry, "deficit 5 at 10/s")

	ok, _ = r.take("a", 10, 5)
	assert.True(t, ok, "the rejected batch must not have consumed tokens")
}

func TestBucketBatchLargerThanBurst(t *testing.T) {
	r, clk := newTestRegistry(2) // limit 10 => capacity 20

	// A batch that can never fit is rejected...
	ok, retry := r.take("a", 10, 1000)
	assert.False(t, ok)
	assert.Equal(t, 30*time.Second, retry, "retry hint is clamped at 30s")

	// ...consumes nothing, and the tenant is not wedged afterwards.
	ok, _ = r.take("a", 10, 20)
	assert.True(t, ok, "normal batches keep flowing after an oversized one")

	// Even after arbitrary waiting the oversized batch never fits (no deadlock,
	// just deterministic rejection).
	clk.advance(time.Hour)
	ok, _ = r.take("a", 10, 1000)
	assert.False(t, ok)
}

func TestPerTenantIsolation(t *testing.T) {
	r, _ := newTestRegistry(2)

	ok, _ := r.take("a", 10, 20)
	require.True(t, ok)
	ok, _ = r.take("a", 10, 1)
	require.False(t, ok, "tenant a exhausted")

	for range 5 {
		ok, _ = r.take("b", 100, 40)
		assert.True(t, ok, "tenant b has its own bucket")
	}
	ok, _ = r.take("a", 10, 1)
	assert.False(t, ok, "tenant b's traffic must not refill tenant a")
}

func TestLimitChangeResizesBucket(t *testing.T) {
	r, clk := newTestRegistry(2)

	ok, _ := r.take("a", 10, 20) // capacity 20, drained
	require.True(t, ok)

	// Limit raised to 100 (capacity 200). The elapsed second refills at the
	// OLD 10/s rate (the change applies from now on).
	clk.advance(time.Second)
	ok, _ = r.take("a", 100, 10)
	assert.True(t, ok)
	ok, _ = r.take("a", 100, 1)
	assert.False(t, ok, "past refill must not be granted retroactively at the new rate")
	clk.advance(time.Second)
	ok, _ = r.take("a", 100, 100)
	assert.True(t, ok, "from the change on, refill runs at 100/s")

	// Limit lowered to 5 (capacity 10): stored tokens are clamped down.
	clk.advance(time.Hour)
	ok, _ = r.take("a", 5, 11)
	assert.False(t, ok, "tokens must be clamped to the new, smaller capacity")
	ok, _ = r.take("a", 5, 10)
	assert.True(t, ok)
}

func TestIdleBucketGC(t *testing.T) {
	r, clk := newTestRegistry(2)

	_, _ = r.take("idle", 10, 1)
	_, _ = r.take("busy", 10, 1)
	require.Equal(t, 2, r.size())

	// "busy" keeps taking; "idle" goes quiet past idleTimeout. The next take
	// after gcInterval sweeps it.
	clk.advance(idleTimeout / 2)
	_, _ = r.take("busy", 10, 1)
	clk.advance(idleTimeout/2 + gcInterval + time.Second)
	_, _ = r.take("busy", 10, 1)

	assert.Equal(t, 1, r.size(), "idle bucket must be GC'd")

	// A GC'd tenant transparently gets a fresh, full bucket.
	ok, _ := r.take("idle", 10, 20)
	assert.True(t, ok)
}
