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
