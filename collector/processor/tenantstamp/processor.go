// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

// Package tenantstamp stamps the authenticated tenant identity
// (tenant.id/client.id/customer.id, as resolved by the tenantauth extension)
// onto every Resource of traces, logs and metrics. Any pre-existing values for
// those attributes are removed first, so clients cannot spoof another tenant.
// Batches without authentication data are dropped.
package tenantstamp // import "github.com/sag-solutions/otel-fleet/collector/processor/tenantstamp"

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processorhelper"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// Resource attribute names written by this processor. They mirror the
// attribute names the tenantauth extension exposes via client.AuthData.
const (
	attrTenantID   = "tenant.id"
	attrClientID   = "client.id"
	attrCustomerID = "customer.id"
)

// dropWarnInterval throttles the "dropping unauthenticated data" warning.
const dropWarnInterval = 10 * time.Second

type identity struct {
	tenantID   string
	clientID   string
	customerID string
}

type stamper struct {
	logger       *zap.Logger
	droppedTotal metric.Int64Counter

	// lastWarnNanos is the unix-nano time of the last drop warning (throttle).
	lastWarnNanos atomic.Int64
}

func newStamper(telemetry component.TelemetrySettings) (*stamper, error) {
	meter := telemetry.MeterProvider.Meter("github.com/sag-solutions/otel-fleet/collector/processor/tenantstamp")
	droppedTotal, err := meter.Int64Counter(
		"otel_fleet_tenantstamp_dropped_batches_total",
		metric.WithDescription("Batches dropped because no authenticated tenant identity was present on the context."),
		metric.WithUnit("{batch}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating otel_fleet_tenantstamp_dropped_batches_total counter: %w", err)
	}
	return &stamper{logger: telemetry.Logger, droppedTotal: droppedTotal}, nil
}

// identityFromContext extracts the tenant identity resolved by the server
// authenticator. Second return value is false when the batch must be dropped.
func (s *stamper) identityFromContext(ctx context.Context, signal string) (identity, bool) {
	auth := client.FromContext(ctx).Auth
	if auth == nil {
		s.drop(ctx, signal, "no auth data on context")
		return identity{}, false
	}
	id := identity{
		tenantID:   attrString(auth, attrTenantID),
		clientID:   attrString(auth, attrClientID),
		customerID: attrString(auth, attrCustomerID),
	}
	if id.tenantID == "" {
		s.drop(ctx, signal, "auth data has no tenant.id")
		return identity{}, false
	}
	return id, true
}

func attrString(auth client.AuthData, name string) string {
	v, _ := auth.GetAttribute(name).(string)
	return v
}

func (s *stamper) drop(ctx context.Context, signal, reason string) {
	s.droppedTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("signal", signal)))
	now := time.Now().UnixNano()
	last := s.lastWarnNanos.Load()
	if now-last >= dropWarnInterval.Nanoseconds() && s.lastWarnNanos.CompareAndSwap(last, now) {
		s.logger.Warn("Dropping unauthenticated telemetry; receivers feeding tenantstamp must use the tenantauth authenticator",
			zap.String("signal", signal), zap.String("reason", reason))
	}
}

// stamp overwrites the tenant identity attributes on a resource. Pre-existing
// values are removed first so clients cannot spoof another tenant even for
// attributes we would not re-set (e.g. empty customer.id).
func stamp(res pcommon.Resource, id identity) {
	attrs := res.Attributes()
	attrs.Remove(attrTenantID)
	attrs.Remove(attrClientID)
	attrs.Remove(attrCustomerID)
	attrs.PutStr(attrTenantID, id.tenantID)
	if id.clientID != "" {
		attrs.PutStr(attrClientID, id.clientID)
	}
	if id.customerID != "" {
		attrs.PutStr(attrCustomerID, id.customerID)
	}
}

func (s *stamper) processTraces(ctx context.Context, td ptrace.Traces) (ptrace.Traces, error) {
	id, ok := s.identityFromContext(ctx, "traces")
	if !ok {
		return td, processorhelper.ErrSkipProcessingData
	}
	rss := td.ResourceSpans()
	for i := 0; i < rss.Len(); i++ {
		stamp(rss.At(i).Resource(), id)
	}
	return td, nil
}

func (s *stamper) processLogs(ctx context.Context, ld plog.Logs) (plog.Logs, error) {
	id, ok := s.identityFromContext(ctx, "logs")
	if !ok {
		return ld, processorhelper.ErrSkipProcessingData
	}
	rls := ld.ResourceLogs()
	for i := 0; i < rls.Len(); i++ {
		stamp(rls.At(i).Resource(), id)
	}
	return ld, nil
}

func (s *stamper) processMetrics(ctx context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	id, ok := s.identityFromContext(ctx, "metrics")
	if !ok {
		return md, processorhelper.ErrSkipProcessingData
	}
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		stamp(rms.At(i).Resource(), id)
	}
	return md, nil
}
