package config

import "testing"

// setEnv sets OTEL_FLEET_<key> for the test and clears it afterward.
func setEnv(t *testing.T, key, val string) {
	t.Helper()
	t.Setenv("OTEL_FLEET_"+key, val)
}

func TestRegionsDefaultSynthesized(t *testing.T) {
	// No OTEL_FLEET_REGIONS → a single "default" region built from the flat
	// ClickHouse/VM settings, and that is the default region.
	setEnv(t, "CLICKHOUSE_ADDR", "ch:9000")
	setEnv(t, "VICTORIAMETRICS_URL", "http://vm:8428")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Regions) != 1 || cfg.Regions[0].Name != "default" {
		t.Fatalf("regions = %+v, want one 'default'", cfg.Regions)
	}
	if cfg.DefaultRegion != "default" {
		t.Errorf("defaultRegion = %q, want default", cfg.DefaultRegion)
	}
	if cfg.Regions[0].ClickHouseAddr != "ch:9000" || cfg.Regions[0].VictoriaMetricsURL != "http://vm:8428" {
		t.Errorf("default region did not inherit flat store settings: %+v", cfg.Regions[0])
	}
	if !cfg.HasRegion("default") || cfg.HasRegion("eu") {
		t.Error("HasRegion wrong for synthesized default")
	}
}

func TestRegionsFromJSON(t *testing.T) {
	setEnv(t, "REGIONS", `[{"name":"eu","clickhouseAddr":"eu-ch:9000"},{"name":"us","clickhouseAddr":"us-ch:9000"}]`)
	setEnv(t, "DEFAULT_REGION", "us")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.RegionNames(); len(got) != 2 || got[0] != "eu" || got[1] != "us" {
		t.Fatalf("RegionNames = %v, want [eu us]", got)
	}
	if cfg.DefaultRegion != "us" {
		t.Errorf("defaultRegion = %q, want us", cfg.DefaultRegion)
	}
	if !cfg.HasRegion("eu") || !cfg.HasRegion("us") || cfg.HasRegion("ap") {
		t.Error("HasRegion wrong")
	}
}

func TestRegionsDefaultRegionMustExist(t *testing.T) {
	setEnv(t, "REGIONS", `[{"name":"eu"}]`)
	setEnv(t, "DEFAULT_REGION", "us")
	if _, err := Load(); err == nil {
		t.Error("expected error when DEFAULT_REGION is not a configured region")
	}
}

func TestRegionsRejectDuplicateAndEmptyNames(t *testing.T) {
	setEnv(t, "REGIONS", `[{"name":"eu"},{"name":"eu"}]`)
	if _, err := Load(); err == nil {
		t.Error("expected error for duplicate region names")
	}
	setEnv(t, "REGIONS", `[{"name":""}]`)
	if _, err := Load(); err == nil {
		t.Error("expected error for empty region name")
	}
}

func TestRegionsRejectBadJSON(t *testing.T) {
	setEnv(t, "REGIONS", `not json`)
	if _, err := Load(); err == nil {
		t.Error("expected error for invalid REGIONS JSON")
	}
}
