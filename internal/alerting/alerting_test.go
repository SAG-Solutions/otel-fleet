package alerting

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/webhooks"
)

type fakeSource struct{ vals map[string]float64 }

func (f *fakeSource) Metric(context.Context, string, time.Time) (map[string]float64, error) {
	return f.vals, nil
}

type fakeStore struct {
	rules []store.AlertRule
	refs  []store.CustomerRef
	hooks []store.Webhook
}

func (f *fakeStore) ListEnabledAlertRules(context.Context) ([]store.AlertRule, error) {
	return f.rules, nil
}
func (f *fakeStore) ListCustomerRefs(context.Context) ([]store.CustomerRef, error) { return f.refs, nil }
func (f *fakeStore) ListWebhooks(context.Context) ([]store.Webhook, error)         { return f.hooks, nil }

type capturedSend struct {
	event    string
	channels int
}

type fakeNotifier struct{ sends []capturedSend }

func (n *fakeNotifier) SendToChannels(_ context.Context, channels []store.Webhook, p webhooks.Payload) {
	n.sends = append(n.sends, capturedSend{event: p.Event, channels: len(channels)})
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestEvaluateFiresOnceThenResolves(t *testing.T) {
	chID := uuid.New()
	cust := store.CustomerRef{ID: uuid.New(), Name: "ACME", ClientID: "cust_x"}
	rule := store.AlertRule{
		ID: uuid.New(), Name: "ingest stopped", Metric: store.AlertMetricIngestItems,
		Comparison: store.AlertComparisonBelow, Threshold: 10, WindowSeconds: 300,
		ChannelIDs: []uuid.UUID{chID}, Enabled: true, // CustomerID nil = all customers
	}
	src := &fakeSource{}
	st := &fakeStore{
		rules: []store.AlertRule{rule},
		refs:  []store.CustomerRef{cust},
		hooks: []store.Webhook{{ID: chID, Type: store.WebhookTypeSlack, Enabled: true}},
	}
	notifier := &fakeNotifier{}
	svc := New(src, nil, st, notifier, time.Minute, discardLog())
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	// Breach: 0 items < 10 → fire AlertFiring once.
	src.vals = map[string]float64{"cust_x": 0}
	must(t, svc.Evaluate(ctx, now))
	if len(notifier.sends) != 1 || notifier.sends[0].event != webhooks.AlertFiring || notifier.sends[0].channels != 1 {
		t.Fatalf("expected one AlertFiring to 1 channel, got %+v", notifier.sends)
	}

	// Still breaching → no new notification (no transition).
	must(t, svc.Evaluate(ctx, now))
	if len(notifier.sends) != 1 {
		t.Fatalf("re-fired while still breaching: %+v", notifier.sends)
	}

	// Recovered: 100 items ≥ 10 → fire AlertResolved once.
	src.vals = map[string]float64{"cust_x": 100}
	must(t, svc.Evaluate(ctx, now))
	if len(notifier.sends) != 2 || notifier.sends[1].event != webhooks.AlertResolved {
		t.Fatalf("expected AlertResolved, got %+v", notifier.sends)
	}

	// Still healthy → no notification.
	must(t, svc.Evaluate(ctx, now))
	if len(notifier.sends) != 2 {
		t.Fatalf("spurious notification while healthy: %+v", notifier.sends)
	}
}

func TestEvaluateErrorLogsAboveScopedCustomer(t *testing.T) {
	chID := uuid.New()
	acme := store.CustomerRef{ID: uuid.New(), Name: "ACME", ClientID: "cust_acme"}
	globex := store.CustomerRef{ID: uuid.New(), Name: "Globex", ClientID: "cust_globex"}
	rule := store.AlertRule{
		ID: uuid.New(), Name: "errors", Metric: store.AlertMetricErrorLogs,
		Comparison: store.AlertComparisonAbove, Threshold: 50, WindowSeconds: 300,
		CustomerID: &acme.ID, ChannelIDs: []uuid.UUID{chID}, Enabled: true,
	}
	src := &fakeSource{vals: map[string]float64{"cust_acme": 120, "cust_globex": 999}}
	st := &fakeStore{
		rules: []store.AlertRule{rule},
		refs:  []store.CustomerRef{acme, globex},
		hooks: []store.Webhook{{ID: chID, Enabled: true}},
	}
	notifier := &fakeNotifier{}
	svc := New(src, nil, st, notifier, time.Minute, discardLog())

	must(t, svc.Evaluate(context.Background(), time.Unix(1_700_000_000, 0)))
	// Rule is scoped to ACME only → exactly one firing, despite Globex also breaching.
	if len(notifier.sends) != 1 || notifier.sends[0].event != webhooks.AlertFiring {
		t.Fatalf("scoped rule should fire once for ACME, got %+v", notifier.sends)
	}
}

type fakePromQL struct {
	val float64
	ok  bool
}

func (f *fakePromQL) Query(context.Context, string) (float64, bool, error) { return f.val, f.ok, nil }

func TestEvaluatePromQLRuleFiresOnceThenResolves(t *testing.T) {
	chID := uuid.New()
	rule := store.AlertRule{
		ID: uuid.New(), Name: "node cpu", Metric: store.AlertMetricPromQL,
		Query: `avg(node_cpu_usage)`, Comparison: store.AlertComparisonAbove, Threshold: 0.8,
		ChannelIDs: []uuid.UUID{chID}, Enabled: true, // promql rules are cluster-wide (no customer)
	}
	pq := &fakePromQL{}
	st := &fakeStore{
		rules: []store.AlertRule{rule},
		refs:  []store.CustomerRef{{ID: uuid.New(), Name: "ACME", ClientID: "cust_x"}},
		hooks: []store.Webhook{{ID: chID, Enabled: true}},
	}
	notifier := &fakeNotifier{}
	svc := New(nil, pq, st, notifier, time.Minute, discardLog())
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)

	// Breach: 0.9 > 0.8 → fire once.
	pq.val, pq.ok = 0.9, true
	must(t, svc.Evaluate(ctx, now))
	if len(notifier.sends) != 1 || notifier.sends[0].event != webhooks.AlertFiring {
		t.Fatalf("expected one AlertFiring, got %+v", notifier.sends)
	}
	// Still breaching → no re-fire.
	must(t, svc.Evaluate(ctx, now))
	if len(notifier.sends) != 1 {
		t.Fatalf("re-fired while still breaching: %+v", notifier.sends)
	}
	// Recovered → resolve.
	pq.val = 0.5
	must(t, svc.Evaluate(ctx, now))
	if len(notifier.sends) != 2 || notifier.sends[1].event != webhooks.AlertResolved {
		t.Fatalf("expected AlertResolved, got %+v", notifier.sends)
	}
	// No data (query returns nothing) must not be treated as a breach.
	pq.val, pq.ok = 0, false
	must(t, svc.Evaluate(ctx, now))
	if len(notifier.sends) != 2 {
		t.Fatalf("no-data must not fire: %+v", notifier.sends)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
}
