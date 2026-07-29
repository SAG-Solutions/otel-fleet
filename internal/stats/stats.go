// Package stats serves the dashboard's usage numbers: ingest counts from
// ClickHouse (otel.ingest_counts_1m), refused-request counts from
// VictoriaMetrics, and the active-customer count from PostgreSQL.
package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

// ErrUpstreamUnavailable is returned when ClickHouse cannot be queried;
// the API maps it to 503 {code:"upstream_unavailable"}.
var ErrUpstreamUnavailable = errors.New("upstream unavailable")

// Signals in canonical order.
var Signals = []string{"logs", "traces", "metrics"}

// Store is the PostgreSQL subset the stats service needs.
type Store interface {
	CountActiveCustomers(ctx context.Context) (int, error)
	ListCustomerRefs(ctx context.Context) ([]store.CustomerRef, error)
	GetCustomer(ctx context.Context, id uuid.UUID) (store.Customer, error)
}

// Service answers the stats endpoints.
type Service struct {
	ch    driver.Conn
	store Store
	vmURL string
	httpc *http.Client
	log   *slog.Logger
}

// New creates a stats service. ch may point at an unreachable server; every
// query maps connection failures to ErrUpstreamUnavailable.
func New(ch driver.Conn, st Store, vmURL string, log *slog.Logger) *Service {
	return &Service{
		ch:    ch,
		store: st,
		vmURL: vmURL,
		httpc: &http.Client{Timeout: 10 * time.Second},
		log:   log,
	}
}

// TopCustomer is one entry of the overview leaderboard.
type TopCustomer struct {
	CustomerID uuid.UUID
	Name       string
	Items      int64
}

// Overview is the fleet-wide dashboard summary.
type Overview struct {
	ActiveCustomers int
	Totals          map[string]int64 // per signal
	RefusedRequests int64
	TopCustomers    []TopCustomer
}

// topCustomersLimit bounds the overview leaderboard.
const topCustomersLimit = 5

// GetOverview aggregates the fleet-wide stats for [from, to).
// GetOverview aggregates fleet-wide ingest. allowed limits the result to a set
// of customer IDs for a tenant-scoped caller; nil means unscoped (all
// customers). When scoped, totals/leaderboard/active-count cover only the
// allowed customers, and refused-request counts (fleet-wide, from VM) are
// suppressed to avoid leaking other tenants' volume.
func (s *Service) GetOverview(ctx context.Context, from, to time.Time, allowed map[uuid.UUID]bool) (Overview, error) {
	rows, err := s.ch.Query(ctx, `
		SELECT TenantId, Signal, sum(Items) AS items
		FROM ingest_counts_1m
		WHERE Minute >= ? AND Minute < ?
		GROUP BY TenantId, Signal`, from.UTC(), to.UTC())
	if err != nil {
		s.log.Warn("stats overview: clickhouse query failed", "err", err)
		return Overview{}, fmt.Errorf("%w: clickhouse: %v", ErrUpstreamUnavailable, err)
	}
	defer rows.Close()

	// Accumulate per tenant and signal so a scoped caller's totals can be
	// recomputed from only the allowed customers.
	perTenantSignal := map[string]map[string]int64{}
	for rows.Next() {
		var tenantID, signal string
		var items uint64
		if err := rows.Scan(&tenantID, &signal, &items); err != nil {
			return Overview{}, fmt.Errorf("%w: clickhouse scan: %v", ErrUpstreamUnavailable, err)
		}
		if perTenantSignal[tenantID] == nil {
			perTenantSignal[tenantID] = map[string]int64{}
		}
		perTenantSignal[tenantID][signal] += int64(items)
	}
	if err := rows.Err(); err != nil {
		return Overview{}, fmt.Errorf("%w: clickhouse: %v", ErrUpstreamUnavailable, err)
	}

	// Map ClickHouse TenantId (= client_id) back to customers.
	refs, err := s.store.ListCustomerRefs(ctx)
	if err != nil {
		return Overview{}, fmt.Errorf("list customer refs: %w", err)
	}
	byClientID := make(map[string]store.CustomerRef, len(refs))
	for _, r := range refs {
		byClientID[r.ClientID] = r
	}

	totals := map[string]int64{"logs": 0, "traces": 0, "metrics": 0}
	top := make([]TopCustomer, 0, len(perTenantSignal))
	for tenantID, bySignal := range perTenantSignal {
		ref, ok := byClientID[tenantID]
		if !ok {
			continue // data from tenants unknown to the control plane
		}
		if allowed != nil && !allowed[ref.ID] {
			continue // out of the caller's scope
		}
		var items int64
		for sig, n := range bySignal {
			totals[sig] += n
			items += n
		}
		top = append(top, TopCustomer{CustomerID: ref.ID, Name: ref.Name, Items: items})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Items != top[j].Items {
			return top[i].Items > top[j].Items
		}
		return top[i].Name < top[j].Name
	})
	if len(top) > topCustomersLimit {
		top = top[:topCustomersLimit]
	}

	active := len(allowed)
	refused := int64(0)
	if allowed == nil {
		if active, err = s.store.CountActiveCustomers(ctx); err != nil {
			return Overview{}, fmt.Errorf("count active customers: %w", err)
		}
		refused = s.refusedRequests(ctx, from, to)
	}

	return Overview{
		ActiveCustomers: active,
		Totals:          totals,
		RefusedRequests: refused,
		TopCustomers:    top,
	}, nil
}

// refusedRequests asks VictoriaMetrics for auth-refused ingest requests in
// the range. Any failure (VM down, empty result) yields 0 — the dashboard
// must not break when metrics are unavailable.
func (s *Service) refusedRequests(ctx context.Context, from, to time.Time) int64 {
	rangeSecs := int64(to.Sub(from).Seconds())
	if rangeSecs <= 0 {
		return 0
	}
	query := fmt.Sprintf(`sum(increase(otel_fleet_auth_requests_total{outcome!="ok"}[%ds]))`, rangeSecs)

	u := s.vmURL + "/api/v1/query?" + url.Values{
		"query": {query},
		"time":  {strconv.FormatInt(to.Unix(), 10)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		s.log.Warn("stats overview: victoriametrics unreachable", "err", err)
		return 0
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		s.log.Warn("stats overview: victoriametrics query failed", "status", resp.StatusCode)
		return 0
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value [2]any `json:"value"` // [ts, "value"]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != "success" {
		s.log.Warn("stats overview: cannot parse victoriametrics response", "err", err, "status", body.Status)
		return 0
	}
	var total float64
	for _, r := range body.Data.Result {
		if str, ok := r.Value[1].(string); ok {
			if v, err := strconv.ParseFloat(str, 64); err == nil {
				total += v
			}
		}
	}
	return int64(math.Round(total))
}

// Point is one throughput sample.
type Point struct {
	Ts    time.Time
	Value float64 // items per second averaged over the step
}

// Series is the throughput of one signal.
type Series struct {
	Signal string
	Points []Point
}

// GetThroughput returns items/sec per signal for one customer, bucketed by
// step. signal == nil means all three signals; signals without data yield an
// empty (not missing) series.
func (s *Service) GetThroughput(ctx context.Context, customerID uuid.UUID, signal *string, from, to time.Time, step time.Duration) ([]Series, error) {
	cust, err := s.store.GetCustomer(ctx, customerID)
	if err != nil {
		return nil, err // store.ErrNotFound maps to 404
	}

	stepSecs := int64(step.Seconds())
	if stepSecs < 1 {
		stepSecs = 60
	}

	signals := Signals
	if signal != nil {
		signals = []string{*signal}
	}

	q := `
		SELECT Signal, toStartOfInterval(Minute, INTERVAL ? SECOND) AS bucket, sum(Items) AS items
		FROM ingest_counts_1m
		WHERE TenantId = ? AND Minute >= ? AND Minute < ?`
	args := []any{stepSecs, cust.ClientID, from.UTC(), to.UTC()}
	if signal != nil {
		q += ` AND Signal = ?`
		args = append(args, *signal)
	}
	q += ` GROUP BY Signal, bucket ORDER BY Signal, bucket`

	rows, err := s.ch.Query(ctx, q, args...)
	if err != nil {
		s.log.Warn("stats throughput: clickhouse query failed", "err", err)
		return nil, fmt.Errorf("%w: clickhouse: %v", ErrUpstreamUnavailable, err)
	}
	defer rows.Close()

	bySignal := make(map[string][]Point, len(signals))
	for _, sig := range signals {
		bySignal[sig] = []Point{}
	}
	for rows.Next() {
		var sig string
		var bucket time.Time
		var items uint64
		if err := rows.Scan(&sig, &bucket, &items); err != nil {
			return nil, fmt.Errorf("%w: clickhouse scan: %v", ErrUpstreamUnavailable, err)
		}
		bySignal[sig] = append(bySignal[sig], Point{
			Ts:    bucket.UTC(),
			Value: float64(items) / float64(stepSecs),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: clickhouse: %v", ErrUpstreamUnavailable, err)
	}

	out := make([]Series, 0, len(signals))
	for _, sig := range signals {
		out = append(out, Series{Signal: sig, Points: bySignal[sig]})
	}
	return out, nil
}

// ParseStep parses a step like "60s" or "5m" (default 60s on empty).
func ParseStep(raw *string) (time.Duration, error) {
	if raw == nil || *raw == "" {
		return time.Minute, nil
	}
	d, err := time.ParseDuration(*raw)
	if err != nil || d < time.Second || d > 24*time.Hour {
		return 0, fmt.Errorf("invalid step %q", *raw)
	}
	return d, nil
}
