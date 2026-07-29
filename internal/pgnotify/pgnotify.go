// Package pgnotify is a thin PostgreSQL LISTEN/NOTIFY helper used to signal
// the OpAMP tier that a customer's edge config changed. It lets the stateless
// API tier and the singleton OpAMP tier run as separate processes: the API
// tier publishes, the OpAMP tier listens and pushes to connected agents.
package pgnotify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EdgeConfigChannel is the NOTIFY channel carrying a customer UUID whose edge
// config was (de)activated.
const EdgeConfigChannel = "otel_fleet_edge_config"

// Notifier publishes NOTIFY messages.
type Notifier struct{ pool *pgxpool.Pool }

// NewNotifier wraps a pool for publishing.
func NewNotifier(pool *pgxpool.Pool) *Notifier { return &Notifier{pool: pool} }

// Notify publishes payload on channel. The channel is a trusted constant, so
// pg_notify's parameterized form keeps the payload safe.
func (n *Notifier) Notify(ctx context.Context, channel, payload string) error {
	_, err := n.pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	return err
}

// Listen holds a dedicated connection, LISTENs on channel, and calls handle
// for every notification until ctx is cancelled. It reconnects with backoff
// when the connection drops, so a transient DB blip does not stop delivery.
func Listen(ctx context.Context, pool *pgxpool.Pool, channel string, log *slog.Logger, handle func(payload string)) {
	backoff := time.Second
	for ctx.Err() == nil {
		if err := listenOnce(ctx, pool, channel, handle); err != nil && ctx.Err() == nil {
			log.Warn("pgnotify: listener dropped, reconnecting", "channel", channel, "err", err, "retry_in", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, channel string, handle func(payload string)) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()

	// channel is a package constant, never user input — safe to interpolate.
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	for {
		notif, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		handle(notif.Payload)
	}
}
