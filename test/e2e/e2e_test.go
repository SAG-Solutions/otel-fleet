//go:build e2e

// Package e2e is a full-stack smoke test: it drives the real control plane
// (session auth, CSRF, RBAC, handlers), sends real OTLP through the gateway
// (tenantauth + tenantstamp → ClickHouse), and reads it back through the
// Explore query path. It catches wiring regressions the store-integration and
// unit tests cannot — e.g. the login-500 that once broke every session.
//
// It assumes a running stack (CI brings it up via docker compose + `go run`):
//
//	OTEL_FLEET_E2E_URL=http://localhost:8080 \
//	OTEL_FLEET_E2E_OTLP_HTTP=http://localhost:4318 \
//	  go test -tags=e2e ./test/e2e/...
//
// With OTEL_FLEET_E2E_URL unset the test skips, so `go test ./...` stays green.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"testing"
	"time"
)

type client struct {
	base string
	otlp string
	http *http.Client
	csrf string
}

func newClient(t *testing.T) *client {
	t.Helper()
	base := os.Getenv("OTEL_FLEET_E2E_URL")
	if base == "" {
		t.Skip("set OTEL_FLEET_E2E_URL to run the e2e smoke test")
	}
	otlp := os.Getenv("OTEL_FLEET_E2E_OTLP_HTTP")
	if otlp == "" {
		otlp = "http://localhost:4318"
	}
	jar, _ := cookiejar.New(nil)
	return &client{base: base, otlp: otlp, http: &http.Client{Timeout: 15 * time.Second, Jar: jar}}
}

func (c *client) do(t *testing.T, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func TestE2ESmoke(t *testing.T) {
	c := newClient(t)

	// 1. Dev login → session cookie. (Regression guard: this is the exact path
	// the userCols bug broke.)
	resp, data := c.do(t, http.MethodPost, "/api/v1/auth/dev-login", map[string]string{"email": "js@sag-solutions.com"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("dev-login: %d %s", resp.StatusCode, data)
	}

	// 2. /me → role + CSRF token for mutations.
	resp, data = c.do(t, http.MethodGet, "/api/v1/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me: %d %s", resp.StatusCode, data)
	}
	var me struct {
		Role      string `json:"role"`
		CsrfToken string `json:"csrfToken"`
	}
	mustJSON(t, data, &me)
	if me.Role != "admin" {
		t.Fatalf("expected admin, got %q", me.Role)
	}
	c.csrf = me.CsrfToken

	// 3. Create a customer → id + show-once initial API key.
	slug := fmt.Sprintf("e2e-%d", time.Now().UnixNano())
	resp, data = c.do(t, http.MethodPost, "/api/v1/customers", map[string]string{"name": "E2E " + slug, "slug": slug})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create customer: %d %s", resp.StatusCode, data)
	}
	var created struct {
		Customer struct {
			ID string `json:"id"`
		} `json:"customer"`
		InitialAPIKey struct {
			Secret string `json:"secret"`
		} `json:"initialApiKey"`
	}
	mustJSON(t, data, &created)
	custID, key := created.Customer.ID, created.InitialAPIKey.Secret
	if custID == "" || key == "" {
		t.Fatalf("missing customer id / key: %s", data)
	}

	// 4. Send OTLP logs through the gateway with the API key. A unique body
	// lets us find exactly this record on the read path.
	marker := fmt.Sprintf("e2e-marker-%d", time.Now().UnixNano())
	c.sendOTLPLog(t, key, marker)

	// 5. Poll the Explore read path until the record lands (ingest → gateway →
	// ClickHouse → query). Generous timeout for batch/export + MV latency.
	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	q := fmt.Sprintf("/api/v1/customers/%s/logs?from=%s&to=%s&limit=100", custID, url.QueryEscape(from), url.QueryEscape(to))

	deadline := time.Now().Add(90 * time.Second)
	for {
		resp, data = c.do(t, http.MethodGet, q, nil)
		if resp.StatusCode == http.StatusOK && bytes.Contains(data, []byte(marker)) {
			t.Logf("e2e OK: OTLP log %q ingested and read back", marker)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log %q not visible on the read path within timeout (last: %d %s)", marker, resp.StatusCode, truncate(data, 300))
		}
		time.Sleep(3 * time.Second)
	}
}

// sendOTLPLog posts a single OTLP/HTTP log record to the gateway.
func (c *client) sendOTLPLog(t *testing.T, key, body string) {
	t.Helper()
	nano := fmt.Sprintf("%d", time.Now().UnixNano())
	payload := map[string]any{
		"resourceLogs": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "e2e"}},
			}},
			"scopeLogs": []any{map[string]any{"logRecords": []any{
				map[string]any{
					"timeUnixNano":   nano,
					"severityNumber": 9,
					"severityText":   "Info",
					"body":           map[string]any{"stringValue": body},
				},
			}}},
		}},
	}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, c.otlp+"/v1/logs", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("OTLP send: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OTLP send: %d %s", resp.StatusCode, truncate(data, 300))
	}
}

func mustJSON(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %s: %v", truncate(data, 200), err)
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
