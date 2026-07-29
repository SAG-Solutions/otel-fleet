package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

// CreateAPIToken inserts a management-API token and its audit entry in one tx.
func (s *PG) CreateAPIToken(ctx context.Context, t NewAPIToken, entries []audit.Entry) (APIToken, error) {
	var out APIToken
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO api_tokens (id, name, token_prefix, token_hash, role, created_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			t.ID, t.Name, t.TokenPrefix, t.TokenHash, t.Role, t.CreatedBy, t.ExpiresAt)
		if err != nil {
			return fmt.Errorf("insert api token: %w", err)
		}
		if err := audit.Write(ctx, tx, entries...); err != nil {
			return err
		}
		out, err = scanAPIToken(tx.QueryRow(ctx, apiTokenSelect+` WHERE t.id = $1`, t.ID))
		return err
	})
	if err != nil {
		return APIToken{}, err
	}
	return out, nil
}

const apiTokenSelect = `
	SELECT t.id, t.name, t.token_prefix, t.role, t.created_by, u.email,
	       t.created_at, t.expires_at, t.revoked_at, t.last_used_at
	FROM api_tokens t
	LEFT JOIN users u ON u.id = t.created_by`

func scanAPIToken(row pgx.Row) (APIToken, error) {
	var t APIToken
	err := row.Scan(&t.ID, &t.Name, &t.TokenPrefix, &t.Role, &t.CreatedBy, &t.CreatedByEmail,
		&t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt)
	return t, err
}

// ListAPITokens returns all tokens (including revoked, for the audit trail),
// newest first. Secrets are never selected.
func (s *PG) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.pool.Query(ctx, apiTokenSelect+` ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken marks a token revoked (idempotent).
func (s *PG) RevokeAPIToken(ctx context.Context, id uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
		if err != nil {
			return fmt.Errorf("revoke api token: %w", err)
		}
		if tag.RowsAffected() == 0 {
			// Either unknown or already revoked; treat unknown as not found.
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT true FROM api_tokens WHERE id = $1`, id).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
		}
		return audit.Write(ctx, tx, entries...)
	})
}

// ActiveAPITokensByPrefix returns non-revoked tokens with the given prefix for
// constant-time hash comparison by the caller.
func (s *PG) ActiveAPITokensByPrefix(ctx context.Context, prefix string) ([]APITokenAuth, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, role, created_by, token_hash, expires_at
		FROM api_tokens
		WHERE token_prefix = $1 AND revoked_at IS NULL`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APITokenAuth{}
	for rows.Next() {
		var a APITokenAuth
		if err := rows.Scan(&a.ID, &a.Role, &a.CreatedBy, &a.TokenHash, &a.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
