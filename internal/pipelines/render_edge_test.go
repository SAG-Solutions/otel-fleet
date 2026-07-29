package pipelines

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// distroBin is where the real collector distro lands after `make build`
// (relative to this package); tests using it skip when absent (CI).
const distroBin = "../../collector/dist/otel-fleet-collector"

func TestRenderEdgeConfigGolden(t *testing.T) {
	got, err := RenderEdgeConfig(samplePipelines())
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "edge_two_pipelines.golden.yaml", got)

	// Structural invariants the golden file must encode.
	for _, want := range []string{
		"logs/acme-corp__backup",
		"traces/acme-corp__backup",
		"logs/globex__audit",
		"otlp/acme-corp__backup__0",
		"otlphttp/globex__audit__grafana",
		"batch/acme-corp__backup__0",
		"memory_limiter",
		"health_check",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered edge config missing %q", want)
		}
	}
	// Edge configs have no routing connectors and no entry pipelines.
	if strings.Contains(got, "connectors") || strings.Contains(got, "routing/") {
		t.Error("edge config must not declare connectors")
	}
	if strings.Contains(got, "/in:") {
		t.Error("non-empty edge config must not declare entry pipelines")
	}
	// No traces pipelines for globex.
	if strings.Contains(got, "traces/globex__audit") {
		t.Error("globex audit must not have a traces pipeline")
	}
}

func TestRenderEdgeConfigDeterministic(t *testing.T) {
	a, err := RenderEdgeConfig(samplePipelines())
	if err != nil {
		t.Fatal(err)
	}
	// Reversed input order must yield identical output.
	ps := samplePipelines()
	ps[0], ps[1] = ps[1], ps[0]
	b, err := RenderEdgeConfig(ps)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("edge render is order-dependent")
	}
}

func TestRenderEdgeConfigEmpty(t *testing.T) {
	got, err := RenderEdgeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "edge_empty.golden.yaml", got)
	for _, want := range []string{"logs/in", "traces/in", "metrics/in", "debug"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty edge config missing %q", want)
		}
	}
}

func TestRenderEdgeConfigDuplicateKeyRejected(t *testing.T) {
	p := samplePipelines()[1]
	if _, err := RenderEdgeConfig([]RenderPipeline{p, p}); err == nil {
		t.Fatal("duplicate pipeline keys must be rejected")
	}
}

func TestRenderEdgeFragmentStable(t *testing.T) {
	got, err := RenderEdgeFragment(samplePipelines()[1])
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "edge_fragment_backup.golden.yaml", got)
	if strings.Contains(got, "routing/") {
		t.Error("edge fragment must consume the otlp receiver, not a routing connector")
	}
}

// TestRenderEdgeConfigValidatesWithDistro runs the real `otelcol validate`
// over both golden shapes. Skipped when the distro binary is absent (CI).
func TestRenderEdgeConfigValidatesWithDistro(t *testing.T) {
	if _, err := os.Stat(distroBin); err != nil {
		t.Skipf("collector distro binary not present at %s", distroBin)
	}
	v := NewValidator(distroBin, slog.Default())
	for name, ps := range map[string][]RenderPipeline{
		"two-pipelines": samplePipelines(),
		"empty":         nil,
	} {
		cfg, err := RenderEdgeConfig(ps)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out, err := v.ValidateRendered(context.Background(), cfg); err != nil {
			t.Errorf("%s: otelcol validate failed: %v\n%s", name, err, out)
		}
	}
}
