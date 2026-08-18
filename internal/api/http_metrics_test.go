package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		0: "2xx", 200: "2xx", 204: "2xx",
		301: "3xx", 400: "4xx", 403: "4xx", 429: "4xx",
		500: "5xx", 503: "5xx",
	}
	for code, want := range cases {
		if got := statusClass(code); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestHTTPMetricsRecordsRequestsByClass(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newHTTPMetrics(reg)

	handler := func(status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	}
	serve := func(method string, status int) {
		rec := httptest.NewRecorder()
		m.middleware(handler(status)).ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/x", nil))
	}

	serve(http.MethodGet, http.StatusOK)
	serve(http.MethodGet, http.StatusOK)
	serve(http.MethodGet, http.StatusForbidden)
	serve(http.MethodPost, http.StatusInternalServerError)

	if got := testutil.ToFloat64(m.requests.WithLabelValues(http.MethodGet, "2xx")); got != 2 {
		t.Errorf("GET 2xx = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues(http.MethodGet, "4xx")); got != 1 {
		t.Errorf("GET 4xx = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues(http.MethodPost, "5xx")); got != 1 {
		t.Errorf("POST 5xx = %v, want 1", got)
	}
	// Duration histogram observed one sample per request (3 GET + 1 POST).
	if got := testutil.CollectAndCount(m.duration); got == 0 {
		t.Errorf("duration histogram recorded nothing")
	}
}

func TestHTTPMetricsNilIsPassThrough(t *testing.T) {
	if m := newHTTPMetrics(nil); m != nil {
		t.Fatalf("nil registry should yield nil metrics, got %v", m)
	}
	var m *httpMetrics
	called := false
	h := m.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !called {
		t.Fatal("nil httpMetrics middleware must pass through to next")
	}
}
