//go:build e2e

// Edge-agent enrollment e2e: proves the full OpAMP enrollment path against a
// REAL supervisor + collector. It mints a per-customer bootstrap token, starts
// the compose `edge` profile edge-agent with that token, and polls the Fleet
// API until the agent enrolls, connects, and reports healthy — bound to the
// right customer.
//
// This is the enrollment half of #55 that was previously only verified by hand
// (docker-run) — now automated. Gated on OTEL_FLEET_E2E_EDGE_AGENT so the plain
// single-region e2e job (which does not start the edge-agent) skips it. The
// edge-agent image must be built first (the CI job does `docker compose
// --profile edge build edge-agent`); locally:
//
//	docker compose -f deploy/compose/docker-compose.yaml --profile edge build edge-agent
//	OTEL_FLEET_E2E_URL=http://localhost:8080 OTEL_FLEET_E2E_EDGE_AGENT=1 \
//	  go test -tags=e2e -run TestEdgeAgent ./test/e2e/... -timeout 300s
//
// The supervisor dials ws://host.docker.internal:4320/v1/opamp (the control
// plane's OpAMP listener on the host), so the control plane must be serving
// OpAMP in plaintext (the default when no OTEL_FLEET_ROLE / TLS env is set).
package e2e

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

// composeFile locates the dev compose file relative to this package (test/e2e).
func composeFile() string {
	return envOr("OTEL_FLEET_E2E_COMPOSE_FILE", "../../deploy/compose/docker-compose.yaml")
}

// compose runs `docker compose -f <file> --profile edge <args...>`. token, when
// non-empty, is exported as OTEL_FLEET_BOOTSTRAP_TOKEN so the compose file's
// ${OTEL_FLEET_BOOTSTRAP_TOKEN} interpolation picks it up.
func compose(t *testing.T, token string, args ...string) ([]byte, error) {
	t.Helper()
	full := append([]string{"compose", "-f", composeFile(), "--profile", "edge"}, args...)
	cmd := exec.Command("docker", full...)
	cmd.Env = os.Environ()
	if token != "" {
		cmd.Env = append(cmd.Env, "OTEL_FLEET_BOOTSTRAP_TOKEN="+token)
	}
	out, err := cmd.CombinedOutput()
	return out, err
}

func TestEdgeAgentEnrollment(t *testing.T) {
	if os.Getenv("OTEL_FLEET_E2E_EDGE_AGENT") == "" {
		t.Skip("set OTEL_FLEET_E2E_EDGE_AGENT=1 (edge-agent image built, OpAMP served) to run")
	}
	c := newClient(t)

	// Admin session (dev-login → CSRF for mutations).
	resp, data := c.do(t, http.MethodPost, "/api/v1/auth/dev-login", map[string]string{"email": "js@sag-solutions.com"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("dev-login: %d %s", resp.StatusCode, data)
	}
	resp, data = c.do(t, http.MethodGet, "/api/v1/me", nil)
	var me struct{ CsrfToken string `json:"csrfToken"` }
	mustJSON(t, data, &me)
	c.csrf = me.CsrfToken

	// Customer to enroll the agent under.
	slug := fmt.Sprintf("edge-%d", time.Now().UnixNano())
	resp, data = c.do(t, http.MethodPost, "/api/v1/customers", map[string]string{"name": "Edge " + slug, "slug": slug})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create customer: %d %s", resp.StatusCode, data)
	}
	var created struct {
		Customer struct {
			ID string `json:"id"`
		} `json:"customer"`
	}
	mustJSON(t, data, &created)
	custID := created.Customer.ID
	if custID == "" {
		t.Fatalf("missing customer id: %s", data)
	}

	// Mint a bootstrap token; `secret` is the full otm_bt_... enrollment token
	// (shown once). tokenPrefix is only the lookup prefix and is NOT usable.
	resp, data = c.do(t, http.MethodPost,
		fmt.Sprintf("/api/v1/customers/%s/bootstrap-tokens", custID),
		map[string]any{"name": "e2e edge enrollment"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create bootstrap token: %d %s", resp.StatusCode, data)
	}
	var tok struct {
		Secret string `json:"secret"`
	}
	mustJSON(t, data, &tok)
	if tok.Secret == "" {
		t.Fatalf("bootstrap token secret empty: %s", data)
	}

	// Start the edge-agent (supervisor + collector) with the token. It dials
	// the host's OpAMP listener, authenticates with the token → the server
	// enrolls an `agents` row bound to this customer.
	if out, err := compose(t, tok.Secret, "up", "-d", "--force-recreate", "edge-agent"); err != nil {
		t.Fatalf("start edge-agent: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := compose(t, "", "rm", "-sf", "edge-agent"); err != nil {
			t.Logf("edge-agent cleanup: %v\n%s", err, out)
		}
		// Drop the per-agent state volume so a local re-run enrolls fresh (a
		// persisted instance UID would otherwise reconnect as the prior run's
		// agent). Best-effort; the project-prefixed name matches compose `name:`.
		_ = exec.Command("docker", "volume", "rm", "-f", "otel-fleet-dev_edgestate").Run()
	})

	// Poll the Fleet API until the edge agent for this customer is connected
	// AND healthy. Allow generous time: cold container start + config apply
	// (config_apply_timeout 30s) + first health report + the 15s write-behind
	// last_seen/heartbeat flush.
	q := fmt.Sprintf("/api/v1/agents?class=edge&customerId=%s", custID)
	deadline := time.Now().Add(180 * time.Second)
	var lastStatus int
	var lastBody []byte
	for {
		resp, data = c.do(t, http.MethodGet, q, nil)
		lastStatus, lastBody = resp.StatusCode, data
		if resp.StatusCode == http.StatusOK {
			var list struct {
				Agents []struct {
					ID          string `json:"id"`
					CustomerID  string `json:"customerId"`
					Class       string `json:"class"`
					Connected   bool   `json:"connected"`
					Healthy     *bool  `json:"healthy"`
					InstanceUID string `json:"instanceUid"`
				} `json:"agents"`
			}
			mustJSON(t, data, &list)
			for _, a := range list.Agents {
				if a.CustomerID != custID || a.Class != "edge" {
					continue
				}
				if a.Connected && a.Healthy != nil && *a.Healthy {
					t.Logf("edge agent enrolled: id=%s instance=%s connected+healthy for customer %s",
						a.ID, a.InstanceUID, custID)
					return
				}
			}
		}
		if time.Now().After(deadline) {
			out, _ := compose(t, "", "logs", "--tail=120", "edge-agent")
			t.Fatalf("edge agent did not become connected+healthy within timeout "+
				"(last: %d %s)\n=== edge-agent logs ===\n%s", lastStatus, truncate(lastBody, 400), out)
		}
		time.Sleep(5 * time.Second)
	}
}
