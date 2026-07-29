// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantauth // import "github.com/sag-solutions/otel-fleet/collector/extension/tenantauth"

import "go.opentelemetry.io/collector/client"

// Attribute names exposed through client.AuthData. Downstream processors
// (tenantstamp, tenantquota) read these.
const (
	AttrTenantID   = "tenant.id"
	AttrClientID   = "client.id"
	AttrCustomerID = "customer.id"
	// AttrRateLimitItemsPerSec is the per-tenant ingest quota in items
	// (log records / spans / metric data points) per second, as returned by
	// ValidateAPIKeyResponse.rate_limit_items_per_sec. Exposed as an int64
	// (NOT a string); 0 means unlimited. Consumed by the tenantquota
	// processor. Cached with the same TTL as the identity, so limit changes
	// propagate within the cache TTL (30s in the shipped gateway config).
	AttrRateLimitItemsPerSec = "rate_limit_items_per_sec"
)

var _ client.AuthData = (*authData)(nil)

// authData carries the identity resolved for a validated API key.
type authData struct {
	tenantID   string
	clientID   string
	customerID string
	// rateLimitItemsPerSec is the per-tenant ingest quota; 0 = unlimited.
	rateLimitItemsPerSec int64
}

func (a *authData) GetAttribute(name string) any {
	switch name {
	case AttrTenantID:
		return a.tenantID
	case AttrClientID:
		return a.clientID
	case AttrCustomerID:
		return a.customerID
	case AttrRateLimitItemsPerSec:
		return a.rateLimitItemsPerSec
	default:
		return nil
	}
}

func (a *authData) GetAttributeNames() []string {
	return []string{AttrTenantID, AttrClientID, AttrCustomerID, AttrRateLimitItemsPerSec}
}
