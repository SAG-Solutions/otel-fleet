package api

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipRateLimiter is a per-client-IP token-bucket limiter. It bounds memory by
// evicting buckets not seen within ttl. It must sit behind chimw.RealIP so
// r.RemoteAddr reflects the true client address (or the fronting proxy's
// forwarded value).
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
	rps     rate.Limit
	burst   int
	ttl     time.Duration
}

type ipBucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func newIPRateLimiter(rps, burst int) *ipRateLimiter {
	return &ipRateLimiter{
		buckets: map[string]*ipBucket{},
		rps:     rate.Limit(rps),
		burst:   burst,
		ttl:     10 * time.Minute,
	}
}

// allow reports whether a request from ip is permitted at now, consuming one
// token. A fresh bucket starts full (burst tokens).
func (l *ipRateLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{lim: rate.NewLimiter(l.rps, l.burst)}
		l.buckets[ip] = b
	}
	b.seen = now
	return b.lim.AllowN(now, 1)
}

// cleanup drops buckets idle longer than ttl.
func (l *ipRateLimiter) cleanup(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, b := range l.buckets {
		if now.Sub(b.seen) > l.ttl {
			delete(l.buckets, ip)
		}
	}
}

// middleware enforces the limit per client IP, answering 429 with a Retry-After
// header when exceeded. It starts a background janitor to evict idle buckets.
func (l *ipRateLimiter) middleware() func(http.Handler) http.Handler {
	go func() {
		t := time.NewTicker(l.ttl)
		defer t.Stop()
		for range t.C {
			l.cleanup(time.Now())
		}
	}()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.allow(clientIP(r), time.Now()) {
				w.Header().Set("Retry-After", "1")
				writeError(w, http.StatusTooManyRequests, codeRateLimited, "rate limit exceeded, slow down")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the host portion of r.RemoteAddr (set by chimw.RealIP).
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// maxBodyBytes caps request body size; a larger body fails the handler's decode
// (surfaced as 400) instead of being buffered wholesale.
func maxBodyBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}
