package api

import (
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
)

// httpMetrics records control-plane API throughput and latency for health/SLO
// dashboards. Labels are deliberately low-cardinality — method plus a status
// CLASS (2xx/3xx/4xx/5xx), no path — so there is no per-route or per-customer
// series explosion. It is nil-safe: a nil receiver (no registry, e.g. unit
// tests) is a pass-through.
type httpMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newHTTPMetrics(reg prometheus.Registerer) *httpMetrics {
	if reg == nil {
		return nil
	}
	m := &httpMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "otel_fleet_http_requests_total",
			Help: "Control-plane HTTP requests, by method and status class (2xx/3xx/4xx/5xx).",
		}, []string{"method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "otel_fleet_http_request_duration_seconds",
			Help:    "Control-plane HTTP request duration in seconds, by method.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

// middleware times each request and records it once the handler returns. Safe
// to use on a nil *httpMetrics (returns next unchanged).
func (m *httpMetrics) middleware(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		m.requests.WithLabelValues(r.Method, statusClass(ww.Status())).Inc()
		m.duration.WithLabelValues(r.Method).Observe(time.Since(start).Seconds())
	})
}

// statusClass buckets a status code into 2xx/3xx/4xx/5xx, bounding label
// cardinality while still separating success from client/server errors. A
// handler that never called WriteHeader reports 0 → treated as 200 (net/http's
// implicit default).
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}
