package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrBadQuery marks an invalid PromQL query (VM rejects it); the API maps it to
// 400. Distinct from ErrUpstreamUnavailable (VM unreachable → 503).
var ErrBadQuery = errors.New("invalid PromQL query")

// MetricPoint is one sample in a time series.
type MetricPoint struct {
	TS    time.Time
	Value float64
}

// MetricSeries is one labelled series returned by a range query.
type MetricSeries struct {
	Labels map[string]string
	Points []MetricPoint
}

// QueryRange proxies an instant-vector PromQL query over [from,to] at step to
// VictoriaMetrics' /api/v1/query_range and returns the matrix. Admin-only at
// the API layer — it can read every metric, including per-tenant self-telemetry.
func (s *Service) QueryRange(ctx context.Context, query string, from, to time.Time, step time.Duration) ([]MetricSeries, error) {
	return s.queryRange(ctx, query, from, to, step, nil)
}

// QueryRangeScoped is QueryRange with VictoriaMetrics `extra_filters[]` label
// matchers applied to EVERY selector in the query, server-side. Used for
// per-tenant queries: the caller passes {tenant_id="<clientID>"} so a scoped
// user can run arbitrary PromQL but only ever sees their own tenant's series
// (no query-injection risk — VM enforces the filter, not string concatenation).
func (s *Service) QueryRangeScoped(ctx context.Context, query string, from, to time.Time, step time.Duration, extraFilters []string) ([]MetricSeries, error) {
	return s.queryRange(ctx, query, from, to, step, extraFilters)
}

func (s *Service) queryRange(ctx context.Context, query string, from, to time.Time, step time.Duration, extraFilters []string) ([]MetricSeries, error) {
	if s.vmURL == "" {
		return nil, ErrUpstreamUnavailable
	}
	vals := url.Values{
		"query": {query},
		"start": {strconv.FormatInt(from.Unix(), 10)},
		"end":   {strconv.FormatInt(to.Unix(), 10)},
		"step":  {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)},
	}
	for _, f := range extraFilters {
		vals.Add("extra_filters[]", f)
	}
	u := s.vmURL + "/api/v1/query_range?" + vals.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		s.log.Warn("metrics query: victoriametrics unreachable", "err", err)
		return nil, ErrUpstreamUnavailable
	}
	defer resp.Body.Close() //nolint:errcheck

	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][2]any          `json:"values"` // [[ts, "value"], ...]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, ErrUpstreamUnavailable
		}
		return nil, fmt.Errorf("metrics query: decode: %w", err)
	}
	// Bad PromQL → VM answers 4xx with status:error.
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity || body.Status == "error" {
		return nil, fmt.Errorf("%w: %s", ErrBadQuery, body.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ErrUpstreamUnavailable
	}

	out := make([]MetricSeries, 0, len(body.Data.Result))
	for _, r := range body.Data.Result {
		s := MetricSeries{Labels: r.Metric, Points: make([]MetricPoint, 0, len(r.Values))}
		for _, v := range r.Values {
			ts, ok := v[0].(float64)
			if !ok {
				continue
			}
			str, ok := v[1].(string)
			if !ok {
				continue
			}
			val, err := strconv.ParseFloat(str, 64)
			if err != nil {
				continue
			}
			s.Points = append(s.Points, MetricPoint{TS: time.Unix(int64(ts), 0).UTC(), Value: val})
		}
		out = append(out, s)
	}
	return out, nil
}
