package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

const billingCols = `price_per_gib_micro, price_per_million_items_micro, currency, updated_at, updated_by`

func scanBillingSettings(row pgx.Row) (BillingSettings, error) {
	var b BillingSettings
	err := row.Scan(&b.PricePerGiBMicro, &b.PricePerMillionItemsMicro, &b.Currency, &b.UpdatedAt, &b.UpdatedBy)
	return b, err
}

// GetBillingSettings returns the singleton price list (seeded to zeros by the
// migration, so it always exists).
func (s *PG) GetBillingSettings(ctx context.Context) (BillingSettings, error) {
	b, err := scanBillingSettings(s.pool.QueryRow(ctx, `SELECT `+billingCols+` FROM billing_settings WHERE id`))
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingSettings{}, ErrNotFound
	}
	return b, err
}

// UpdateBillingSettings patches the singleton price list and audits the change
// in the same transaction. nil fields keep their stored value.
func (s *PG) UpdateBillingSettings(ctx context.Context, upd BillingSettingsUpdate, actor *uuid.UUID, entries []audit.Entry) (BillingSettings, error) {
	var out BillingSettings
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanBillingSettings(tx.QueryRow(ctx, `
			UPDATE billing_settings
			SET price_per_gib_micro           = COALESCE($1, price_per_gib_micro),
			    price_per_million_items_micro = COALESCE($2, price_per_million_items_micro),
			    currency                      = COALESCE($3, currency),
			    updated_at                    = now(),
			    updated_by                    = $4
			WHERE id
			RETURNING `+billingCols,
			upd.PricePerGiBMicro, upd.PricePerMillionItemsMicro, upd.Currency, actor))
		if err != nil {
			return fmt.Errorf("update billing settings: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return BillingSettings{}, err
	}
	return out, nil
}

const billingOverrideCols = `customer_id, price_per_gib_micro, price_per_million_items_micro, updated_at, updated_by`

func scanBillingOverride(row pgx.Row) (BillingOverride, error) {
	var o BillingOverride
	err := row.Scan(&o.CustomerID, &o.PricePerGiBMicro, &o.PricePerMillionItemsMicro, &o.UpdatedAt, &o.UpdatedBy)
	return o, err
}

// ListBillingOverrides returns every per-customer price override. The statement
// builder resolves each customer's effective price from these; customers with
// no row bill at the global rate.
func (s *PG) ListBillingOverrides(ctx context.Context) ([]BillingOverride, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+billingOverrideCols+` FROM billing_price_overrides ORDER BY customer_id`)
	if err != nil {
		return nil, fmt.Errorf("list billing overrides: %w", err)
	}
	defer rows.Close()
	var out []BillingOverride
	for rows.Next() {
		o, err := scanBillingOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// SetBillingOverride upserts a customer's price override (nil price = inherit
// the global rate for that dimension) and audits the change in the same
// transaction. Returns ErrNotFound if the customer does not exist.
func (s *PG) SetBillingOverride(ctx context.Context, customerID uuid.UUID, gibMicro, itemsMicro *int64, actor *uuid.UUID, entries []audit.Entry) (BillingOverride, error) {
	var out BillingOverride
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanBillingOverride(tx.QueryRow(ctx, `
			INSERT INTO billing_price_overrides (customer_id, price_per_gib_micro, price_per_million_items_micro, updated_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (customer_id) DO UPDATE
			SET price_per_gib_micro           = EXCLUDED.price_per_gib_micro,
			    price_per_million_items_micro = EXCLUDED.price_per_million_items_micro,
			    updated_at                    = now(),
			    updated_by                    = EXCLUDED.updated_by
			RETURNING `+billingOverrideCols,
			customerID, gibMicro, itemsMicro, actor))
		if err != nil {
			if isForeignKeyViolation(err, "billing_price_overrides_customer_id_fkey") {
				return ErrNotFound
			}
			return fmt.Errorf("set billing override: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return BillingOverride{}, err
	}
	return out, nil
}

// DeleteBillingOverride removes a customer's override (reverting to global
// pricing) and audits it. No-op-safe: deleting a missing override returns
// ErrNotFound so the handler can 404.
func (s *PG) DeleteBillingOverride(ctx context.Context, customerID uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM billing_price_overrides WHERE customer_id = $1`, customerID)
		if err != nil {
			return fmt.Errorf("delete billing override: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Write(ctx, tx, entries...)
	})
}
