package pipelines

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

// classFilterStore records how ListActivePipelines is called; the embedded
// nil Store panics on any other method (nothing else may be hit).
type classFilterStore struct {
	store.Store
	calls []struct {
		class    string
		customer *uuid.UUID
	}
	pipelines []store.ActivePipeline
}

func (f *classFilterStore) ListActivePipelines(_ context.Context, targetClass string, customerID *uuid.UUID) ([]store.ActivePipeline, error) {
	f.calls = append(f.calls, struct {
		class    string
		customer *uuid.UUID
	}{targetClass, customerID})
	return f.pipelines, nil
}

func activePipeline(t *testing.T, name, slug string) store.ActivePipeline {
	t.Helper()
	g, err := MarshalGraph(Graph{
		Signals:   []string{"logs"},
		Exporters: []Node{{Type: "debug", Config: map[string]any{}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage = g
	return store.ActivePipeline{
		PipelineID:   uuid.New(),
		PipelineName: name,
		CustomerID:   uuid.New(),
		CustomerSlug: slug,
		ClientID:     "cust_test1234",
		Graph:        raw,
	}
}

// TestRenderCurrentFiltersForwardingClass guards the class-aware plumbing:
// the forwarding renderer must only ever see target_class='forwarding'.
func TestRenderCurrentFiltersForwardingClass(t *testing.T) {
	f := &classFilterStore{pipelines: []store.ActivePipeline{activePipeline(t, "fwd", "acme")}}
	svc := NewService(f, NewValidator("/nonexistent", slog.Default()), NewPublishDistributor(), nil, slog.Default())

	if _, err := svc.RenderCurrent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("want 1 store call, got %d", len(f.calls))
	}
	if f.calls[0].class != ClassForwarding || f.calls[0].customer != nil {
		t.Errorf("RenderCurrent queried (%q, %v), want (forwarding, nil)", f.calls[0].class, f.calls[0].customer)
	}
}

// TestRenderEdgeCurrentFiltersEdgeClassAndCustomer guards the edge side: only
// this customer's edge pipelines feed its agents' config.
func TestRenderEdgeCurrentFiltersEdgeClassAndCustomer(t *testing.T) {
	f := &classFilterStore{}
	svc := NewService(f, NewValidator("/nonexistent", slog.Default()), NewPublishDistributor(), nil, slog.Default())
	customerID := uuid.New()

	cfg, err := svc.RenderEdgeCurrent(context.Background(), customerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("want 1 store call, got %d", len(f.calls))
	}
	if f.calls[0].class != ClassEdge {
		t.Errorf("RenderEdgeCurrent queried class %q, want edge", f.calls[0].class)
	}
	if f.calls[0].customer == nil || *f.calls[0].customer != customerID {
		t.Errorf("RenderEdgeCurrent queried customer %v, want %s", f.calls[0].customer, customerID)
	}
	// No active edge pipelines → the empty-state config.
	if cfg == "" {
		t.Error("empty edge state must still render a valid config")
	}
}

// TestValidateDraftClassScoping: forwarding drafts validate against all
// active forwarding pipelines; edge drafts against none (placeholder
// customer, no persisted peers).
func TestValidateDraftClassScoping(t *testing.T) {
	f := &classFilterStore{}
	svc := NewService(f, NewValidator("/nonexistent", slog.Default()), NewPublishDistributor(), nil, slog.Default())
	g := Graph{Signals: []string{"logs"}, Exporters: []Node{{Type: "debug", Config: map[string]any{}}}}

	res := svc.ValidateDraft(context.Background(), ClassForwarding, g)
	if !res.Valid {
		t.Fatalf("forwarding draft invalid: %+v", res.Issues)
	}
	if len(f.calls) != 1 || f.calls[0].class != ClassForwarding {
		t.Fatalf("forwarding draft must load active forwarding pipelines, calls: %+v", f.calls)
	}

	f.calls = nil
	res = svc.ValidateDraft(context.Background(), ClassEdge, g)
	if !res.Valid {
		t.Fatalf("edge draft invalid: %+v", res.Issues)
	}
	if len(f.calls) != 0 {
		t.Fatalf("edge draft must not load peers (per-customer config), calls: %+v", f.calls)
	}
}
