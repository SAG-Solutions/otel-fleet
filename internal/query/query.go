// Package query is the read path over stored telemetry: tenant-scoped log
// search and trace exploration against ClickHouse. Every query is bound to one
// customer's TenantId (= client_id), so a customer can only ever see its own
// data.
package query

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

// ErrUpstreamUnavailable is returned when ClickHouse cannot be queried; the API
// maps it to 503.
var ErrUpstreamUnavailable = errors.New("telemetry store unavailable")

// Store resolves a customer to its TenantId.
type Store interface {
	GetCustomer(ctx context.Context, id uuid.UUID) (store.Customer, error)
}

// Service answers the explore endpoints.
type Service struct {
	ch    driver.Conn
	store Store
	log   *slog.Logger
}

// New creates a query service.
func New(ch driver.Conn, st Store, log *slog.Logger) *Service {
	return &Service{ch: ch, store: st, log: log}
}

func (s *Service) tenant(ctx context.Context, customerID uuid.UUID) (string, error) {
	c, err := s.store.GetCustomer(ctx, customerID)
	if err != nil {
		return "", err // store.ErrNotFound → 404
	}
	return c.ClientID, nil
}

// --- logs ---

// LogRecord is one stored log row.
type LogRecord struct {
	Timestamp      time.Time
	SeverityText   string
	SeverityNumber int32
	ServiceName    string
	Body           string
	TraceID        string
	SpanID         string
	Attributes     map[string]string
}

// LogQuery narrows a log search. Empty optional fields are ignored.
type LogQuery struct {
	From, To    time.Time
	Text        string
	Service     string
	MinSeverity int32
	Limit       int
	Before      *time.Time // cursor: strictly older than this
}

// QueryLogs returns matching logs newest-first plus the next cursor.
func (s *Service) QueryLogs(ctx context.Context, customerID uuid.UUID, q LogQuery) ([]LogRecord, *time.Time, error) {
	tenant, err := s.tenant(ctx, customerID)
	if err != nil {
		return nil, nil, err
	}
	limit := clampLimit(q.Limit)

	sql := `SELECT Timestamp, SeverityText, SeverityNumber, ServiceName, Body, TraceId, SpanId,
		mapUpdate(ResourceAttributes, LogAttributes) AS attrs
		FROM otel.otel_logs
		WHERE TenantId = ? AND Timestamp >= ? AND Timestamp < ?`
	args := []any{tenant, q.From.UTC(), q.To.UTC()}
	if q.Text != "" {
		sql += ` AND positionCaseInsensitive(Body, ?) > 0`
		args = append(args, q.Text)
	}
	if q.Service != "" {
		sql += ` AND ServiceName = ?`
		args = append(args, q.Service)
	}
	if q.MinSeverity > 0 {
		sql += ` AND SeverityNumber >= ?`
		args = append(args, q.MinSeverity)
	}
	if q.Before != nil {
		sql += ` AND Timestamp < ?`
		args = append(args, q.Before.UTC())
	}
	sql += ` ORDER BY Timestamp DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.ch.Query(ctx, sql, args...)
	if err != nil {
		s.log.Warn("query logs failed", "err", err)
		return nil, nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer rows.Close()

	out := []LogRecord{}
	for rows.Next() {
		var r LogRecord
		var sev uint8
		if err := rows.Scan(&r.Timestamp, &r.SeverityText, &sev, &r.ServiceName, &r.Body, &r.TraceID, &r.SpanID, &r.Attributes); err != nil {
			return nil, nil, fmt.Errorf("%w: scan: %v", ErrUpstreamUnavailable, err)
		}
		r.SeverityNumber = int32(sev)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	return out, nextCursor(out, limit, func(r LogRecord) time.Time { return r.Timestamp }), nil
}

// --- traces ---

// TraceSummary is one trace, derived from its root span.
type TraceSummary struct {
	TraceID     string
	RootName    string
	RootService string
	StartTime   time.Time
	DurationMs  float64
	SpanCount   int32
	ErrorCount  int32
}

// TraceQuery narrows a trace listing.
type TraceQuery struct {
	From, To      time.Time
	Service       string
	Name          string
	MinDurationMs float64
	ErrorsOnly    bool
	Limit         int
	Before        *time.Time
}

// QueryTraces lists traces newest-first (one row per trace from its root span),
// enriched with span and error counts.
func (s *Service) QueryTraces(ctx context.Context, customerID uuid.UUID, q TraceQuery) ([]TraceSummary, *time.Time, error) {
	tenant, err := s.tenant(ctx, customerID)
	if err != nil {
		return nil, nil, err
	}
	limit := clampLimit(q.Limit)

	// Root spans give one row per trace (name, service, start, duration).
	sql := `SELECT TraceId, SpanName, ServiceName, Timestamp, Duration
		FROM otel.otel_traces
		WHERE TenantId = ? AND ParentSpanId = '' AND Timestamp >= ? AND Timestamp < ?`
	args := []any{tenant, q.From.UTC(), q.To.UTC()}
	if q.Service != "" {
		sql += ` AND ServiceName = ?`
		args = append(args, q.Service)
	}
	if q.Name != "" {
		sql += ` AND positionCaseInsensitive(SpanName, ?) > 0`
		args = append(args, q.Name)
	}
	if q.MinDurationMs > 0 {
		sql += ` AND Duration >= ?`
		args = append(args, int64(q.MinDurationMs*1e6)) // ms → ns
	}
	if q.Before != nil {
		sql += ` AND Timestamp < ?`
		args = append(args, q.Before.UTC())
	}
	sql += ` ORDER BY Timestamp DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.ch.Query(ctx, sql, args...)
	if err != nil {
		s.log.Warn("query traces failed", "err", err)
		return nil, nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer rows.Close()

	summaries := []TraceSummary{}
	ids := []string{}
	for rows.Next() {
		var t TraceSummary
		var durNs uint64
		if err := rows.Scan(&t.TraceID, &t.RootName, &t.RootService, &t.StartTime, &durNs); err != nil {
			return nil, nil, fmt.Errorf("%w: scan: %v", ErrUpstreamUnavailable, err)
		}
		t.DurationMs = float64(durNs) / 1e6
		summaries = append(summaries, t)
		ids = append(ids, t.TraceID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}

	if len(ids) > 0 {
		counts, err := s.traceCounts(ctx, tenant, ids)
		if err != nil {
			return nil, nil, err
		}
		filtered := summaries[:0]
		for _, t := range summaries {
			if c, ok := counts[t.TraceID]; ok {
				t.SpanCount, t.ErrorCount = c.spans, c.errors
			}
			if q.ErrorsOnly && t.ErrorCount == 0 {
				continue
			}
			filtered = append(filtered, t)
		}
		summaries = filtered
	}
	return summaries, nextCursor(summaries, limit, func(t TraceSummary) time.Time { return t.StartTime }), nil
}

type spanCounts struct{ spans, errors int32 }

// traceCounts aggregates span and error counts for a set of trace IDs in one query.
func (s *Service) traceCounts(ctx context.Context, tenant string, ids []string) (map[string]spanCounts, error) {
	rows, err := s.ch.Query(ctx, `
		SELECT TraceId, toInt32(count()), toInt32(countIf(StatusCode = 'STATUS_CODE_ERROR' OR StatusCode = 'Error'))
		FROM otel.otel_traces
		WHERE TenantId = ? AND TraceId IN (?)
		GROUP BY TraceId`, tenant, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer rows.Close()
	out := make(map[string]spanCounts, len(ids))
	for rows.Next() {
		var id string
		var c spanCounts
		if err := rows.Scan(&id, &c.spans, &c.errors); err != nil {
			return nil, fmt.Errorf("%w: scan: %v", ErrUpstreamUnavailable, err)
		}
		out[id] = c
	}
	return out, rows.Err()
}

// Span is one span of a trace.
type Span struct {
	SpanID        string
	ParentSpanID  string
	Name          string
	Service       string
	Kind          string
	StartTime     time.Time
	DurationMs    float64
	StatusCode    string
	StatusMessage string
	Attributes    map[string]string
}

// GetTrace returns all spans of a trace ordered by start time.
func (s *Service) GetTrace(ctx context.Context, customerID uuid.UUID, traceID string) ([]Span, error) {
	tenant, err := s.tenant(ctx, customerID)
	if err != nil {
		return nil, err
	}
	rows, err := s.ch.Query(ctx, `
		SELECT SpanId, ParentSpanId, SpanName, ServiceName, SpanKind, Timestamp, Duration, StatusCode, StatusMessage, SpanAttributes
		FROM otel.otel_traces
		WHERE TenantId = ? AND TraceId = ?
		ORDER BY Timestamp ASC`, tenant, traceID)
	if err != nil {
		s.log.Warn("get trace failed", "err", err)
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer rows.Close()
	out := []Span{}
	for rows.Next() {
		var sp Span
		var durNs uint64
		if err := rows.Scan(&sp.SpanID, &sp.ParentSpanID, &sp.Name, &sp.Service, &sp.Kind, &sp.StartTime, &durNs, &sp.StatusCode, &sp.StatusMessage, &sp.Attributes); err != nil {
			return nil, fmt.Errorf("%w: scan: %v", ErrUpstreamUnavailable, err)
		}
		sp.DurationMs = float64(durNs) / 1e6
		out = append(out, sp)
	}
	return out, rows.Err()
}

func clampLimit(n int) int {
	if n <= 0 {
		return 100
	}
	if n > 1000 {
		return 1000
	}
	return n
}

// nextCursor returns the timestamp of the last row when a full page was
// returned (more may exist), else nil.
func nextCursor[T any](rows []T, limit int, ts func(T) time.Time) *time.Time {
	if len(rows) < limit || len(rows) == 0 {
		return nil
	}
	t := ts(rows[len(rows)-1])
	return &t
}
