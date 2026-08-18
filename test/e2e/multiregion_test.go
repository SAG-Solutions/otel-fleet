//go:build e2e

// Multi-region e2e: proves region-aware read routing against a REAL two-region
// setup (two ClickHouse instances). It creates a customer pinned to each region,
// seeds telemetry directly into each region's ClickHouse, and asserts that the
// Explore read path routes each customer's query to its own region and that the
// fleet-wide overview fans out across both regions.
//
// Gated on OTEL_FLEET_E2E_MULTIREGION so it only runs in the dedicated CI job
// (the single-region e2e job leaves it unset → skipped). Requires the control
// plane running with OTEL_FLEET_REGIONS=[eu,us] and the two ClickHouse addrs:
//
//	OTEL_FLEET_E2E_URL=http://localhost:8080 OTEL_FLEET_E2E_MULTIREGION=1 \
//	OTEL_FLEET_E2E_CH_EU=localhost:9000 OTEL_FLEET_E2E_CH_US=localhost:9001 \
//	  go test -tags=e2e -run TestMultiRegion ./test/e2e/...
package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

func chConn(t *testing.T, addr string) driver.Conn {
	t.Helper()
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{Database: "otel", Username: "otelfleet", Password: "otelfleet"},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("clickhouse open %s: %v", addr, err)
	}
	return conn
}

// seedLog inserts one log row for a tenant. TenantId is materialized from
// ResourceAttributes['tenant.id'], so we set that map, not TenantId directly.
func seedLog(t *testing.T, conn driver.Conn, clientID, body string) {
	t.Helper()
	err := conn.Exec(context.Background(),
		`INSERT INTO otel.otel_logs (Timestamp, ServiceName, Body, SeverityText, SeverityNumber, ResourceAttributes)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		time.Now().UTC(), "e2e", body, "Info", uint8(9),
		map[string]string{"tenant.id": clientID})
	if err != nil {
		t.Fatalf("seed log for %s: %v", clientID, err)
	}
}

// seedCount inserts an ingest_counts_1m row the overview fan-out reads.
func seedCount(t *testing.T, conn driver.Conn, clientID string, items uint64) {
	t.Helper()
	err := conn.Exec(context.Background(),
		`INSERT INTO otel.ingest_counts_1m (TenantId, Signal, ServiceName, Minute, Items, Bytes)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		clientID, "logs", "e2e", time.Now().UTC().Truncate(time.Minute), items, uint64(100))
	if err != nil {
		t.Fatalf("seed count for %s: %v", clientID, err)
	}
}

func (c *client) createCustomer(t *testing.T, name, region string) (id, clientID string) {
	t.Helper()
	slug := fmt.Sprintf("mr-%s-%d", region, time.Now().UnixNano())
	resp, data := c.do(t, http.MethodPost, "/api/v1/customers",
		map[string]string{"name": name, "slug": slug, "region": region})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create customer (%s): %d %s", region, resp.StatusCode, data)
	}
	var created struct {
		Customer struct {
			ID       string `json:"id"`
			ClientID string `json:"tenantId"`
			Region   string `json:"region"`
		} `json:"customer"`
	}
	mustJSON(t, data, &created)
	if created.Customer.Region != region {
		t.Fatalf("customer region = %q, want %q", created.Customer.Region, region)
	}
	if created.Customer.ID == "" || created.Customer.ClientID == "" {
		t.Fatalf("missing id/tenantId: %s", data)
	}
	return created.Customer.ID, created.Customer.ClientID
}

func (c *client) logsBody(t *testing.T, custID string) string {
	t.Helper()
	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	q := fmt.Sprintf("/api/v1/customers/%s/logs?from=%s&to=%s&limit=100",
		custID, url.QueryEscape(from), url.QueryEscape(to))
	resp, data := c.do(t, http.MethodGet, q, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs %s: %d %s", custID, resp.StatusCode, data)
	}
	return string(data)
}

func TestMultiRegionRoutingAndFanout(t *testing.T) {
	if os.Getenv("OTEL_FLEET_E2E_MULTIREGION") == "" {
		t.Skip("set OTEL_FLEET_E2E_MULTIREGION=1 (two-region control plane) to run")
	}
	c := newClient(t)
	euAddr := envOr("OTEL_FLEET_E2E_CH_EU", "localhost:9000")
	usAddr := envOr("OTEL_FLEET_E2E_CH_US", "localhost:9001")
	eu := chConn(t, euAddr)
	us := chConn(t, usAddr)

	// Admin session.
	resp, data := c.do(t, http.MethodPost, "/api/v1/auth/dev-login", map[string]string{"email": "js@sag-solutions.com"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("dev-login: %d %s", resp.StatusCode, data)
	}
	resp, data = c.do(t, http.MethodGet, "/api/v1/me", nil)
	var me struct{ CsrfToken string `json:"csrfToken"` }
	mustJSON(t, data, &me)
	c.csrf = me.CsrfToken

	// Two customers, pinned to different regions.
	euID, euClient := c.createCustomer(t, "MR Europe", "eu")
	usID, usClient := c.createCustomer(t, "MR US", "us")

	// Seed each customer's telemetry into ONLY its own region's ClickHouse.
	stamp := time.Now().UnixNano()
	euMarker := fmt.Sprintf("mr-eu-%d", stamp)
	usMarker := fmt.Sprintf("mr-us-%d", stamp)
	seedLog(t, eu, euClient, euMarker)
	seedLog(t, us, usClient, usMarker)
	seedCount(t, eu, euClient, 111)
	seedCount(t, us, usClient, 222)

	// ROUTING: each customer's Explore query reads only its own region's CH.
	// (If euID's query wrongly hit the us CH it would find nothing — euClient's
	// data lives only in eu — so seeing euMarker proves it routed to eu.)
	euLogs := c.logsBody(t, euID)
	if !strings.Contains(euLogs, euMarker) {
		t.Errorf("eu customer logs missing its eu-region marker %q; got: %s", euMarker, euLogs)
	}
	if strings.Contains(euLogs, usMarker) {
		t.Errorf("eu customer logs leaked the us-region marker — residency/routing broken")
	}
	usLogs := c.logsBody(t, usID)
	if !strings.Contains(usLogs, usMarker) {
		t.Errorf("us customer logs missing its us-region marker %q; got: %s", usMarker, usLogs)
	}
	if strings.Contains(usLogs, euMarker) {
		t.Errorf("us customer logs leaked the eu-region marker — residency/routing broken")
	}

	// FAN-OUT: the fleet-wide overview aggregates across BOTH regions.
	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	resp, data = c.do(t, http.MethodGet,
		fmt.Sprintf("/api/v1/stats/overview?from=%s&to=%s", url.QueryEscape(from), url.QueryEscape(to)), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview: %d %s", resp.StatusCode, data)
	}
	var ov struct {
		TopCustomers []struct {
			CustomerID string `json:"customerId"`
			Items      int64  `json:"items"`
		} `json:"topCustomers"`
	}
	mustJSON(t, data, &ov)
	seenEU, seenUS := false, false
	for _, tc := range ov.TopCustomers {
		if tc.CustomerID == euID && tc.Items >= 111 {
			seenEU = true
		}
		if tc.CustomerID == usID && tc.Items >= 222 {
			seenUS = true
		}
	}
	if !seenEU || !seenUS {
		t.Errorf("overview did not fan out across regions: eu-seen=%t us-seen=%t; top=%s", seenEU, seenUS, data)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
