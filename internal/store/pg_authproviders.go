package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

const authProviderCols = `id, type, name, display_name, client_id, client_secret_enc, issuer, saml_config, enabled, created_at, updated_at`

func scanAuthProvider(row pgx.Row) (AuthProvider, error) {
	var p AuthProvider
	err := row.Scan(&p.ID, &p.Type, &p.Name, &p.DisplayName, &p.ClientID, &p.ClientSecretEnc,
		&p.Issuer, &p.SAMLConfig, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// ListAuthProviders returns the database-managed SSO providers (optionally
// only enabled ones), in creation order.
func (s *PG) ListAuthProviders(ctx context.Context, enabledOnly bool) ([]AuthProvider, error) {
	q := `SELECT ` + authProviderCols + ` FROM auth_providers`
	if enabledOnly {
		q += ` WHERE enabled`
	}
	q += ` ORDER BY created_at, id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuthProvider{}
	for rows.Next() {
		p, err := scanAuthProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PG) GetAuthProvider(ctx context.Context, id uuid.UUID) (AuthProvider, error) {
	p, err := scanAuthProvider(s.pool.QueryRow(ctx, `
		SELECT `+authProviderCols+` FROM auth_providers WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthProvider{}, ErrNotFound
	}
	return p, err
}

func (s *PG) GetAuthProviderByName(ctx context.Context, name string) (AuthProvider, error) {
	p, err := scanAuthProvider(s.pool.QueryRow(ctx, `
		SELECT `+authProviderCols+` FROM auth_providers WHERE name = $1`, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthProvider{}, ErrNotFound
	}
	return p, err
}

// CreateAuthProvider inserts a provider plus audit entries in one
// transaction. Returns ErrNameExists when the slug is taken.
func (s *PG) CreateAuthProvider(ctx context.Context, p NewAuthProvider, entries []audit.Entry) (AuthProvider, error) {
	var out AuthProvider
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanAuthProvider(tx.QueryRow(ctx, `
			INSERT INTO auth_providers (id, type, name, display_name, client_id, client_secret_enc, issuer, saml_config, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING `+authProviderCols,
			p.ID, p.Type, p.Name, p.DisplayName, p.ClientID, p.ClientSecretEnc, p.Issuer, p.SAMLConfig, p.Enabled))
		if isUniqueViolation(err, "auth_providers_name_key") {
			return ErrNameExists
		}
		if err != nil {
			return fmt.Errorf("insert auth provider: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return AuthProvider{}, err
	}
	return out, nil
}

// UpdateAuthProvider patches a provider; a nil ClientSecretEnc keeps the
// stored secret.
func (s *PG) UpdateAuthProvider(ctx context.Context, id uuid.UUID, upd AuthProviderUpdate, entries []audit.Entry) (AuthProvider, error) {
	var out AuthProvider
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanAuthProvider(tx.QueryRow(ctx, `
			UPDATE auth_providers
			SET display_name      = COALESCE($2, display_name),
			    client_id         = COALESCE($3, client_id),
			    client_secret_enc = COALESCE($4, client_secret_enc),
			    issuer            = COALESCE($5, issuer),
			    saml_config       = COALESCE($6, saml_config),
			    enabled           = COALESCE($7, enabled),
			    updated_at        = now()
			WHERE id = $1
			RETURNING `+authProviderCols,
			id, upd.DisplayName, upd.ClientID, upd.ClientSecretEnc, upd.Issuer, upd.SAMLConfig, upd.Enabled))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("update auth provider: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return AuthProvider{}, err
	}
	return out, nil
}

// DeleteAuthProvider removes a provider. Existing sessions stay valid; the
// login option disappears immediately.
func (s *PG) DeleteAuthProvider(ctx context.Context, id uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM auth_providers WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete auth provider: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Write(ctx, tx, entries...)
	})
}
