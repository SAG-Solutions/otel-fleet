// Package leader provides single-active-runner election over PostgreSQL
// session-level advisory locks, so the otel-fleet opamp tier can run with
// multiple replicas for availability while still executing its singleton
// workers (the alerting evaluator and the retention sweep) on exactly one
// replica at a time.
//
// Each worker is guarded by its own advisory-lock key. The elected replica
// holds pg_try_advisory_lock on a dedicated pooled connection for the lifetime
// of its leadership; if it crashes or loses the connection, PostgreSQL releases
// the session-level lock automatically and another replica acquires it within
// retryInterval — no external coordinator (etcd/consul/k8s lease) needed.
//
// Workers guarded this way MUST be idempotent-safe to START and STOP: on
// leadership loss the elected context is cancelled and the worker must return
// promptly; on (re)election it is started fresh.
package leader

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Key derives a stable advisory-lock key from a worker name (FNV-64a), so
// callers use readable names ("alerting", "retention") instead of magic
// integers. Distinct names yield distinct keys with overwhelming probability.
func Key(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("otel-fleet:leader:" + name))
	return int64(h.Sum64())
}

// Elector runs a worker on exactly one replica at a time.
type Elector struct {
	pool *pgxpool.Pool
	key  int64
	name string
	log  *slog.Logger

	// retryInterval is how long to wait before re-contesting after losing (or
	// failing to acquire) leadership. pollInterval is how often the leader
	// checks that it still holds the lock connection.
	retryInterval time.Duration
	pollInterval  time.Duration
}

// New builds an Elector for the named worker. The name both labels logs and
// (via Key) selects the advisory-lock key.
func New(pool *pgxpool.Pool, name string, log *slog.Logger) *Elector {
	return &Elector{
		pool:          pool,
		key:           Key(name),
		name:          name,
		log:           log,
		retryInterval: 10 * time.Second,
		pollInterval:  5 * time.Second,
	}
}

// Run blocks until ctx is cancelled. Whenever this replica holds the lock it
// invokes work(electedCtx); electedCtx is cancelled the moment leadership ends
// (lock connection lost or ctx cancelled), so work must observe cancellation
// and return. Between campaigns it backs off by retryInterval.
func (e *Elector) Run(ctx context.Context, work func(context.Context)) error {
	for ctx.Err() == nil {
		if err := e.campaign(ctx, work); err != nil && !errors.Is(err, context.Canceled) {
			e.log.Warn("leader campaign ended with error", "worker", e.name, "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.retryInterval):
		}
	}
	return ctx.Err()
}

// campaign acquires a dedicated connection, tries the advisory lock once, and —
// if elected — runs work until leadership or ctx ends. Returns nil when it
// simply wasn't elected (caller backs off and retries).
func (e *Elector) campaign(ctx context.Context, work func(context.Context)) error {
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	var got bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", e.key).Scan(&got); err != nil {
		return err
	}
	if !got {
		return nil // another replica leads
	}
	e.log.Info("acquired leadership", "worker", e.name)
	defer func() {
		// Best-effort explicit unlock. If the connection died the lock is
		// already gone; use a fresh context so shutdown still releases it.
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", e.key); err != nil {
			e.log.Debug("advisory unlock failed (connection likely already closed)", "worker", e.name, "err", err)
		}
		e.log.Info("released leadership", "worker", e.name)
	}()

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		work(workCtx)
	}()

	ticker := time.NewTicker(e.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return ctx.Err()
		case <-done:
			// work returned on its own; give up leadership and let Run retry.
			return nil
		case <-ticker.C:
			// Verify we still own the session holding the lock. A failed ping
			// means the connection (and thus the advisory lock) is gone.
			if err := conn.Ping(ctx); err != nil {
				e.log.Warn("lost leadership connection, stepping down", "worker", e.name, "err", err)
				cancel()
				<-done
				return err
			}
		}
	}
}
