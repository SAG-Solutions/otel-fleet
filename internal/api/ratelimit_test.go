package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIPRateLimiterAllowsBurstThenThrottles(t *testing.T) {
	l := newIPRateLimiter(1, 3) // 1 rps, burst 3
	t0 := time.Unix(1_700_000_000, 0)

	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4", t0) {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}
	if l.allow("1.2.3.4", t0) {
		t.Fatal("4th request in the same instant should be throttled")
	}
	// A different IP has its own bucket.
	if !l.allow("5.6.7.8", t0) {
		t.Fatal("distinct IP must not share the bucket")
	}
	// One second later a single token has refilled.
	if !l.allow("1.2.3.4", t0.Add(time.Second)) {
		t.Fatal("token should refill after 1s")
	}
	if l.allow("1.2.3.4", t0.Add(time.Second)) {
		t.Fatal("only one token should have refilled")
	}
}

func TestIPRateLimiterCleanupEvictsIdle(t *testing.T) {
	l := newIPRateLimiter(1, 1)
	t0 := time.Unix(1_700_000_000, 0)
	l.allow("1.2.3.4", t0)
	l.cleanup(t0.Add(l.ttl + time.Minute))
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 0 {
		t.Fatalf("idle bucket should be evicted, have %d", n)
	}
}

func TestRateLimitMiddleware429(t *testing.T) {
	h := newIPRateLimiter(1, 2).middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/customers", nil)
		req.RemoteAddr = "9.9.9.9:5000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
		if i == 2 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("3rd request = %d, want 429", rec.Code)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Error("429 should carry Retry-After")
			}
		}
	}
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("first two within burst = %v, want [200 200 ...]", codes)
	}
}

func TestMaxBodyBytesRejectsOversize(t *testing.T) {
	h := maxBodyBytes(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	big := httptest.NewRequest(http.MethodPost, "/api/v1/customers", strings.NewReader(strings.Repeat("x", 100)))
	bigRec := httptest.NewRecorder()
	h.ServeHTTP(bigRec, big)
	if bigRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body = %d, want 413", bigRec.Code)
	}

	small := httptest.NewRequest(http.MethodPost, "/api/v1/customers", strings.NewReader("xxxxx"))
	smallRec := httptest.NewRecorder()
	h.ServeHTTP(smallRec, small)
	if smallRec.Code != http.StatusOK {
		t.Fatalf("small body = %d, want 200", smallRec.Code)
	}
}
