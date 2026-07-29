// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantstamp // import "github.com/sag-solutions/otel-fleet/collector/processor/tenantstamp"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processorhelper"
)

var componentType = component.MustNewType("tenantstamp")

// capabilities: the processor rewrites resource attributes in place.
var capabilities = consumer.Capabilities{MutatesData: true}

// NewFactory creates the factory for the tenantstamp processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		componentType,
		func() component.Config { return &Config{} },
		processor.WithTraces(createTraces, component.StabilityLevelBeta),
		processor.WithLogs(createLogs, component.StabilityLevelBeta),
		processor.WithMetrics(createMetrics, component.StabilityLevelBeta),
	)
}

func createTraces(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Traces) (processor.Traces, error) {
	s, err := newStamper(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	return processorhelper.NewTraces(ctx, set, cfg, next, s.processTraces,
		processorhelper.WithCapabilities(capabilities))
}

func createLogs(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Logs) (processor.Logs, error) {
	s, err := newStamper(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	return processorhelper.NewLogs(ctx, set, cfg, next, s.processLogs,
		processorhelper.WithCapabilities(capabilities))
}

func createMetrics(ctx context.Context, set processor.Settings, cfg component.Config, next consumer.Metrics) (processor.Metrics, error) {
	s, err := newStamper(set.TelemetrySettings)
	if err != nil {
		return nil, err
	}
	return processorhelper.NewMetrics(ctx, set, cfg, next, s.processMetrics,
		processorhelper.WithCapabilities(capabilities))
}
