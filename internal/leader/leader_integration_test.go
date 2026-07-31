//go:build integration

// Leader election tests need a real PostgreSQL (advisory locks are the whole
// point — a fake proves nothing). Gated on OTEL_FLEET_TEST_DATABASE_URL, same
// as the store integration suite, so plain `go test ./...` skips them.
//
// Run:  OTEL_FLEET_TEST_DATABASE_URL=postgres://otelfleet:otelfleet@localhost:5432/otel_fleet_test \
//         go test -tags=integration ./internal/leader/...
package leader

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("OTEL_FLEET_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OTEL_FLEET_TEST_DATABASE_URL unset")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// fastElector is an Elector with sub-second intervals so the test doesn't wait
// the production 10s/5s.
func fastElector(pool *pgxpool.Pool, name string, log *slog.Logger) *Elector {
	e := New(pool, name, log)
	e.retryInterval = 150 * time.Millisecond
	e.pollInterval = 150 * time.Millisecond
	return e
}

func TestOnlyOneLeaderRunsAndFailsOver(t *testing.T) {
	pool := testPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Unique name per run so parallel/re-runs don't collide on the lock key.
	name := "test-" + t.Name() + "-" + time.Now().Format("150405.000")

	var active int32     // how many workers are currently inside work()
	var maxActive int32  // high-water mark — must never exceed 1
	aRan := make(chan struct{}, 1)
	bRan := make(chan struct{}, 1)

	work := func(ran chan struct{}) func(context.Context) {
		return func(ctx context.Context) {
			n := atomic.AddInt32(&active, 1)
			for {
				if hw := atomic.LoadInt32(&maxActive); n > hw {
					atomic.CompareAndSwapInt32(&maxActive, hw, n)
				} else {
					break
				}
			}
			select {
			case ran <- struct{}{}:
			default:
			}
			<-ctx.Done()
			atomic.AddInt32(&active, -1)
		}
	}

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()

	go func() { _ = fastElector(pool, name, log).Run(ctxA, work(aRan)) }()
	// Let A win the lock first.
	select {
	case <-aRan:
	case <-time.After(5 * time.Second):
		t.Fatal("A never became leader")
	}
	go func() { _ = fastElector(pool, name, log).Run(ctxB, work(bRan)) }()

	// B must NOT run while A holds the lock.
	select {
	case <-bRan:
		t.Fatal("B ran work while A still held leadership (mutual exclusion broken)")
	case <-time.After(1 * time.Second):
	}

	// A steps down → B must take over.
	cancelA()
	select {
	case <-bRan:
	case <-time.After(5 * time.Second):
		t.Fatal("B did not take over after A stepped down (failover broken)")
	}

	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Fatalf("max concurrent leaders = %d, want 1", got)
	}
}

// TestDistinctNamesDoNotBlock proves two different workers (distinct keys) can
// both be led on the same replica simultaneously.
func TestDistinctNamesDoNotBlock(t *testing.T) {
	pool := testPool(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	stamp := time.Now().Format("150405.000")

	ran := make(chan string, 2)
	work := func(label string) func(context.Context) {
		return func(ctx context.Context) {
			ran <- label
			<-ctx.Done()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = fastElector(pool, "test-alpha-"+stamp, log).Run(ctx, work("alpha")) }()
	go func() { _ = fastElector(pool, "test-beta-"+stamp, log).Run(ctx, work("beta")) }()

	seen := map[string]bool{}
	for range 2 {
		select {
		case l := <-ran:
			seen[l] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("only %v ran; both distinct-key workers should lead", seen)
		}
	}
}
