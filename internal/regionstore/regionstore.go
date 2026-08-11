// Package regionstore holds the per-region telemetry store handles (one
// ClickHouse connection and one VictoriaMetrics base URL per data-residency
// region) and resolves a region name to them. It is the routing seam for
// multi-region reads: a customer-scoped read looks up the customer's region and
// queries that region's stores, so telemetry is read from the region it was
// written to.
//
// An unknown or empty region falls back to the default region, so single-region
// deployments (one synthesized "default" region) and fleet-wide reads that have
// no single region both work unchanged.
package regionstore

import (
	"sort"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Registry maps region names to their ClickHouse connection and VictoriaMetrics
// URL. It is read-only after construction; the connections are owned by the
// caller (opened and closed in main).
type Registry struct {
	ch      map[string]driver.Conn
	vm      map[string]string
	deflt   string
}

// New builds a registry. ch and vm are keyed by region name; deflt is the
// default region (the fallback for unknown/empty region names) and must be a
// key in both maps.
func New(ch map[string]driver.Conn, vm map[string]string, deflt string) *Registry {
	return &Registry{ch: ch, vm: vm, deflt: deflt}
}

// ClickHouse returns the ClickHouse connection for region, or the default
// region's connection when region is empty or unknown.
func (r *Registry) ClickHouse(region string) driver.Conn {
	if c, ok := r.ch[region]; ok {
		return c
	}
	return r.ch[r.deflt]
}

// VM returns the VictoriaMetrics base URL for region, or the default region's
// URL when region is empty/unknown (or the region has none configured).
func (r *Registry) VM(region string) string {
	if u, ok := r.vm[region]; ok && u != "" {
		return u
	}
	return r.vm[r.deflt]
}

// DefaultRegion is the fallback region name.
func (r *Registry) DefaultRegion() string { return r.deflt }

// Names returns every configured region name, sorted for determinism. Fleet-
// wide reads fan a query out over each region's stores and merge the results.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.ch))
	for n := range r.ch {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Multi reports whether more than one region is configured (i.e. fleet-wide
// reads must fan out rather than hitting a single store).
func (r *Registry) Multi() bool { return len(r.ch) > 1 }
