// Package retention enforces per-customer telemetry retention overrides by
// submitting lightweight ClickHouse DELETE mutations. Rows older than a
// customer's retention_days are removed ahead of the global 30-day table TTL;
// customers without an override are untouched.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// startupDelay is how long after boot the first run happens (lets the rest of
// the control plane settle and avoids hammering ClickHouse during restarts).
const startupDelay = 2 * time.Minute

// tables lists every per-tenant telemetry table with its time column (see
// deploy/clickhouse/schema/001+002).
var tables = []struct {
	name    string
	timeCol string
}{
	{"otel_logs", "TimestampTime"},
	{"otel_traces", "Timestamp"},
	{"otel_metrics_gauge", "TimeUnix"},
	{"otel_metrics_sum", "TimeUnix"},
	{"otel_metrics_histogram", "TimeUnix"},
	{"otel_metrics_exponential_histogram", "TimeUnix"},
	{"otel_metrics_summary", "TimeUnix"},
}

// CH is the ClickHouse subset the job needs (implemented by driver.Conn).
type CH interface {
	Exec(ctx context.Context, query string, args ...any) error
}

// Store is the persistence subset the job needs.
type Store interface {
	ListCustomers(ctx context.Context, status *string) ([]store.Customer, error)
	WriteAuditEntries(ctx context.Context, entries []audit.Entry) error
}

// Service runs the nightly retention sweep.
type Service struct {
	// resolve returns the ClickHouse connection for a customer's region, so each
	// customer's DELETE mutations run against the region its telemetry lives in.
	resolve  func(region string) CH
	store    Store
	interval time.Duration
	log      *slog.Logger
}

// New wires the retention job. resolve maps a customer's region to its
// ClickHouse connection; interval comes from OTEL_FLEET_RETENTION_INTERVAL
// (default 24h).
func New(resolve func(region string) CH, st Store, interval time.Duration, log *slog.Logger) *Service {
	return &Service{resolve: resolve, store: st, interval: interval, log: log}
}

// Run executes the sweep once shortly after startup and then on every
// interval tick until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	first := time.NewTimer(startupDelay)
	defer first.Stop()
	select {
	case <-first.C:
		s.RunOnce(ctx)
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.RunOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// RunOnce applies every active customer's retention override. Mutations are
// submitted asynchronously (mutations_sync=0) — ClickHouse deletes in the
// background; this job only fires them. Per-customer failures are logged and
// do not stop the sweep.
func (s *Service) RunOnce(ctx context.Context) {
	active := store.CustomerActive
	customers, err := s.store.ListCustomers(ctx, &active)
	if err != nil {
		s.log.Error("retention: list customers failed", "err", err)
		return
	}
	for _, c := range customers {
		if c.RetentionDays == nil {
			continue
		}
		days := *c.RetentionDays
		submitted, errs := s.applyCustomer(ctx, s.resolve(c.Region), c.ClientID, days)
		s.log.Info("retention: sweep for customer",
			"customer", c.ID, "client_id", c.ClientID, "retention_days", days,
			"mutations_submitted", submitted, "errors", errs)
		if submitted == 0 {
			continue
		}
		customerID := c.ID
		if err := s.store.WriteAuditEntries(ctx, []audit.Entry{{
			ActorType:  audit.ActorSystem,
			Action:     "retention.apply",
			EntityType: "customer",
			EntityID:   c.ID.String(),
			CustomerID: &customerID,
			Payload: map[string]any{
				"retention_days":      days,
				"mutations_submitted": submitted,
				"errors":              errs,
			},
		}}); err != nil {
			s.log.Error("retention: write audit entry failed", "customer", c.ID, "err", err)
		}
	}
}

// applyCustomer submits one DELETE mutation per telemetry table and reports
// how many were accepted.
func (s *Service) applyCustomer(ctx context.Context, ch CH, clientID string, days int) (submitted, errs int) {
	for _, stmt := range Statements(days) {
		if err := ch.Exec(ctx, stmt, clientID); err != nil {
			s.log.Error("retention: submit mutation failed", "client_id", clientID, "err", err)
			errs++
			continue
		}
		submitted++
	}
	return submitted, errs
}

// Statements returns the DELETE mutation SQL for a retention of the given
// number of days; each statement takes the TenantId as its single parameter.
// Exported for tests.
func Statements(days int) []string {
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		out = append(out, fmt.Sprintf(
			"ALTER TABLE otel.%s DELETE WHERE TenantId = ? AND %s < now() - INTERVAL %d DAY SETTINGS mutations_sync = 0",
			t.name, t.timeCol, days))
	}
	return out
}

// PurgeStatements returns DELETE mutation SQL that removes ALL of a tenant's
// telemetry with no age bound — for erase-on-delete (right-to-erasure). It
// covers every per-tenant signal table plus the ingest_counts_1m usage rollup,
// so no tenant-identifiable rows remain. Each statement takes the TenantId as
// its single parameter. Exported for tests + the delete path.
func PurgeStatements() []string {
	out := make([]string, 0, len(tables)+1)
	for _, t := range tables {
		out = append(out, fmt.Sprintf("ALTER TABLE otel.%s DELETE WHERE TenantId = ? SETTINGS mutations_sync = 0", t.name))
	}
	// Usage rollup that feeds cost/overview; also keyed by TenantId.
	out = append(out, "ALTER TABLE otel.ingest_counts_1m DELETE WHERE TenantId = ? SETTINGS mutations_sync = 0")
	return out
}

// PurgeCustomer submits every purge mutation for one tenant against ch. Errors
// are returned as a count (mutations are async: mutations_sync=0), letting the
// caller log without failing the delete. ch may be nil (purge disabled) → no-op.
func PurgeCustomer(ctx context.Context, ch CH, clientID string) (submitted, errs int) {
	if ch == nil {
		return 0, 0
	}
	for _, stmt := range PurgeStatements() {
		if err := ch.Exec(ctx, stmt, clientID); err != nil {
			errs++
			continue
		}
		submitted++
	}
	return submitted, errs
}
