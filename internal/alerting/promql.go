package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// vmPromQLSource evaluates instant PromQL queries against a VictoriaMetrics /
// Prometheus-compatible endpoint. A rule's query should aggregate to a single
// series (e.g. sum(...), avg(...)); if it returns a vector, the first sample is
// used.
type vmPromQLSource struct {
	baseURL string
	httpc   *http.Client
}

// NewVMPromQLSource builds a PromQLSource over a VictoriaMetrics base URL
// (e.g. http://victoriametrics:8428).
func NewVMPromQLSource(baseURL string) PromQLSource {
	return &vmPromQLSource{baseURL: baseURL, httpc: &http.Client{Timeout: 10 * time.Second}}
}

func (v *vmPromQLSource) Query(ctx context.Context, query string) (float64, bool, error) {
	u := v.baseURL + "/api/v1/query?" + url.Values{"query": {query}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, false, err
	}
	resp, err := v.httpc.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("victoriametrics query: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("victoriametrics query: status %d", resp.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Value [2]any `json:"value"` // [ts, "value"]
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, false, fmt.Errorf("victoriametrics query: decode: %w", err)
	}
	if body.Status != "success" || len(body.Data.Result) == 0 {
		return 0, false, nil // no data
	}
	str, ok := body.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, false, nil
	}
	value, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0, false, fmt.Errorf("victoriametrics query: parse value %q: %w", str, err)
	}
	return value, true, nil
}
