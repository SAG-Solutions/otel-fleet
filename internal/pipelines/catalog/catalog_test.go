package catalog

import (
	"encoding/json"
	"testing"
)

// Every catalog schema must compile and its defaults must satisfy it — a
// broken entry would otherwise only surface when a user opens the builder.
func TestCatalogSchemasCompileAndDefaultsValidate(t *testing.T) {
	comps := All()
	if len(comps) < 10 {
		t.Fatalf("catalog unexpectedly small: %d components", len(comps))
	}
	for _, c := range comps {
		t.Run(c.Kind+"/"+c.Type, func(t *testing.T) {
			if _, err := c.CompiledSchema(); err != nil {
				t.Fatalf("schema does not compile: %v", err)
			}
			if err := c.ValidateConfig(c.Defaults()); err != nil {
				t.Fatalf("defaults do not validate against own schema: %v", err)
			}
			if c.DisplayName == "" || c.Description == "" {
				t.Error("displayName and description are required")
			}
		})
	}
}

// Every preset must reference a real exporter and its pre-filled defaults must
// validate against that exporter's schema — otherwise "add preset" would drop a
// broken node into the builder.
func TestPresetsReferenceValidExporters(t *testing.T) {
	presets := Presets()
	if len(presets) < 8 {
		t.Fatalf("presets unexpectedly few: %d", len(presets))
	}
	seen := map[string]bool{}
	for _, p := range presets {
		t.Run(p.ID, func(t *testing.T) {
			if p.ID == "" || p.DisplayName == "" || p.Description == "" || p.Icon == "" {
				t.Error("id, displayName, description and icon are required")
			}
			if seen[p.ID] {
				t.Errorf("duplicate preset id %q", p.ID)
			}
			seen[p.ID] = true
			exp, ok := Lookup(KindExporter, p.ExporterType)
			if !ok {
				t.Fatalf("preset references unknown exporter %q", p.ExporterType)
			}
			var cfg map[string]any
			if err := json.Unmarshal([]byte(p.DefaultsJSON), &cfg); err != nil {
				t.Fatalf("defaults are not valid JSON: %v", err)
			}
			if err := exp.ValidateConfig(cfg); err != nil {
				t.Fatalf("preset defaults do not validate against %s schema: %v", p.ExporterType, err)
			}
		})
	}
}

func TestLookup(t *testing.T) {
	if _, ok := Lookup(KindExporter, "otlphttp"); !ok {
		t.Error("otlphttp exporter missing from catalog")
	}
	if _, ok := Lookup(KindProcessor, "batch"); !ok {
		t.Error("batch processor missing from catalog")
	}
	if _, ok := Lookup(KindExporter, "batch"); ok {
		t.Error("kind mismatch must not resolve")
	}
}
