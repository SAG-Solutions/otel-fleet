package retention

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func intp(v int) *int { return &v }

type fakeCH struct {
	stmts []string
	args  [][]any
	failN int // fail the first N Exec calls
	calls int
}

func (f *fakeCH) Exec(_ context.Context, query string, args ...any) error {
	f.calls++
	if f.calls <= f.failN {
		return errors.New("boom")
	}
	f.stmts = append(f.stmts, query)
	f.args = append(f.args, args)
	return nil
}

type fakeStore struct {
	customers []store.Customer
	audits    [][]audit.Entry
}

func (f *fakeStore) ListCustomers(_ context.Context, status *string) ([]store.Customer, error) {
	if status != nil {
		var out []store.Customer
		for _, c := range f.customers {
			if c.Status == *status {
				out = append(out, c)
			}
		}
		return out, nil
	}
	return f.customers, nil
}

func (f *fakeStore) WriteAuditEntries(_ context.Context, entries []audit.Entry) error {
	f.audits = append(f.audits, entries)
	return nil
}

func TestStatementsCoverAllTables(t *testing.T) {
	stmts := Statements(7)
	if len(stmts) != len(tables) {
		t.Fatalf("got %d statements, want %d (one per table)", len(stmts), len(tables))
	}
	for i, s := range stmts {
		tbl := tables[i]
		if !strings.Contains(s, "ALTER TABLE otel."+tbl.name+" DELETE") {
			t.Errorf("statement %d not an ALTER DELETE for %s: %s", i, tbl.name, s)
		}
		if !strings.Contains(s, tbl.timeCol+" < now() - INTERVAL 7 DAY") {
			t.Errorf("statement %d missing 7-day cutoff on %s: %s", i, tbl.timeCol, s)
		}
		if !strings.Contains(s, "TenantId = ?") {
			t.Errorf("statement %d must bind TenantId as a parameter: %s", i, s)
		}
		if !strings.Contains(s, "mutations_sync = 0") {
			t.Errorf("statement %d must submit async: %s", i, s)
		}
	}
}

func TestPurgeStatementsCoverAllTablesNoAgeBound(t *testing.T) {
	stmts := PurgeStatements()
	// One per telemetry table + the ingest_counts_1m usage rollup.
	if len(stmts) != len(tables)+1 {
		t.Fatalf("got %d purge statements, want %d", len(stmts), len(tables)+1)
	}
	for i, s := range stmts {
		if !strings.Contains(s, "TenantId = ?") {
			t.Errorf("purge %d must bind TenantId: %s", i, s)
		}
		if strings.Contains(s, "INTERVAL") {
			t.Errorf("purge %d must NOT have an age bound (erasure): %s", i, s)
		}
		if !strings.Contains(s, "mutations_sync = 0") {
			t.Errorf("purge %d must submit async: %s", i, s)
		}
	}
	if !strings.Contains(strings.Join(stmts, "\n"), "ingest_counts_1m") {
		t.Error("purge must include the ingest_counts_1m usage rollup")
	}
}

func TestPurgeCustomer(t *testing.T) {
	ch := &fakeCH{}
	submitted, errs := PurgeCustomer(context.Background(), ch, "cust_x")
	if submitted != len(tables)+1 || errs != 0 {
		t.Fatalf("submitted=%d errs=%d, want %d/0", submitted, errs, len(tables)+1)
	}
	for _, a := range ch.args {
		if len(a) != 1 || a[0] != "cust_x" {
			t.Fatalf("each purge must bind the client id, got %v", a)
		}
	}
	// A partial failure is counted, not fatal.
	failing := &fakeCH{failN: 2}
	sub, e := PurgeCustomer(context.Background(), failing, "cust_y")
	if e != 2 || sub != len(tables)+1-2 {
		t.Fatalf("failing purge submitted=%d errs=%d", sub, e)
	}
	// nil ch (purge disabled) is a no-op.
	if s, e := PurgeCustomer(context.Background(), nil, "cust_z"); s != 0 || e != 0 {
		t.Fatalf("nil ch should no-op, got %d/%d", s, e)
	}
}

func TestRunOnceOnlyCustomersWithOverride(t *testing.T) {
	withOverride := store.Customer{ID: uuid.New(), ClientID: "cust_a", Status: store.CustomerActive, RetentionDays: intp(3)}
	noOverride := store.Customer{ID: uuid.New(), ClientID: "cust_b", Status: store.CustomerActive}
	ch := &fakeCH{}
	st := &fakeStore{customers: []store.Customer{withOverride, noOverride}}

	New(func(string) CH { return ch }, st, 0, testLogger()).RunOnce(context.Background())

	// One mutation per table, for the override customer only.
	if len(ch.stmts) != len(tables) {
		t.Fatalf("got %d mutations, want %d (only the override customer)", len(ch.stmts), len(tables))
	}
	for _, args := range ch.args {
		if len(args) != 1 || args[0] != "cust_a" {
			t.Errorf("mutation bound %v, want [cust_a]", args)
		}
	}
	// Exactly one audit batch (retention.apply for cust_a).
	if len(st.audits) != 1 || st.audits[0][0].Action != "retention.apply" {
		t.Fatalf("audit entries = %+v, want one retention.apply", st.audits)
	}
	if st.audits[0][0].ActorType != audit.ActorSystem {
		t.Errorf("audit actor = %q, want system", st.audits[0][0].ActorType)
	}
}

func TestRunOnceNoAuditWhenAllMutationsFail(t *testing.T) {
	c := store.Customer{ID: uuid.New(), ClientID: "cust_a", Status: store.CustomerActive, RetentionDays: intp(5)}
	ch := &fakeCH{failN: len(tables)} // every mutation fails
	st := &fakeStore{customers: []store.Customer{c}}

	New(func(string) CH { return ch }, st, 0, testLogger()).RunOnce(context.Background())

	if len(st.audits) != 0 {
		t.Errorf("no audit entry expected when nothing was submitted, got %+v", st.audits)
	}
}
