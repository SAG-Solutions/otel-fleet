// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantquota // import "github.com/sag-solutions/otel-fleet/collector/processor/tenantquota"

import (
	"context"
	"sync"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"
)

var componentType = component.MustNewType("tenantquota")

const defaultBurstSeconds = 2

// capabilities: the processor only inspects data (counts items); it never
// mutates it.
var capabilities = consumer.Capabilities{MutatesData: false}

// NewFactory creates the factory for the tenantquota processor.
//
// The same configured component (same component.ID) is instantiated once per
// signal by the service; all instances must share ONE token-bucket registry
// so a tenant's items/sec budget spans logs, traces and metrics together.
// The factory keeps a per-ID map of shared quota state (memorylimiter uses
// the same pattern).
func NewFactory() processor.Factory {
	shared := &sharedQuotas{quotas: map[component.ID]*quota{}}
	return processor.NewFactory(
		componentType,
		createDefaultConfig,
		processor.WithTraces(shared.createTraces, component.StabilityLevelBeta),
		processor.WithLogs(shared.createLogs, component.StabilityLevelBeta),
		processor.WithMetrics(shared.createMetrics, component.StabilityLevelBeta),
	)
}

func createDefaultConfig() component.Config {
	return &Config{BurstSeconds: defaultBurstSeconds}
}

type sharedQuotas struct {
	mu     sync.Mutex
	quotas map[component.ID]*quota
}

func (s *sharedQuotas) get(set processor.Settings, cfg *Config) (*quota, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reuse the shared state across the per-signal instances of one configured
	// component; rebuild it if a config reload changed burst_seconds.
	if q, ok := s.quotas[set.ID]; ok && q.registry.burstSeconds == cfg.BurstSeconds {
		return q, nil
	}
	q, err := newQuota(cfg, set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	s.quotas[set.ID] = q
	return q, nil
}

func (s *sharedQuotas) createLogs(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Logs) (processor.Logs, error) {
	q, err := s.get(set, cfg.(*Config))
	if err != nil {
		return nil, err
	}
	return processorhelper.NewLogs(ctx, set, cfg, next, q.processLogs,
		processorhelper.WithCapabilities(capabilities))
}

func (s *sharedQuotas) createTraces(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Traces) (processor.Traces, error) {
	q, err := s.get(set, cfg.(*Config))
	if err != nil {
		return nil, err
	}
	return processorhelper.NewTraces(ctx, set, cfg, next, q.processTraces,
		processorhelper.WithCapabilities(capabilities))
}

func (s *sharedQuotas) createMetrics(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Metrics) (processor.Metrics, error) {
	q, err := s.get(set, cfg.(*Config))
	if err != nil {
		return nil, err
	}
	return processorhelper.NewMetrics(ctx, set, cfg, next, q.processMetrics,
		processorhelper.WithCapabilities(capabilities))
}
