package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// vmPromQLSource evaluates instant PromQL queries against per-region
// VictoriaMetrics / Prometheus-compatible endpoints. A rule's query should
// aggregate to a single series (e.g. sum(...), avg(...)); if it returns a
// vector, the first sample is used. Cluster-wide PromQL rules are evaluated
// against EACH region independently (fire-per-region).
type vmPromQLSource struct {
	vmByRegion map[string]string
	regions    []string // regions with a non-empty VM URL, sorted
	httpc      *http.Client
}

// NewVMPromQLSource builds a PromQLSource over a region→VictoriaMetrics-URL map
// (e.g. {"eu": "http://eu-vm:8428"}). Regions with an empty URL are skipped.
func NewVMPromQLSource(vmByRegion map[string]string) PromQLSource {
	regions := make([]string, 0, len(vmByRegion))
	for name, u := range vmByRegion {
		if u != "" {
			regions = append(regions, name)
		}
	}
	sort.Strings(regions)
	return &vmPromQLSource{vmByRegion: vmByRegion, regions: regions, httpc: &http.Client{Timeout: 10 * time.Second}}
}

// Regions lists the regions with a metrics store to evaluate against.
func (v *vmPromQLSource) Regions() []string { return v.regions }

func (v *vmPromQLSource) Query(ctx context.Context, region, query string) (float64, bool, error) {
	base := v.vmByRegion[region]
	if base == "" {
		return 0, false, nil // no metrics store for this region → no data
	}
	u := base + "/api/v1/query?" + url.Values{"query": {query}}.Encode()
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
