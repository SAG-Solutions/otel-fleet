package api

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

// Denial reasons — a bounded label set for the denial counter (keep it small
// to avoid metric cardinality blow-up).
const (
	reasonUnauthenticated = "unauthenticated"
	reasonUnknownUser     = "unknown_user"
	reasonAccountDisabled = "account_disabled"
	reasonInvalidToken    = "invalid_api_token"
	reasonRequiresAdmin   = "requires_admin"
	reasonInsufficient    = "insufficient_role"
	reasonCSRF            = "csrf"
	reasonRateLimited     = "rate_limited"
	reasonTenantScope     = "tenant_scope"
)

// securityMetrics counts denied requests by reason for alerting/SLOs. It is
// nil-safe: a nil receiver (no registry, e.g. in unit tests) is a no-op.
type securityMetrics struct {
	denied *prometheus.CounterVec
}

func newSecurityMetrics(reg prometheus.Registerer) *securityMetrics {
	if reg == nil {
		return nil
	}
	m := &securityMetrics{
		denied: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "otel_fleet_http_denied_total",
			Help: "HTTP requests denied by the API guard or rate limiter, by reason.",
		}, []string{"reason"}),
	}
	reg.MustRegister(m.denied)
	return m
}

func (m *securityMetrics) inc(reason string) {
	if m != nil && m.denied != nil {
		m.denied.WithLabelValues(reason).Inc()
	}
}

// denyRequest emits a structured security-audit log line, bumps the denial
// counter, and writes the error response. Denials go to logs + metrics (not
// the DB audit_log) so unauthenticated floods can't grow persistent state.
// actor is the acting identity when known (email / "api-token"), else "-".
func denyRequest(w http.ResponseWriter, r *http.Request, log *slog.Logger, m *securityMetrics,
	status int, code, msg, reason, actor string) {
	if actor == "" {
		actor = "-"
	}
	if log != nil {
		log.Warn("security: request denied",
			"reason", reason,
			"status", status,
			"method", r.Method,
			"path", r.URL.Path,
			"remote_ip", clientIP(r),
			"actor", actor,
		)
	}
	m.inc(reason)
	writeError(w, status, code, msg)
}
