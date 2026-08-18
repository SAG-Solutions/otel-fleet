package billing

import (
	"testing"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/stats"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

func TestMulDivExactAndOverflowSafe(t *testing.T) {
	// 1 GiB at 1_000_000 micro/GiB (= 1.0 currency/GiB) = 1_000_000 micro.
	if got := mulDiv(1_000_000, 1<<30, 1<<30); got != 1_000_000 {
		t.Errorf("1 GiB: got %d, want 1_000_000", got)
	}
	// price×bytes overflows int64 (1e8 × 1e12 = 1e20); big.Int keeps it exact.
	// 100_000_000 micro/GiB over 1 TiB (2^40 bytes) = 100_000_000 * 1024 micro.
	if got := mulDiv(100_000_000, 1<<40, 1<<30); got != 100_000_000*1024 {
		t.Errorf("1 TiB overflow case: got %d, want %d", got, int64(100_000_000)*1024)
	}
	// Zero/negative inputs price to nothing.
	for _, c := range [][3]int64{{0, 5, 5}, {5, 0, 5}, {5, 5, 0}, {-1, 5, 5}} {
		if got := mulDiv(c[0], c[1], c[2]); got != 0 {
			t.Errorf("mulDiv%v = %d, want 0", c, got)
		}
	}
}

func TestComputeStatement(t *testing.T) {
	acme := uuid.New()
	globex := uuid.New()
	settings := store.BillingSettings{
		PricePerGiBMicro:          2_000_000, // €2.00 / GiB
		PricePerMillionItemsMicro: 500_000,   // €0.50 / 1e6 items
		Currency:                  "EUR",
	}
	costs := []stats.CustomerCost{
		{CustomerID: acme, Name: "ACME", Bytes: 1 << 30, Items: 2_000_000},  // 1 GiB, 2M items
		{CustomerID: globex, Name: "Globex", Bytes: 3 << 30, Items: 0},      // 3 GiB
	}

	st := Compute("2026-07", costs, settings, nil)

	if st.Month != "2026-07" || st.Currency != "EUR" {
		t.Fatalf("header: %+v", st)
	}
	// ACME: 1 GiB × 2_000_000 = 2_000_000; 2M items × 500_000/1e6 = 1_000_000 → 3_000_000.
	// Globex: 3 GiB × 2_000_000 = 6_000_000 → 6_000_000.
	// Sorted by total desc → Globex first.
	if len(st.Lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(st.Lines))
	}
	if st.Lines[0].Name != "Globex" || st.Lines[0].TotalMicro != 6_000_000 {
		t.Errorf("line0 = %+v", st.Lines[0])
	}
	if st.Lines[1].Name != "ACME" || st.Lines[1].TotalMicro != 3_000_000 {
		t.Errorf("line1 = %+v", st.Lines[1])
	}
	if st.Lines[1].BytesCostMicro != 2_000_000 || st.Lines[1].ItemsCostMicro != 1_000_000 {
		t.Errorf("ACME split = %+v", st.Lines[1])
	}
	if st.TotalMicro != 9_000_000 {
		t.Errorf("total = %d, want 9_000_000", st.TotalMicro)
	}
}

func TestComputeZeroPrices(t *testing.T) {
	st := Compute("2026-07", []stats.CustomerCost{{Name: "x", Bytes: 1 << 40, Items: 1e9}}, store.BillingSettings{Currency: "USD"}, nil)
	if st.TotalMicro != 0 || st.Lines[0].TotalMicro != 0 {
		t.Errorf("zero price list should bill nothing, got %+v", st)
	}
}

func TestComputeWithOverride(t *testing.T) {
	acme := uuid.New()
	globex := uuid.New()
	settings := store.BillingSettings{
		PricePerGiBMicro:          2_000_000, // €2.00 / GiB global
		PricePerMillionItemsMicro: 500_000,   // €0.50 / 1e6 items global
		Currency:                  "EUR",
	}
	costs := []stats.CustomerCost{
		{CustomerID: acme, Name: "ACME", Bytes: 1 << 30, Items: 2_000_000},
		{CustomerID: globex, Name: "Globex", Bytes: 1 << 30, Items: 2_000_000},
	}
	// ACME overrides ONLY the GiB rate to €5.00; its items rate inherits global.
	gib := int64(5_000_000)
	overrides := map[uuid.UUID]store.BillingOverride{
		acme: {CustomerID: acme, PricePerGiBMicro: &gib},
	}

	st := Compute("2026-07", costs, settings, overrides)

	byName := map[string]Line{}
	for _, l := range st.Lines {
		byName[l.Name] = l
	}
	// ACME: 1 GiB × 5_000_000 (override) + 2M items × 500_000/1e6 (global) = 6_000_000.
	if a := byName["ACME"]; a.TotalMicro != 6_000_000 || a.BytesCostMicro != 5_000_000 || a.ItemsCostMicro != 1_000_000 || !a.Overridden {
		t.Errorf("ACME override line = %+v", a)
	}
	// Globex: fully global → 2_000_000 + 1_000_000 = 3_000_000, not flagged.
	if g := byName["Globex"]; g.TotalMicro != 3_000_000 || g.Overridden {
		t.Errorf("Globex global line = %+v", g)
	}
	if st.TotalMicro != 9_000_000 {
		t.Errorf("total = %d, want 9_000_000", st.TotalMicro)
	}
	// Header still reports the GLOBAL price list, not any override.
	if st.PricePerGiBMicro != 2_000_000 {
		t.Errorf("header should carry global price, got %d", st.PricePerGiBMicro)
	}
}
