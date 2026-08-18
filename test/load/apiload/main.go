// Command apiload is a tiny, dependency-free load generator for the otel-fleet
// control-plane HTTP API. It dev-logs-in (so it needs OTEL_FLEET_DEV_LOGIN=true),
// then drives one endpoint with N concurrent workers for a fixed duration and
// reports throughput + latency percentiles. It is a capacity-planning aid, not a
// benchmark of record — run it against a production-like control plane, not a
// laptop, for numbers you can size on.
//
// Example:
//
//	go run ./test/load/apiload \
//	  -url http://localhost:8080 -email js@sag-solutions.com \
//	  -path '/api/v1/stats/overview?from=2020-01-01T00:00:00Z&to=2030-01-01T00:00:00Z' \
//	  -c 50 -d 20s
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	base := flag.String("url", "http://localhost:8080", "control-plane base URL")
	email := flag.String("email", "js@sag-solutions.com", "dev-login email (needs OTEL_FLEET_DEV_LOGIN=true)")
	path := flag.String("path", "/api/v1/stats/overview?from=2020-01-01T00:00:00Z&to=2030-01-01T00:00:00Z", "request path")
	method := flag.String("method", http.MethodGet, "HTTP method")
	conc := flag.Int("c", 50, "concurrent workers")
	dur := flag.Duration("d", 20*time.Second, "test duration")
	warmup := flag.Duration("warmup", 2*time.Second, "warm-up (not measured)")
	flag.Parse()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}

	// Authenticate: dev-login sets the session cookie in the jar.
	if err := devLogin(client, *base, *email); err != nil {
		fmt.Fprintf(os.Stderr, "dev-login failed: %v\n(is OTEL_FLEET_DEV_LOGIN=true and the URL reachable?)\n", err)
		os.Exit(1)
	}

	fmt.Printf("apiload: %s %s\n  workers=%d duration=%s (warmup=%s)\n", *method, *path, *conc, *dur, *warmup)

	var (
		mu        sync.Mutex
		latencies []time.Duration
		c2xx      atomic.Int64
		c4xx      atomic.Int64
		c429      atomic.Int64
		c5xx      atomic.Int64
		cErr      atomic.Int64 // transport errors
		measuring atomic.Bool
	)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				start := time.Now()
				req, _ := http.NewRequest(*method, *base+*path, nil)
				resp, err := client.Do(req)
				elapsed := time.Since(start)
				if err != nil {
					cErr.Add(1)
					continue
				}
				code := resp.StatusCode
				drain(resp)
				measure := measuring.Load()
				switch {
				case code == http.StatusTooManyRequests:
					c429.Add(1)
				case code >= 500:
					c5xx.Add(1)
				case code >= 400:
					c4xx.Add(1)
				default:
					c2xx.Add(1)
					if measure {
						mu.Lock()
						latencies = append(latencies, elapsed)
						mu.Unlock()
					}
				}
			}
		}()
	}

	time.Sleep(*warmup)
	c2xx.Store(0)
	c4xx.Store(0)
	c429.Store(0)
	c5xx.Store(0)
	cErr.Store(0)
	measuring.Store(true)
	measureStart := time.Now()
	time.Sleep(*dur)
	measuring.Store(false)
	wall := time.Since(measureStart)
	close(stop)
	wg.Wait()

	report(latencies, counts{c2xx.Load(), c4xx.Load(), c429.Load(), c5xx.Load(), cErr.Load()}, wall)
}

func devLogin(c *http.Client, base, email string) error {
	body := strings.NewReader(fmt.Sprintf(`{"email":%q}`, email))
	req, _ := http.NewRequest(http.MethodPost, base+"/api/v1/auth/dev-login", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	drain(resp)
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

type counts struct{ ok, c4xx, c429, c5xx, err int64 }

func report(latencies []time.Duration, c counts, wall time.Duration) {
	total := c.ok + c.c4xx + c.c429 + c.c5xx + c.err
	fmt.Printf("\nresults (measured %.1fs):\n", wall.Seconds())
	fmt.Printf("  responses: %d 2xx, %d 429 (rate-limited), %d other-4xx, %d 5xx, %d transport-err\n",
		c.ok, c.c429, c.c4xx, c.c5xx, c.err)
	fmt.Printf("  attempted: %.0f req/s total\n", float64(total)/wall.Seconds())
	fmt.Printf("  successful (2xx): %.0f req/s\n", float64(c.ok)/wall.Seconds())
	if len(latencies) == 0 {
		fmt.Println("  latency (2xx): (no samples)")
		return
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p := func(q float64) time.Duration { return latencies[int(float64(len(latencies)-1)*q)] }
	fmt.Printf("  latency (2xx): p50=%s p90=%s p95=%s p99=%s max=%s\n",
		round(p(0.50)), round(p(0.90)), round(p(0.95)), round(p(0.99)), round(latencies[len(latencies)-1]))
}

func round(d time.Duration) time.Duration { return d.Round(time.Millisecond) }
