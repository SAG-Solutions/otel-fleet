package stats

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

// CostDay is one daily ingest-volume bucket of a customer. Days without data
// are omitted (sparse); Bytes is 0 for rows written before the byte-
// accounting MVs existed.
type CostDay struct {
	Date  time.Time
	Items int64
	Bytes int64
}

// CustomerCost is the ingest volume of one customer over the query range.
type CustomerCost struct {
	CustomerID uuid.UUID
	Name       string
	Items      int64
	Bytes      int64
	Days       []CostDay
}

// GetCost aggregates per-customer ingest volume (items + estimated bytes) in
// daily buckets for [from, to). TenantIds unknown to the control plane are
// skipped; customers are sorted by bytes descending.
func (s *Service) GetCost(ctx context.Context, from, to time.Time) ([]CustomerCost, error) {
	rows, err := s.ch.Query(ctx, `
		SELECT TenantId, toDate(Minute) AS d, sum(Items) AS items, sum(Bytes) AS bytes
		FROM ingest_counts_1m
		WHERE Minute >= ? AND Minute < ?
		GROUP BY TenantId, d
		ORDER BY TenantId, d`, from.UTC(), to.UTC())
	if err != nil {
		s.log.Warn("stats cost: clickhouse query failed", "err", err)
		return nil, fmt.Errorf("%w: clickhouse: %v", ErrUpstreamUnavailable, err)
	}
	defer rows.Close()

	perTenant := map[string]*CustomerCost{}
	var tenantOrder []string
	for rows.Next() {
		var tenantID string
		var day time.Time
		var items, bytes uint64
		if err := rows.Scan(&tenantID, &day, &items, &bytes); err != nil {
			return nil, fmt.Errorf("%w: clickhouse scan: %v", ErrUpstreamUnavailable, err)
		}
		cc, ok := perTenant[tenantID]
		if !ok {
			cc = &CustomerCost{}
			perTenant[tenantID] = cc
			tenantOrder = append(tenantOrder, tenantID)
		}
		cc.Items += int64(items)
		cc.Bytes += int64(bytes)
		cc.Days = append(cc.Days, CostDay{Date: day.UTC(), Items: int64(items), Bytes: int64(bytes)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: clickhouse: %v", ErrUpstreamUnavailable, err)
	}

	// Map ClickHouse TenantId (= client_id) back to customers.
	refs, err := s.store.ListCustomerRefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list customer refs: %w", err)
	}
	byClientID := make(map[string]store.CustomerRef, len(refs))
	for _, r := range refs {
		byClientID[r.ClientID] = r
	}

	out := make([]CustomerCost, 0, len(perTenant))
	for _, tenantID := range tenantOrder {
		ref, ok := byClientID[tenantID]
		if !ok {
			continue // data from tenants unknown to the control plane
		}
		cc := perTenant[tenantID]
		cc.CustomerID = ref.ID
		cc.Name = ref.Name
		out = append(out, *cc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
