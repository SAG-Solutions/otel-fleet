package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

const webhookCols = `id, type, name, url, events, secret_enc, enabled, created_by, created_at, updated_at`

func scanWebhook(row pgx.Row) (Webhook, error) {
	var w Webhook
	err := row.Scan(&w.ID, &w.Type, &w.Name, &w.URL, &w.Events, &w.SecretEnc, &w.Enabled,
		&w.CreatedBy, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

// ListWebhooks returns all webhooks in creation order.
func (s *PG) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+webhookCols+` FROM webhooks ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *PG) GetWebhook(ctx context.Context, id uuid.UUID) (Webhook, error) {
	w, err := scanWebhook(s.pool.QueryRow(ctx, `
		SELECT `+webhookCols+` FROM webhooks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Webhook{}, ErrNotFound
	}
	return w, err
}

// CreateWebhook inserts a webhook plus audit entries in one transaction.
func (s *PG) CreateWebhook(ctx context.Context, w NewWebhook, entries []audit.Entry) (Webhook, error) {
	var out Webhook
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanWebhook(tx.QueryRow(ctx, `
			INSERT INTO webhooks (id, type, name, url, events, secret_enc, enabled, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING `+webhookCols,
			w.ID, w.Type, w.Name, w.URL, w.Events, w.SecretEnc, w.Enabled, w.CreatedBy))
		if err != nil {
			return fmt.Errorf("insert webhook: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return Webhook{}, err
	}
	return out, nil
}

// UpdateWebhook patches a webhook. The secret column is only written when
// upd.SecretSet is true (nil SecretEnc removes signing).
func (s *PG) UpdateWebhook(ctx context.Context, id uuid.UUID, upd WebhookUpdate, entries []audit.Entry) (Webhook, error) {
	var out Webhook
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanWebhook(tx.QueryRow(ctx, `
			UPDATE webhooks
			SET type       = COALESCE($2, type),
			    name       = COALESCE($3, name),
			    url        = COALESCE($4, url),
			    events     = COALESCE($5, events),
			    secret_enc = CASE WHEN $6 THEN $7 ELSE secret_enc END,
			    enabled    = COALESCE($8, enabled),
			    updated_at = now()
			WHERE id = $1
			RETURNING `+webhookCols,
			id, upd.Type, upd.Name, upd.URL, upd.Events, upd.SecretSet, upd.SecretEnc, upd.Enabled))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("update webhook: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return Webhook{}, err
	}
	return out, nil
}

// DeleteWebhook removes a webhook.
func (s *PG) DeleteWebhook(ctx context.Context, id uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM webhooks WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete webhook: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Write(ctx, tx, entries...)
	})
}

// WriteAuditEntries appends standalone audit entries in their own
// transaction (system actions whose mutation happened outside PostgreSQL).
func (s *PG) WriteAuditEntries(ctx context.Context, entries []audit.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, entries...)
	})
}
