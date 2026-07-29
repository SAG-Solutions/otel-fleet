package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestOpsHandler(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "otel_fleet_test_total"})
	reg.MustRegister(c)
	c.Inc()

	ready := errors.New("db down")
	h := NewOpsHandler(reg, func(context.Context) error { return ready }, func(context.Context) (string, error) { return "receivers: {}\n", nil })
	srv := httptest.NewServer(h)
	defer srv.Close()

	get := func(path string) (int, string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	if code, body := get("/healthz"); code != http.StatusOK || body != "ok" {
		t.Errorf("/healthz = %d %q, want 200 ok", code, body)
	}
	if code, _ := get("/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("/readyz with failing check = %d, want 503", code)
	}
	ready = nil
	if code, _ := get("/readyz"); code != http.StatusOK {
		t.Errorf("/readyz with passing check = %d, want 200", code)
	}
	if code, body := get("/metrics"); code != http.StatusOK || !strings.Contains(body, "otel_fleet_test_total") {
		t.Errorf("/metrics = %d, want 200 containing otel_fleet_test_total", code)
	}
}
