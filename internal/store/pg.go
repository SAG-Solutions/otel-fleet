package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

// PG implements Store on top of a pgx connection pool.
type PG struct {
	pool *pgxpool.Pool
}

// NewPG wraps the given pool.
func NewPG(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

var _ Store = (*PG)(nil)

// Ping reports database reachability (used by /readyz).
func (s *PG) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

const customerCols = `id, slug, name, client_id, status, rate_limit_items_per_sec, retention_days, created_at, updated_at`

func scanCustomer(row pgx.Row) (Customer, error) {
	var c Customer
	err := row.Scan(&c.ID, &c.Slug, &c.Name, &c.ClientID, &c.Status,
		&c.RateLimitItemsPerSec, &c.RetentionDays, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

const apiKeyCols = `id, customer_id, name, key_prefix, key_hash, created_by, created_at, expires_at, revoked_at, last_used_at`

func scanAPIKey(row pgx.Row) (APIKey, error) {
	var k APIKey
	err := row.Scan(&k.ID, &k.CustomerID, &k.Name, &k.KeyPrefix, &k.KeyHash, &k.CreatedBy,
		&k.CreatedAt, &k.ExpiresAt, &k.RevokedAt, &k.LastUsedAt)
	return k, err
}

func (s *PG) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}

func isForeignKeyViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == constraint
}

// CreateCustomer inserts a customer plus its initial API key and the audit
// entries in one transaction. Returns ErrSlugExists / ErrConflict on unique
// violations.
func (s *PG) CreateCustomer(ctx context.Context, c NewCustomer, k NewAPIKey, entries []audit.Entry) (Customer, APIKey, error) {
	var cust Customer
	var key APIKey
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		cust, err = scanCustomer(tx.QueryRow(ctx, `
			INSERT INTO customers (id, slug, name, client_id)
			VALUES ($1, $2, $3, $4)
			RETURNING `+customerCols,
			c.ID, c.Slug, c.Name, c.ClientID))
		switch {
		case isUniqueViolation(err, "customers_slug_key"):
			return ErrSlugExists
		case isUniqueViolation(err, "customers_client_id_key"):
			return fmt.Errorf("client_id collision: %w", ErrConflict)
		case err != nil:
			return fmt.Errorf("insert customer: %w", err)
		}

		key, err = insertAPIKey(ctx, tx, k)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return Customer{}, APIKey{}, err
	}
	return cust, key, nil
}

func (s *PG) GetCustomer(ctx context.Context, id uuid.UUID) (Customer, error) {
	c, err := scanCustomer(s.pool.QueryRow(ctx, `
		SELECT `+customerCols+` FROM customers WHERE id = $1 AND status <> 'deleted'`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	return c, err
}

func (s *PG) ListCustomers(ctx context.Context, status *string) ([]Customer, error) {
	q := `SELECT ` + customerCols + ` FROM customers WHERE status <> 'deleted'`
	args := []any{}
	if status != nil {
		q += ` AND status = $1`
		args = append(args, *status)
	}
	q += ` ORDER BY created_at, id`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Customer{}
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PG) UpdateCustomer(ctx context.Context, id uuid.UUID, upd CustomerUpdate, entries []audit.Entry) (Customer, error) {
	var cust Customer
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		cust, err = scanCustomer(tx.QueryRow(ctx, `
			UPDATE customers
			SET name   = COALESCE($2, name),
			    status = COALESCE($3, status),
			    rate_limit_items_per_sec = CASE WHEN $4 THEN $5 ELSE rate_limit_items_per_sec END,
			    retention_days           = CASE WHEN $6 THEN $7 ELSE retention_days END,
			    updated_at = now()
			WHERE id = $1 AND status <> 'deleted'
			RETURNING `+customerCols,
			id, upd.Name, upd.Status,
			upd.RateLimitItemsPerSec.Set, upd.RateLimitItemsPerSec.Value,
			upd.RetentionDays.Set, upd.RetentionDays.Value))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("update customer: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return Customer{}, err
	}
	return cust, nil
}

// SoftDeleteCustomer marks the customer deleted and revokes all of its
// API keys in the same transaction.
func (s *PG) SoftDeleteCustomer(ctx context.Context, id uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE customers SET status = 'deleted', updated_at = now()
			WHERE id = $1 AND status <> 'deleted'`, id)
		if err != nil {
			return fmt.Errorf("delete customer: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `
			UPDATE api_keys SET revoked_at = now()
			WHERE customer_id = $1 AND revoked_at IS NULL`, id); err != nil {
			return fmt.Errorf("revoke keys: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
}

func (s *PG) CountActiveCustomers(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE status = 'active'`).Scan(&n)
	return n, err
}

func (s *PG) ListCustomerRefs(ctx context.Context) ([]CustomerRef, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, client_id FROM customers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomerRef
	for rows.Next() {
		var r CustomerRef
		if err := rows.Scan(&r.ID, &r.Name, &r.ClientID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func insertAPIKey(ctx context.Context, tx pgx.Tx, k NewAPIKey) (APIKey, error) {
	key, err := scanAPIKey(tx.QueryRow(ctx, `
		INSERT INTO api_keys (id, customer_id, name, key_prefix, key_hash, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+apiKeyCols,
		k.ID, k.CustomerID, k.Name, k.KeyPrefix, k.KeyHash, k.CreatedBy, k.ExpiresAt))
	if err != nil {
		return APIKey{}, fmt.Errorf("insert api key: %w", err)
	}
	return key, nil
}

func (s *PG) ListAPIKeys(ctx context.Context, customerID uuid.UUID) ([]APIKey, error) {
	if _, err := s.GetCustomer(ctx, customerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+apiKeyCols+` FROM api_keys WHERE customer_id = $1 ORDER BY created_at, id`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *PG) CreateAPIKey(ctx context.Context, k NewAPIKey, entries []audit.Entry) (APIKey, error) {
	if _, err := s.GetCustomer(ctx, k.CustomerID); err != nil {
		return APIKey{}, err
	}
	var key APIKey
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if key, err = insertAPIKey(ctx, tx, k); err != nil {
			return err
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return APIKey{}, err
	}
	return key, nil
}

// RevokeAPIKey is idempotent: revoking an already-revoked key succeeds.
func (s *PG) RevokeAPIKey(ctx context.Context, customerID, keyID uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var exists bool
		err := tx.QueryRow(ctx, `
			SELECT true FROM api_keys WHERE id = $1 AND customer_id = $2`, keyID, customerID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE api_keys SET revoked_at = now()
			WHERE id = $1 AND customer_id = $2 AND revoked_at IS NULL`, keyID, customerID); err != nil {
			return fmt.Errorf("revoke key: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
}

// ActiveKeysByPrefix returns all non-revoked keys with the given prefix joined
// with their customer. Expiry and customer status are checked by the caller.
func (s *PG) ActiveKeysByPrefix(ctx context.Context, prefix string) ([]AuthKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT k.id, k.customer_id, c.client_id, k.key_hash, c.status, k.expires_at,
		       COALESCE(c.rate_limit_items_per_sec, 0)
		FROM api_keys k
		JOIN customers c ON c.id = k.customer_id
		WHERE k.key_prefix = $1 AND k.revoked_at IS NULL`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuthKey
	for rows.Next() {
		var k AuthKey
		if err := rows.Scan(&k.KeyID, &k.CustomerID, &k.ClientID, &k.KeyHash, &k.CustomerStatus, &k.ExpiresAt, &k.RateLimitItemsPerSec); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// TouchAPIKeys batch-updates last_used_at, keeping the latest timestamp.
func (s *PG) TouchAPIKeys(ctx context.Context, usages map[uuid.UUID]time.Time) error {
	if len(usages) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for id, ts := range usages {
		batch.Queue(`
			UPDATE api_keys
			SET last_used_at = GREATEST(COALESCE(last_used_at, 'epoch'::timestamptz), $2)
			WHERE id = $1`, id, ts)
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

const userCols = `id, email, display_name, role, external_id, disabled_at, last_login_at, created_at`

func scanUserRow(row pgx.Row, u *User) error {
	return row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.ExternalID, &u.DisabledAt, &u.LastLoginAt, &u.CreatedAt)
}

func (s *PG) GetUser(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := scanUserRow(s.pool.QueryRow(ctx, `
		SELECT `+userCols+` FROM users WHERE id = $1`, id), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// UpsertUserByIdentity resolves (provider, subject) to a user, creating the
// identity and — if the email is unknown — the user (with roleIfNew) on first
// login. Existing users keep their assigned role (an invited user's role
// survives their first login), with one exception: roleIfNew == "admin" means
// the email is in OTEL_FLEET_ADMIN_EMAILS, which always forces admin.
// Every call stamps last_login_at.
func (s *PG) UpsertUserByIdentity(ctx context.Context, provider, subject, email string, displayName *string, roleIfNew string) (User, error) {
	var u User
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		err := scanUserRow(tx.QueryRow(ctx, `
			SELECT u.id, u.email, u.display_name, u.role, u.external_id, u.disabled_at, u.last_login_at, u.created_at
			FROM users u JOIN user_identities i ON i.user_id = u.id
			WHERE i.provider = $1 AND i.subject = $2`, provider, subject), &u)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// No identity yet: attach to an existing user with this email
			// (invite flow), or create a new user.
			err = scanUserRow(tx.QueryRow(ctx, `
				SELECT `+userCols+` FROM users WHERE email = $1`, email), &u)
			if errors.Is(err, pgx.ErrNoRows) {
				if err := scanUserRow(tx.QueryRow(ctx, `
					INSERT INTO users (email, display_name, role)
					VALUES ($1, $2, $3)
					RETURNING `+userCols,
					email, displayName, roleIfNew), &u); err != nil {
					return fmt.Errorf("insert user: %w", err)
				}
			} else if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO user_identities (user_id, provider, subject) VALUES ($1, $2, $3)`,
				u.ID, provider, subject); err != nil {
				return fmt.Errorf("insert identity: %w", err)
			}
		}

		role := u.Role
		if roleIfNew == "admin" { // OTEL_FLEET_ADMIN_EMAILS forces admin
			role = "admin"
		}
		if err := scanUserRow(tx.QueryRow(ctx, `
			UPDATE users SET role = $2, last_login_at = now()
			WHERE id = $1
			RETURNING `+userCols, u.ID, role), &u); err != nil {
			return fmt.Errorf("stamp login: %w", err)
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}
