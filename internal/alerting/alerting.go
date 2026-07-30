// Package alerting evaluates metric-threshold alert rules on a periodic tick
// and fires the referenced notification channels when a rule crosses its
// threshold. It runs on the singleton (OpAMP) tier alongside the retention
// sweep and webhook dispatcher. Firing state is tracked in memory: a rule
// notifies once when it enters breach and once when it clears (a restart may
// re-notify an already-breaching rule, which is acceptable).
package alerting

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/webhooks"
)

// CH is the ClickHouse read subset the evaluator needs.
type CH interface {
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
}

// MetricSource computes a rule's metric per tenant (client id) since a time.
// The evaluator depends on this seam so its transition logic is testable
// without a live ClickHouse.
type MetricSource interface {
	Metric(ctx context.Context, metric string, since time.Time) (map[string]float64, error)
}

// chSource is the ClickHouse-backed MetricSource.
type chSource struct{ ch CH }

// NewClickHouseSource builds a MetricSource over a ClickHouse connection.
func NewClickHouseSource(ch CH) MetricSource { return &chSource{ch: ch} }

// Store is the persistence subset the evaluator needs.
type Store interface {
	ListEnabledAlertRules(ctx context.Context) ([]store.AlertRule, error)
	ListCustomerRefs(ctx context.Context) ([]store.CustomerRef, error)
	ListWebhooks(ctx context.Context) ([]store.Webhook, error)
}

// Notifier delivers an alert payload to an explicit set of channels.
type Notifier interface {
	SendToChannels(ctx context.Context, channels []store.Webhook, p webhooks.Payload)
}

// PromQLSource evaluates an instant PromQL query against the metrics store
// (VictoriaMetrics). It returns the single scalar value and whether the query
// produced a result (empty result = no data, not a breach). Used for
// cluster/infra-wide alert rules (metric == promql). May be nil when no metrics
// store is configured — such rules are then skipped.
type PromQLSource interface {
	Query(ctx context.Context, query string) (value float64, ok bool, err error)
}

// Service evaluates alert rules on an interval.
type Service struct {
	source   MetricSource
	promql   PromQLSource
	store    Store
	notifier Notifier
	interval time.Duration
	log      *slog.Logger

	mu     sync.Mutex
	firing map[string]bool // ruleID|customerID -> currently breaching
}

// New wires the evaluator. interval is the tick period (e.g. 60s). promql may
// be nil (PromQL rules are then skipped with a warning).
func New(source MetricSource, promql PromQLSource, st Store, notifier Notifier, interval time.Duration, log *slog.Logger) *Service {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Service{source: source, promql: promql, store: st, notifier: notifier, interval: interval, log: log, firing: map[string]bool{}}
}

// Run evaluates once shortly after startup, then every interval, until ctx is
// cancelled.
func (s *Service) Run(ctx context.Context) {
	select {
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
		return
	}
	s.evalOnce(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evalOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) evalOnce(ctx context.Context) {
	if err := s.Evaluate(ctx, time.Now()); err != nil {
		s.log.Error("alerting: evaluation failed", "err", err)
	}
}

// Evaluate runs one full evaluation pass at time now. Exported for testing.
func (s *Service) Evaluate(ctx context.Context, now time.Time) error {
	rules, err := s.store.ListEnabledAlertRules(ctx)
	if err != nil {
		return fmt.Errorf("list rules: %w", err)
	}
	if len(rules) == 0 {
		return nil
	}
	refs, err := s.store.ListCustomerRefs(ctx)
	if err != nil {
		return fmt.Errorf("list customer refs: %w", err)
	}
	byClientID := make(map[string]store.CustomerRef, len(refs))
	byID := make(map[uuid.UUID]store.CustomerRef, len(refs))
	for _, r := range refs {
		byClientID[r.ClientID] = r
		byID[r.ID] = r
	}
	hooks, err := s.store.ListWebhooks(ctx)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}
	byHookID := make(map[uuid.UUID]store.Webhook, len(hooks))
	for _, h := range hooks {
		byHookID[h.ID] = h
	}

	for _, rule := range rules {
		if err := s.evalRule(ctx, now, rule, refs, byClientID, byID, byHookID); err != nil {
			s.log.Error("alerting: rule evaluation failed", "rule", rule.ID, "name", rule.Name, "err", err)
		}
	}
	return nil
}

func (s *Service) evalRule(ctx context.Context, now time.Time, rule store.AlertRule, refs []store.CustomerRef,
	byClientID map[string]store.CustomerRef, byID map[uuid.UUID]store.CustomerRef, byHookID map[uuid.UUID]store.Webhook) error {

	if rule.Metric == store.AlertMetricPromQL {
		return s.evalPromQL(ctx, now, rule, byHookID)
	}

	since := now.Add(-time.Duration(rule.WindowSeconds) * time.Second).UTC()
	perTenant, err := s.source.Metric(ctx, rule.Metric, since)
	if err != nil {
		return err
	}

	// In-scope customers: a single pinned customer, or all of them.
	var scope []store.CustomerRef
	if rule.CustomerID != nil {
		if ref, ok := byID[*rule.CustomerID]; ok {
			scope = []store.CustomerRef{ref}
		}
	} else {
		scope = refs
	}

	channels := s.channelsFor(rule, byHookID)
	for _, ref := range scope {
		value := perTenant[ref.ClientID] // missing tenant = 0 (e.g. ingest stopped)
		breached := breach(rule.Comparison, value, rule.Threshold)
		key := rule.ID.String() + "|" + ref.ID.String()

		s.mu.Lock()
		was := s.firing[key]
		s.firing[key] = breached
		s.mu.Unlock()

		if breached == was {
			continue // no transition
		}
		event := webhooks.AlertResolved
		if breached {
			event = webhooks.AlertFiring
		}
		s.log.Info("alerting: transition", "rule", rule.Name, "customer", ref.Name, "metric", rule.Metric, "value", value, "threshold", rule.Threshold, "firing", breached)
		if len(channels) > 0 {
			s.notifier.SendToChannels(ctx, channels, webhooks.Payload{
				Event:      event,
				OccurredAt: now.UTC(),
				Detail: map[string]any{
					"rule":          rule.Name,
					"customer":      ref.Name,
					"metric":        rule.Metric,
					"comparison":    rule.Comparison,
					"threshold":     rule.Threshold,
					"observed":      value,
					"windowSeconds": rule.WindowSeconds,
				},
			})
		}
	}
	return nil
}

// evalPromQL handles a cluster/infra-wide PromQL rule: run the query against
// the metrics store, compare the scalar result to the threshold, and fire once
// on transition. An empty/absent result is treated as "no data" (not a breach)
// so a transiently-unavailable metric doesn't flap.
func (s *Service) evalPromQL(ctx context.Context, now time.Time, rule store.AlertRule, byHookID map[uuid.UUID]store.Webhook) error {
	if s.promql == nil {
		s.log.Warn("alerting: promql rule skipped, no metrics store configured", "rule", rule.Name)
		return nil
	}
	value, ok, err := s.promql.Query(ctx, rule.Query)
	if err != nil {
		return err
	}
	breached := ok && breach(rule.Comparison, value, rule.Threshold)
	key := rule.ID.String() + "|promql"

	s.mu.Lock()
	was := s.firing[key]
	s.firing[key] = breached
	s.mu.Unlock()

	if breached == was {
		return nil // no transition
	}
	event := webhooks.AlertResolved
	if breached {
		event = webhooks.AlertFiring
	}
	s.log.Info("alerting: transition", "rule", rule.Name, "metric", "promql", "query", rule.Query, "value", value, "threshold", rule.Threshold, "firing", breached)
	channels := s.channelsFor(rule, byHookID)
	if len(channels) > 0 {
		s.notifier.SendToChannels(ctx, channels, webhooks.Payload{
			Event:      event,
			OccurredAt: now.UTC(),
			Detail: map[string]any{
				"rule":       rule.Name,
				"metric":     "promql",
				"query":      rule.Query,
				"comparison": rule.Comparison,
				"threshold":  rule.Threshold,
				"observed":   value,
			},
		})
	}
	return nil
}

func (s *Service) channelsFor(rule store.AlertRule, byHookID map[uuid.UUID]store.Webhook) []store.Webhook {
	var out []store.Webhook
	for _, id := range rule.ChannelIDs {
		if h, ok := byHookID[id]; ok {
			out = append(out, h)
		}
	}
	return out
}

// Metric computes the rule's metric per tenant (client id) since the given
// time. Tenants with no data are simply absent from the map (treated as 0).
func (c *chSource) Metric(ctx context.Context, metric string, since time.Time) (map[string]float64, error) {
	var q string
	switch metric {
	case store.AlertMetricIngestItems:
		q = `SELECT TenantId, sum(Items) AS v FROM ingest_counts_1m WHERE Minute >= ? GROUP BY TenantId`
	case store.AlertMetricErrorLogs:
		q = `SELECT TenantId, count() AS v FROM otel_logs WHERE Timestamp >= ? AND SeverityNumber >= 17 GROUP BY TenantId`
	default:
		return nil, fmt.Errorf("unknown metric %q", metric)
	}
	rows, err := c.ch.Query(ctx, q, since)
	if err != nil {
		return nil, fmt.Errorf("clickhouse %s: %w", metric, err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var tenant string
		var v uint64
		if err := rows.Scan(&tenant, &v); err != nil {
			return nil, err
		}
		out[tenant] = float64(v)
	}
	return out, rows.Err()
}

func breach(comparison string, value, threshold float64) bool {
	switch comparison {
	case store.AlertComparisonBelow:
		return value < threshold
	case store.AlertComparisonAbove:
		return value > threshold
	default:
		return false
	}
}
