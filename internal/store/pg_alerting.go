package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sag-solutions/otel-fleet/internal/audit"
)

const alertRuleCols = `id, name, metric, query, comparison, threshold, window_seconds, customer_id, channel_ids, enabled, created_at, updated_at`

func scanAlertRule(row pgx.Row) (AlertRule, error) {
	var r AlertRule
	err := row.Scan(&r.ID, &r.Name, &r.Metric, &r.Query, &r.Comparison, &r.Threshold, &r.WindowSeconds,
		&r.CustomerID, &r.ChannelIDs, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *PG) listAlertRules(ctx context.Context, enabledOnly bool) ([]AlertRule, error) {
	q := `SELECT ` + alertRuleCols + ` FROM alert_rules`
	if enabledOnly {
		q += ` WHERE enabled`
	}
	q += ` ORDER BY created_at, id`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertRule{}
	for rows.Next() {
		r, err := scanAlertRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PG) ListAlertRules(ctx context.Context) ([]AlertRule, error) {
	return s.listAlertRules(ctx, false)
}

func (s *PG) ListEnabledAlertRules(ctx context.Context) ([]AlertRule, error) {
	return s.listAlertRules(ctx, true)
}

func (s *PG) GetAlertRule(ctx context.Context, id uuid.UUID) (AlertRule, error) {
	r, err := scanAlertRule(s.pool.QueryRow(ctx, `SELECT `+alertRuleCols+` FROM alert_rules WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertRule{}, ErrNotFound
	}
	return r, err
}

func (s *PG) CreateAlertRule(ctx context.Context, r NewAlertRule, entries []audit.Entry) (AlertRule, error) {
	channels := r.ChannelIDs
	if channels == nil {
		channels = []uuid.UUID{}
	}
	var out AlertRule
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanAlertRule(tx.QueryRow(ctx, `
			INSERT INTO alert_rules (id, name, metric, query, comparison, threshold, window_seconds, customer_id, channel_ids, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING `+alertRuleCols,
			r.ID, r.Name, r.Metric, r.Query, r.Comparison, r.Threshold, r.WindowSeconds, r.CustomerID, channels, r.Enabled))
		if isForeignKeyViolation(err, "alert_rules_customer_id_fkey") {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("insert alert rule: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return AlertRule{}, err
	}
	return out, nil
}

func (s *PG) UpdateAlertRule(ctx context.Context, id uuid.UUID, upd AlertRuleUpdate, entries []audit.Entry) (AlertRule, error) {
	var out AlertRule
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanAlertRule(tx.QueryRow(ctx, `
			UPDATE alert_rules
			SET name           = COALESCE($2, name),
			    metric         = COALESCE($3, metric),
			    comparison     = COALESCE($4, comparison),
			    threshold      = COALESCE($5, threshold),
			    window_seconds = COALESCE($6, window_seconds),
			    channel_ids    = COALESCE($7, channel_ids),
			    enabled        = COALESCE($8, enabled),
			    query          = COALESCE($9, query),
			    updated_at     = now()
			WHERE id = $1
			RETURNING `+alertRuleCols,
			id, upd.Name, upd.Metric, upd.Comparison, upd.Threshold, upd.WindowSeconds, upd.ChannelIDs, upd.Enabled, upd.Query))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("update alert rule: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return AlertRule{}, err
	}
	return out, nil
}

func (s *PG) DeleteAlertRule(ctx context.Context, id uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete alert rule: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Write(ctx, tx, entries...)
	})
}

const maintenanceWindowCols = `id, name, starts_at, ends_at, created_at`

func scanMaintenanceWindow(row pgx.Row) (MaintenanceWindow, error) {
	var w MaintenanceWindow
	err := row.Scan(&w.ID, &w.Name, &w.StartsAt, &w.EndsAt, &w.CreatedAt)
	return w, err
}

func (s *PG) scanMaintenanceWindows(ctx context.Context, q string, args ...any) ([]MaintenanceWindow, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MaintenanceWindow{}
	for rows.Next() {
		w, err := scanMaintenanceWindow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *PG) ListMaintenanceWindows(ctx context.Context) ([]MaintenanceWindow, error) {
	return s.scanMaintenanceWindows(ctx, `SELECT `+maintenanceWindowCols+` FROM alert_maintenance_windows ORDER BY starts_at DESC, id`)
}

func (s *PG) ListActiveMaintenanceWindows(ctx context.Context, at time.Time) ([]MaintenanceWindow, error) {
	return s.scanMaintenanceWindows(ctx, `SELECT `+maintenanceWindowCols+` FROM alert_maintenance_windows WHERE starts_at <= $1 AND ends_at > $1`, at)
}

func (s *PG) CreateMaintenanceWindow(ctx context.Context, w NewMaintenanceWindow, entries []audit.Entry) (MaintenanceWindow, error) {
	var out MaintenanceWindow
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = scanMaintenanceWindow(tx.QueryRow(ctx, `
			INSERT INTO alert_maintenance_windows (id, name, starts_at, ends_at)
			VALUES ($1, $2, $3, $4)
			RETURNING `+maintenanceWindowCols,
			w.ID, w.Name, w.StartsAt, w.EndsAt))
		if err != nil {
			return fmt.Errorf("insert maintenance window: %w", err)
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return MaintenanceWindow{}, err
	}
	return out, nil
}

func (s *PG) DeleteMaintenanceWindow(ctx context.Context, id uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM alert_maintenance_windows WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete maintenance window: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Write(ctx, tx, entries...)
	})
}
