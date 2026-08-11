package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// PipelineStageQuery selects the flow view of one pipeline.
type PipelineStageQuery struct {
	ClientID string
	Signals  []string
	// ExporterRegex matches the `exporter` label of collector self-telemetry
	// for exactly this pipeline's exporters (see pipelines.ExporterLabelRegex).
	ExporterRegex string
	From, To      time.Time
}

// ExporterStage aggregates one exporter instance's counters over the range.
type ExporterStage struct {
	Sent          int64
	SendFailed    int64
	EnqueueFailed int64
	QueueSize     float64
	QueueCapacity float64
}

// PipelineFlow is the stage view: items entering per signal, and per-exporter
// delivery counters.
type PipelineFlow struct {
	Received map[string]int64
	// Exporters is keyed by exporter instance name; ExporterNames preserves a
	// sorted order for stable API output.
	Exporters     map[string]ExporterStage
	ExporterNames []string
}

// GetPipelineStages answers the pipeline stage-stats endpoint. Received counts
// come from ClickHouse (per tenant — a tenant's pipelines of the same signal
// share this number). Exporter counters come from VictoriaMetrics; when it is
// unreachable they are zero, matching the dashboard's degrade-gracefully rule.
func (s *Service) GetPipelineStages(ctx context.Context, q PipelineStageQuery) (PipelineFlow, error) {
	flow := PipelineFlow{
		Received:  map[string]int64{},
		Exporters: map[string]ExporterStage{},
	}

	if len(q.Signals) > 0 {
		rows, err := s.stores.ClickHouse("").Query(ctx, `
			SELECT Signal, sum(Items) AS items
			FROM ingest_counts_1m
			WHERE TenantId = ? AND Minute >= ? AND Minute < ? AND Signal IN ?
			GROUP BY Signal`, q.ClientID, q.From.UTC(), q.To.UTC(), q.Signals)
		if err != nil {
			s.log.Warn("stage stats: clickhouse query failed", "err", err)
			return PipelineFlow{}, fmt.Errorf("%w: clickhouse: %v", ErrUpstreamUnavailable, err)
		}
		defer rows.Close()
		for rows.Next() {
			var sig string
			var items uint64
			if err := rows.Scan(&sig, &items); err != nil {
				return PipelineFlow{}, fmt.Errorf("%w: clickhouse scan: %v", ErrUpstreamUnavailable, err)
			}
			flow.Received[sig] = int64(items)
		}
		if err := rows.Err(); err != nil {
			return PipelineFlow{}, fmt.Errorf("%w: clickhouse: %v", ErrUpstreamUnavailable, err)
		}
	}

	rangeSecs := int64(q.To.Sub(q.From).Seconds())
	if rangeSecs <= 0 {
		return flow, nil
	}
	itemMetrics := `(spans|metric_points|log_records)`
	counters := []struct {
		query string
		set   func(st *ExporterStage, v float64)
	}{
		{fmt.Sprintf(`sum by (exporter) (increase({__name__=~"otelcol_exporter_sent_%s",exporter=~%q}[%ds]))`, itemMetrics, q.ExporterRegex, rangeSecs),
			func(st *ExporterStage, v float64) { st.Sent = int64(math.Round(v)) }},
		{fmt.Sprintf(`sum by (exporter) (increase({__name__=~"otelcol_exporter_send_failed_%s",exporter=~%q}[%ds]))`, itemMetrics, q.ExporterRegex, rangeSecs),
			func(st *ExporterStage, v float64) { st.SendFailed = int64(math.Round(v)) }},
		{fmt.Sprintf(`sum by (exporter) (increase({__name__=~"otelcol_exporter_enqueue_failed_%s",exporter=~%q}[%ds]))`, itemMetrics, q.ExporterRegex, rangeSecs),
			func(st *ExporterStage, v float64) { st.EnqueueFailed = int64(math.Round(v)) }},
		{fmt.Sprintf(`max by (exporter) (otelcol_exporter_queue_size{exporter=~%q})`, q.ExporterRegex),
			func(st *ExporterStage, v float64) { st.QueueSize = v }},
		{fmt.Sprintf(`max by (exporter) (otelcol_exporter_queue_capacity{exporter=~%q})`, q.ExporterRegex),
			func(st *ExporterStage, v float64) { st.QueueCapacity = v }},
	}
	for _, c := range counters {
		values, ok := s.vmSumBy(ctx, c.query, "exporter", q.To)
		if !ok {
			continue // VM down or query failed: leave zeros
		}
		for name, v := range values {
			st := flow.Exporters[name]
			c.set(&st, v)
			flow.Exporters[name] = st
		}
	}

	flow.ExporterNames = make([]string, 0, len(flow.Exporters))
	for name := range flow.Exporters {
		flow.ExporterNames = append(flow.ExporterNames, name)
	}
	sort.Strings(flow.ExporterNames)
	return flow, nil
}

// vmSumBy runs an instant PromQL query and returns the value per `by` label.
// ok=false on any failure — callers degrade to zeros.
func (s *Service) vmSumBy(ctx context.Context, query, label string, at time.Time) (map[string]float64, bool) {
	u := s.stores.VM("") + "/api/v1/query?" + url.Values{
		"query": {query},
		"time":  {strconv.FormatInt(at.Unix(), 10)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, false
	}
	resp, err := s.httpc.Do(req)
	if err != nil {
		s.log.Warn("stage stats: victoriametrics unreachable", "err", err)
		return nil, false
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		s.log.Warn("stage stats: victoriametrics query failed", "status", resp.StatusCode, "query", query)
		return nil, false
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != "success" {
		s.log.Warn("stage stats: cannot parse victoriametrics response", "err", err)
		return nil, false
	}
	out := make(map[string]float64, len(body.Data.Result))
	for _, r := range body.Data.Result {
		name := r.Metric[label]
		if name == "" {
			continue
		}
		if str, ok := r.Value[1].(string); ok {
			if v, err := strconv.ParseFloat(str, 64); err == nil {
				out[name] = v
			}
		}
	}
	return out, true
}
