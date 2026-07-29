package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestSecurityMetricsCountsDenialsByReason(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newSecurityMetrics(reg)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)

	denyRequest(httptest.NewRecorder(), req, nil, m, http.StatusForbidden, codeForbidden, "no", reasonRequiresAdmin, "e@x")
	denyRequest(httptest.NewRecorder(), req, nil, m, http.StatusTooManyRequests, codeRateLimited, "slow", reasonRateLimited, "")
	denyRequest(httptest.NewRecorder(), req, nil, m, http.StatusForbidden, codeForbidden, "no", reasonRequiresAdmin, "e@x")

	if got := testutil.ToFloat64(m.denied.WithLabelValues(reasonRequiresAdmin)); got != 2 {
		t.Fatalf("requires_admin count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.denied.WithLabelValues(reasonRateLimited)); got != 1 {
		t.Fatalf("rate_limited count = %v, want 1", got)
	}

	// The last denial still wrote a well-formed error response.
	rec := httptest.NewRecorder()
	denyRequest(rec, req, nil, m, http.StatusForbidden, codeForbidden, "denied", reasonCSRF, "-")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	// nil metrics + nil logger must be a no-op, not a panic.
	denyRequest(httptest.NewRecorder(), req, nil, nil, http.StatusForbidden, codeForbidden, "x", reasonCSRF, "")
}

func TestNewSecurityMetricsNilRegistry(t *testing.T) {
	if m := newSecurityMetrics(nil); m != nil {
		t.Fatalf("nil registry should yield nil metrics, got %v", m)
	}
	// inc on a nil receiver is safe.
	var m *securityMetrics
	m.inc(reasonUnauthenticated)
}
