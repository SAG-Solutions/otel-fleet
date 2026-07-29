// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

// Package tenantquota enforces the per-tenant ingest quota resolved by the
// tenantauth extension (client.AuthData attribute "rate_limit_items_per_sec",
// an int64; 0/absent = unlimited) with a per-tenant token bucket shared
// across the logs, traces and metrics pipelines.
//
// Items are counted as log records, spans and metric DATA POINTS. A batch
// that does not fit is rejected as a whole (nothing is consumed from the
// bucket) with a retryable gRPC RESOURCE_EXHAUSTED status carrying RetryInfo;
// the otlpreceiver surfaces that to clients unchanged on gRPC and as
// HTTP 429 Too Many Requests on OTLP/HTTP.
//
// Placement: after tenantstamp (which requires the per-request auth context
// and drops unauthenticated data) and before batch (which discards that
// context and would also decouple the client from the rejection).
package tenantquota // import "github.com/sag-solutions/otel-fleet/collector/processor/tenantquota"

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Attribute names read from client.AuthData (written by the tenantauth
// extension; see collector/extension/tenantauth/authdata.go).
const (
	attrTenantID             = "tenant.id"
	attrRateLimitItemsPerSec = "rate_limit_items_per_sec"
)

// quota is the shared state behind the logs/traces/metrics processor
// instances of one configured tenantquota component: a single token-bucket
// registry, so the tenant's items/sec budget spans all three signals.
type quota struct {
	registry *registry

	decisionsTotal     metric.Int64Counter
	rejectedItemsTotal metric.Int64Counter
}

func newQuota(cfg *Config, telemetry component.TelemetrySettings) (*quota, error) {
	meter := telemetry.MeterProvider.Meter("github.com/sag-solutions/otel-fleet/collector/processor/tenantquota")
	decisionsTotal, err := meter.Int64Counter(
		"otel_fleet_quota_decisions_total",
		metric.WithDescription("Quota admission decisions per tenant, by decision (allowed|rejected). Batches without a limit are not counted."),
		metric.WithUnit("{batch}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating otel_fleet_quota_decisions_total counter: %w", err)
	}
	rejectedItemsTotal, err := meter.Int64Counter(
		"otel_fleet_quota_rejected_items_total",
		metric.WithDescription("Items (log records, spans, metric data points) rejected by the per-tenant ingest quota."),
		metric.WithUnit("{item}"),
	)
	if err != nil {
		return nil, fmt.Errorf("creating otel_fleet_quota_rejected_items_total counter: %w", err)
	}
	return &quota{
		registry:           newRegistry(cfg.BurstSeconds),
		decisionsTotal:     decisionsTotal,
		rejectedItemsTotal: rejectedItemsTotal,
	}, nil
}

// admit applies the tenant's quota to a batch of n items. It returns nil when
// the batch may pass (no limit configured, or tokens consumed) and a
// retryable RESOURCE_EXHAUSTED status error when the whole batch is rejected.
func (q *quota) admit(ctx context.Context, signal string, n int) error {
	auth := client.FromContext(ctx).Auth
	if auth == nil {
		// tenantstamp (upstream) drops unauthenticated data; anything that
		// still gets here without auth context passes through unlimited.
		return nil
	}
	tenant, _ := auth.GetAttribute(attrTenantID).(string)
	limit := limitFromAuth(auth)
	if tenant == "" || limit <= 0 || n == 0 {
		return nil // unlimited tenant or empty batch
	}

	allowed, retryAfter := q.registry.take(tenant, limit, n)
	if allowed {
		q.decisionsTotal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("tenant_id", tenant), attribute.String("decision", "allowed")))
		return nil
	}
	q.decisionsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("tenant_id", tenant), attribute.String("decision", "rejected")))
	q.rejectedItemsTotal.Add(ctx, int64(n), metric.WithAttributes(
		attribute.String("tenant_id", tenant), attribute.String("signal", signal)))

	// NOT consumererror.NewPermanent: the otlpreceiver forwards this gRPC
	// status as-is (RESOURCE_EXHAUSTED) and maps it to HTTP 429; RetryInfo
	// marks it retryable/throttled for OTLP gRPC exporters.
	st := status.New(codes.ResourceExhausted,
		fmt.Sprintf("tenant %q exceeded its ingest quota of %d items/sec (batch of %d %s items rejected)", tenant, limit, n, signal))
	if detailed, detErr := st.WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(retryAfter)}); detErr == nil {
		st = detailed
	}
	return st.Err()
}

// limitFromAuth reads the per-tenant limit from the auth data. The canonical
// type is int64 (tenantauth); other integer widths and decimal strings are
// accepted defensively. <=0 means unlimited.
func limitFromAuth(auth client.AuthData) int64 {
	switch v := auth.GetAttribute(attrRateLimitItemsPerSec).(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		if v > uint64(1)<<62 {
			return 1 << 62
		}
		return int64(v)
	case string:
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return 0
}

func (q *quota) processLogs(ctx context.Context, ld plog.Logs) (plog.Logs, error) {
	if err := q.admit(ctx, "logs", ld.LogRecordCount()); err != nil {
		return ld, err
	}
	return ld, nil
}

func (q *quota) processTraces(ctx context.Context, td ptrace.Traces) (ptrace.Traces, error) {
	if err := q.admit(ctx, "traces", td.SpanCount()); err != nil {
		return td, err
	}
	return td, nil
}

func (q *quota) processMetrics(ctx context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	if err := q.admit(ctx, "metrics", md.DataPointCount()); err != nil {
		return md, err
	}
	return md, nil
}
