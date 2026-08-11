package regionstore

import (
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// fakeConn is a distinct driver.Conn per region so we can assert identity.
type fakeConn struct{ driver.Conn }

func TestRoutingAndFallback(t *testing.T) {
	eu := &fakeConn{}
	us := &fakeConn{}
	r := New(
		map[string]driver.Conn{"eu": eu, "us": us},
		map[string]string{"eu": "http://eu-vm", "us": "http://us-vm"},
		"eu",
	)

	if r.ClickHouse("us") != us || r.ClickHouse("eu") != eu {
		t.Error("ClickHouse did not route to the named region")
	}
	// Unknown and empty region fall back to the default (eu).
	if r.ClickHouse("ap") != eu {
		t.Error("unknown region should fall back to default conn")
	}
	if r.ClickHouse("") != eu {
		t.Error("empty region should fall back to default conn")
	}
	if r.VM("us") != "http://us-vm" {
		t.Errorf("VM(us) = %q", r.VM("us"))
	}
	if r.VM("ap") != "http://eu-vm" || r.VM("") != "http://eu-vm" {
		t.Error("unknown/empty region VM should fall back to default")
	}
	if r.DefaultRegion() != "eu" {
		t.Errorf("DefaultRegion = %q", r.DefaultRegion())
	}
}

func TestVMEmptyFallsBack(t *testing.T) {
	// A region with no VM URL configured falls back to the default's.
	r := New(
		map[string]driver.Conn{"eu": &fakeConn{}, "edge": &fakeConn{}},
		map[string]string{"eu": "http://eu-vm", "edge": ""},
		"eu",
	)
	if r.VM("edge") != "http://eu-vm" {
		t.Errorf("VM(edge) = %q, want default fallback", r.VM("edge"))
	}
}
