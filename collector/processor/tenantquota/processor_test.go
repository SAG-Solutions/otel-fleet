// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantquota

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeAuthData mimics what the tenantauth extension puts into client.Info.
type fakeAuthData struct {
	attrs map[string]any
}

func (f *fakeAuthData) GetAttribute(name string) any { return f.attrs[name] }
func (f *fakeAuthData) GetAttributeNames() []string {
	names := make([]string, 0, len(f.attrs))
	for k := range f.attrs {
		names = append(names, k)
	}
	return names
}

// authCtx builds a context carrying tenant identity and (optionally) a limit.
// limit uses `any` so tests can exercise the tolerated non-int64 encodings.
func authCtx(tenant string, limit any) context.Context {
	attrs := map[string]any{"tenant.id": tenant}
	if limit != nil {
		attrs["rate_limit_items_per_sec"] = limit
	}
	return client.NewContext(context.Background(), client.Info{Auth: &fakeAuthData{attrs: attrs}})
}

func logsWith(n int) plog.Logs {
	ld := plog.NewLogs()
	lrs := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords()
	for i := 0; i < n; i++ {
		lrs.AppendEmpty().Body().SetStr("x")
	}
	return ld
}

func tracesWith(n int) ptrace.Traces {
	td := ptrace.NewTraces()
	spans := td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans()
	for i := 0; i < n; i++ {
		spans.AppendEmpty().SetName("op")
	}
	return td
}

// metricsWith returns metrics with one Sum metric carrying n DATA POINTS —
// the unit that must be counted (not the number of metrics, which is 1).
func metricsWith(n int) pmetric.Metrics {
	md := pmetric.NewMetrics()
	m := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("requests")
	dps := m.SetEmptySum().DataPoints()
	for i := 0; i < n; i++ {
		dps.AppendEmpty().SetIntValue(int64(i))
	}
	return md
}

// newTestQuota builds the shared quota state plus a deterministic clock and
// wires per-signal processors through the same factory path production uses.
type testQuota struct {
	shared *sharedQuotas
	set    processor.Settings
	cfg    *Config
	clk    *fakeClock
}

func newTestQuota(t *testing.T, cfg *Config) *testQuota {
	t.Helper()
	if cfg == nil {
		cfg = createDefaultConfig().(*Config)
	}
	require.NoError(t, cfg.Validate())
	shared := &sharedQuotas{quotas: map[component.ID]*quota{}}
	set := processortest.NewNopSettings(componentType)
	q, err := shared.get(set, cfg)
	require.NoError(t, err)
	clk := &fakeClock{t: time.Unix(1700000000, 0)}
	q.registry.now = clk.now
	return &testQuota{shared: shared, set: set, cfg: cfg, clk: clk}
}

func requireResourceExhausted(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "rejection must be a gRPC status error so the otlpreceiver forwards it verbatim")
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	var retryInfo *errdetails.RetryInfo
	for _, d := range st.Details() {
		if ri, ok := d.(*errdetails.RetryInfo); ok {
			retryInfo = ri
		}
	}
	require.NotNil(t, retryInfo, "RetryInfo detail marks the error retryable for OTLP gRPC exporters")
	assert.Positive(t, retryInfo.GetRetryDelay().AsDuration())
}

func TestQuotaLogsAllowThenReject(t *testing.T) {
	tq := newTestQuota(t, nil) // burst_seconds 2
	sink := new(consumertest.LogsSink)
	p, err := tq.shared.createLogs(context.Background(), tq.set, tq.cfg, sink)
	require.NoError(t, err)

	ctx := authCtx("tenant-a", int64(10)) // capacity 20

	require.NoError(t, p.ConsumeLogs(ctx, logsWith(20)))
	require.Len(t, sink.AllLogs(), 1)

	err = p.ConsumeLogs(ctx, logsWith(1))
	requireResourceExhausted(t, err)
	require.Len(t, sink.AllLogs(), 1, "rejected batch must not reach the next consumer")

	// Refill over the fake clock: 1s at 10/s.
	tq.clk.advance(time.Second)
	require.NoError(t, p.ConsumeLogs(ctx, logsWith(10)))
	require.Len(t, sink.AllLogs(), 2)
}

func TestQuotaTracesAllowThenReject(t *testing.T) {
	tq := newTestQuota(t, nil)
	sink := new(consumertest.TracesSink)
	p, err := tq.shared.createTraces(context.Background(), tq.set, tq.cfg, sink)
	require.NoError(t, err)

	ctx := authCtx("tenant-a", int64(5)) // capacity 10
	require.NoError(t, p.ConsumeTraces(ctx, tracesWith(10)))
	requireResourceExhausted(t, p.ConsumeTraces(ctx, tracesWith(1)))
	require.Len(t, sink.AllTraces(), 1)
}

func TestQuotaMetricsCountsDataPoints(t *testing.T) {
	tq := newTestQuota(t, nil)
	sink := new(consumertest.MetricsSink)
	p, err := tq.shared.createMetrics(context.Background(), tq.set, tq.cfg, sink)
	require.NoError(t, err)

	ctx := authCtx("tenant-a", int64(5)) // capacity 10

	// One metric with 10 data points fills the bucket exactly: if the
	// processor (wrongly) counted metrics, the next batch would pass too.
	require.NoError(t, p.ConsumeMetrics(ctx, metricsWith(10)))
	requireResourceExhausted(t, p.ConsumeMetrics(ctx, metricsWith(1)))
	require.Len(t, sink.AllMetrics(), 1)
}

func TestQuotaBudgetSharedAcrossSignals(t *testing.T) {
	// Logs, traces and metrics instances of the same configured component
	// must drain ONE per-tenant bucket.
	tq := newTestQuota(t, nil)
	logsSink := new(consumertest.LogsSink)
	tracesSink := new(consumertest.TracesSink)
	lp, err := tq.shared.createLogs(context.Background(), tq.set, tq.cfg, logsSink)
	require.NoError(t, err)
	tp, err := tq.shared.createTraces(context.Background(), tq.set, tq.cfg, tracesSink)
	require.NoError(t, err)

	ctx := authCtx("tenant-a", int64(10)) // capacity 20
	require.NoError(t, lp.ConsumeLogs(ctx, logsWith(20)))
	requireResourceExhausted(t, tp.ConsumeTraces(ctx, tracesWith(1)))
	assert.Empty(t, tracesSink.AllTraces())
}

func TestQuotaPerTenantIsolation(t *testing.T) {
	tq := newTestQuota(t, nil)
	sink := new(consumertest.LogsSink)
	p, err := tq.shared.createLogs(context.Background(), tq.set, tq.cfg, sink)
	require.NoError(t, err)

	ctxA := authCtx("tenant-a", int64(10))
	ctxB := authCtx("tenant-b", int64(10))

	require.NoError(t, p.ConsumeLogs(ctxA, logsWith(20)))
	requireResourceExhausted(t, p.ConsumeLogs(ctxA, logsWith(1)))

	// Tenant B is unaffected by A's exhaustion.
	for range 3 {
		require.NoError(t, p.ConsumeLogs(ctxB, logsWith(5)))
	}
	requireResourceExhausted(t, p.ConsumeLogs(ctxA, logsWith(1)))
}

func TestQuotaPassthroughWithoutLimit(t *testing.T) {
	tq := newTestQuota(t, nil)
	sink := new(consumertest.LogsSink)
	p, err := tq.shared.createLogs(context.Background(), tq.set, tq.cfg, sink)
	require.NoError(t, err)

	t.Run("limit zero", func(t *testing.T) {
		for range 5 {
			require.NoError(t, p.ConsumeLogs(authCtx("unlimited", int64(0)), logsWith(1000)))
		}
	})
	t.Run("limit attribute absent", func(t *testing.T) {
		require.NoError(t, p.ConsumeLogs(authCtx("no-limit-attr", nil), logsWith(1000)))
	})
	t.Run("no auth on context", func(t *testing.T) {
		// tenantstamp upstream drops unauthenticated data; if something still
		// arrives without auth the quota processor must not block it.
		require.NoError(t, p.ConsumeLogs(context.Background(), logsWith(1000)))
	})
	assert.Len(t, sink.AllLogs(), 7)
}

func TestQuotaBatchLargerThanBurstRejectedNotWedged(t *testing.T) {
	tq := newTestQuota(t, nil)
	sink := new(consumertest.LogsSink)
	p, err := tq.shared.createLogs(context.Background(), tq.set, tq.cfg, sink)
	require.NoError(t, err)

	ctx := authCtx("tenant-a", int64(10)) // capacity 20

	// A single batch larger than the burst capacity can never fit: rejected
	// immediately (no blocking/deadlock), counted, and nothing is consumed.
	requireResourceExhausted(t, p.ConsumeLogs(ctx, logsWith(1000)))
	assert.Empty(t, sink.AllLogs())

	// The bucket is untouched: a full burst still goes through.
	require.NoError(t, p.ConsumeLogs(ctx, logsWith(20)))
	require.Len(t, sink.AllLogs(), 1)
}

func TestLimitFromAuthEncodings(t *testing.T) {
	tests := []struct {
		name  string
		limit any
		want  int64
	}{
		{"canonical int64", int64(50), 50},
		{"int", int(7), 7},
		{"int32", int32(8), 8},
		{"uint32", uint32(9), 9},
		{"uint64", uint64(10), 10},
		{"decimal string", "42", 42},
		{"garbage string", "many", 0},
		{"nil", nil, 0},
		{"unrelated type", 1.5, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &fakeAuthData{attrs: map[string]any{"rate_limit_items_per_sec": tt.limit}}
			assert.Equal(t, tt.want, limitFromAuth(auth))
		})
	}
}

// encoded fingerprints an attribute set the same way the SDK data points do.
func encoded(kvs ...attribute.KeyValue) string {
	s := attribute.NewSet(kvs...)
	return s.Encoded(attribute.DefaultEncoder())
}

func TestQuotaTelemetry(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	set := processortest.NewNopSettings(componentType)
	set.TelemetrySettings = componenttest.NewNopTelemetrySettings()
	set.TelemetrySettings.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	shared := &sharedQuotas{quotas: map[component.ID]*quota{}}
	cfg := createDefaultConfig().(*Config)
	q, err := shared.get(set, cfg)
	require.NoError(t, err)
	q.registry.now = (&fakeClock{t: time.Unix(1700000000, 0)}).now

	p, err := shared.createLogs(context.Background(), set, cfg, consumertest.NewNop())
	require.NoError(t, err)

	ctx := authCtx("tenant-a", int64(10)) // capacity 20
	require.NoError(t, p.ConsumeLogs(ctx, logsWith(20)))
	requireResourceExhausted(t, p.ConsumeLogs(ctx, logsWith(7)))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	sums := map[string]map[string]int64{} // metric name -> attr fingerprint -> value
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			byAttr := map[string]int64{}
			for _, dp := range sum.DataPoints {
				byAttr[dp.Attributes.Encoded(attribute.DefaultEncoder())] = dp.Value
			}
			sums[m.Name] = byAttr
		}
	}

	dec := sums["otel_fleet_quota_decisions_total"]
	require.NotNil(t, dec, "otel_fleet_quota_decisions_total must be emitted")
	assert.Equal(t, int64(1), dec[encoded(
		attribute.String("decision", "allowed"), attribute.String("tenant_id", "tenant-a"))])
	assert.Equal(t, int64(1), dec[encoded(
		attribute.String("decision", "rejected"), attribute.String("tenant_id", "tenant-a"))])

	rej := sums["otel_fleet_quota_rejected_items_total"]
	require.NotNil(t, rej, "otel_fleet_quota_rejected_items_total must be emitted")
	assert.Equal(t, int64(7), rej[encoded(
		attribute.String("signal", "logs"), attribute.String("tenant_id", "tenant-a"))])
}

func TestConfigValidate(t *testing.T) {
	cfg := createDefaultConfig().(*Config)
	assert.NoError(t, cfg.Validate())
	assert.EqualValues(t, 2, cfg.BurstSeconds)
	cfg.BurstSeconds = 0
	assert.Error(t, cfg.Validate())
	cfg.BurstSeconds = -1
	assert.Error(t, cfg.Validate())
}
