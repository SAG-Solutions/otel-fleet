// Copyright The otel-fleet Authors
// SPDX-License-Identifier: Apache-2.0

package tenantstamp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processortest"
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

func authCtx(tenant, clientID, customer string) context.Context {
	return client.NewContext(context.Background(), client.Info{
		Auth: &fakeAuthData{attrs: map[string]any{
			"tenant.id":   tenant,
			"client.id":   clientID,
			"customer.id": customer,
		}},
	})
}

func spoofAttrs(attrs pcommon.Map) {
	attrs.PutStr("service.name", "victim-service")
	attrs.PutStr("tenant.id", "spoofed-tenant")
	attrs.PutStr("client.id", "spoofed-client")
	attrs.PutStr("customer.id", "spoofed-customer")
}

func assertStamped(t *testing.T, attrs pcommon.Map) {
	t.Helper()
	tenant, ok := attrs.Get("tenant.id")
	require.True(t, ok)
	assert.Equal(t, "client-1", tenant.Str())
	clientID, ok := attrs.Get("client.id")
	require.True(t, ok)
	assert.Equal(t, "client-1", clientID.Str())
	customer, ok := attrs.Get("customer.id")
	require.True(t, ok)
	assert.Equal(t, "customer-1", customer.Str())
	// Untouched attributes survive.
	svc, ok := attrs.Get("service.name")
	require.True(t, ok)
	assert.Equal(t, "victim-service", svc.Str())
}

func TestStampTraces(t *testing.T) {
	sink := new(consumertest.TracesSink)
	p, err := createTraces(context.Background(), processortest.NewNopSettings(componentType), &Config{}, sink)
	require.NoError(t, err)

	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	spoofAttrs(rs.Resource().Attributes())
	rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("op")
	// Second resource without any attributes must be stamped too.
	td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()

	require.NoError(t, p.ConsumeTraces(authCtx("client-1", "client-1", "customer-1"), td))
	require.Len(t, sink.AllTraces(), 1)
	got := sink.AllTraces()[0]
	assertStamped(t, got.ResourceSpans().At(0).Resource().Attributes())
	tenant, ok := got.ResourceSpans().At(1).Resource().Attributes().Get("tenant.id")
	require.True(t, ok)
	assert.Equal(t, "client-1", tenant.Str())
}

func TestStampLogs(t *testing.T) {
	sink := new(consumertest.LogsSink)
	p, err := createLogs(context.Background(), processortest.NewNopSettings(componentType), &Config{}, sink)
	require.NoError(t, err)

	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	spoofAttrs(rl.Resource().Attributes())
	rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("hello")

	require.NoError(t, p.ConsumeLogs(authCtx("client-1", "client-1", "customer-1"), ld))
	require.Len(t, sink.AllLogs(), 1)
	assertStamped(t, sink.AllLogs()[0].ResourceLogs().At(0).Resource().Attributes())
}

func TestStampMetrics(t *testing.T) {
	sink := new(consumertest.MetricsSink)
	p, err := createMetrics(context.Background(), processortest.NewNopSettings(componentType), &Config{}, sink)
	require.NoError(t, err)

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	spoofAttrs(rm.Resource().Attributes())
	m := rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("requests")
	m.SetEmptySum().DataPoints().AppendEmpty().SetIntValue(1)

	require.NoError(t, p.ConsumeMetrics(authCtx("client-1", "client-1", "customer-1"), md))
	require.Len(t, sink.AllMetrics(), 1)
	assertStamped(t, sink.AllMetrics()[0].ResourceMetrics().At(0).Resource().Attributes())
}

func TestSpoofedAttributesRemovedEvenWhenAuthValueEmpty(t *testing.T) {
	// customer.id is empty in the auth data: the spoofed value must still be
	// deleted, not left in place.
	sink := new(consumertest.LogsSink)
	p, err := createLogs(context.Background(), processortest.NewNopSettings(componentType), &Config{}, sink)
	require.NoError(t, err)

	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	spoofAttrs(rl.Resource().Attributes())
	rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()

	require.NoError(t, p.ConsumeLogs(authCtx("client-1", "client-1", ""), ld))
	attrs := sink.AllLogs()[0].ResourceLogs().At(0).Resource().Attributes()
	_, ok := attrs.Get("customer.id")
	assert.False(t, ok, "spoofed customer.id must be removed when auth data has none")
	tenant, ok := attrs.Get("tenant.id")
	require.True(t, ok)
	assert.Equal(t, "client-1", tenant.Str())
}

func TestDropWithoutAuth(t *testing.T) {
	settings := processortest.NewNopSettings(componentType)

	t.Run("traces", func(t *testing.T) {
		sink := new(consumertest.TracesSink)
		p, err := createTraces(context.Background(), settings, &Config{}, sink)
		require.NoError(t, err)
		td := ptrace.NewTraces()
		td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		require.NoError(t, p.ConsumeTraces(context.Background(), td), "drop must not surface an error to the receiver")
		assert.Empty(t, sink.AllTraces())
	})

	t.Run("logs", func(t *testing.T) {
		sink := new(consumertest.LogsSink)
		p, err := createLogs(context.Background(), settings, &Config{}, sink)
		require.NoError(t, err)
		ld := plog.NewLogs()
		ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		require.NoError(t, p.ConsumeLogs(context.Background(), ld))
		assert.Empty(t, sink.AllLogs())
	})

	t.Run("metrics", func(t *testing.T) {
		sink := new(consumertest.MetricsSink)
		p, err := createMetrics(context.Background(), settings, &Config{}, sink)
		require.NoError(t, err)
		md := pmetric.NewMetrics()
		md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
		require.NoError(t, p.ConsumeMetrics(context.Background(), md))
		assert.Empty(t, sink.AllMetrics())
	})
}

func TestDropWhenTenantIDMissing(t *testing.T) {
	sink := new(consumertest.LogsSink)
	p, err := createLogs(context.Background(), processortest.NewNopSettings(componentType), &Config{}, sink)
	require.NoError(t, err)

	ctx := client.NewContext(context.Background(), client.Info{
		Auth: &fakeAuthData{attrs: map[string]any{"customer.id": "customer-1"}},
	})
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	require.NoError(t, p.ConsumeLogs(ctx, ld))
	assert.Empty(t, sink.AllLogs())
}
