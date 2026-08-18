// Package billing turns per-customer ingest usage into a priced monthly
// statement. All money is integer micro-units of the configured currency
// (1 unit = 1e-6); amounts are computed exactly with math/big to avoid float
// rounding.
package billing

import (
	"math/big"
	"sort"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/stats"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

const bytesPerGiB = 1 << 30

// Line is one customer's priced usage for the period.
type Line struct {
	CustomerID     uuid.UUID
	Name           string
	Items          int64
	Bytes          int64
	BytesCostMicro int64
	ItemsCostMicro int64
	TotalMicro     int64
	// Overridden is true when this customer's line used a per-customer price
	// override (on at least one dimension) instead of the global rate.
	Overridden bool
}

// Statement is a full period's bill across customers.
type Statement struct {
	Month                     string // YYYY-MM
	Currency                  string
	PricePerGiBMicro          int64
	PricePerMillionItemsMicro int64
	Lines                     []Line
	TotalMicro                int64
}

// Compute prices each customer's usage and totals the statement. The global
// settings prices apply unless a customer has an entry in overrides, whose
// non-nil price supersedes the global rate for that dimension. Lines are sorted
// by amount desc, then name.
func Compute(month string, costs []stats.CustomerCost, settings store.BillingSettings, overrides map[uuid.UUID]store.BillingOverride) Statement {
	st := Statement{
		Month:                     month,
		Currency:                  settings.Currency,
		PricePerGiBMicro:          settings.PricePerGiBMicro,
		PricePerMillionItemsMicro: settings.PricePerMillionItemsMicro,
		Lines:                     make([]Line, 0, len(costs)),
	}
	for _, c := range costs {
		gibPrice, itemsPrice := settings.PricePerGiBMicro, settings.PricePerMillionItemsMicro
		overridden := false
		if ov, ok := overrides[c.CustomerID]; ok {
			if ov.PricePerGiBMicro != nil {
				gibPrice, overridden = *ov.PricePerGiBMicro, true
			}
			if ov.PricePerMillionItemsMicro != nil {
				itemsPrice, overridden = *ov.PricePerMillionItemsMicro, true
			}
		}
		bytesCost := mulDiv(gibPrice, c.Bytes, bytesPerGiB)
		itemsCost := mulDiv(itemsPrice, c.Items, 1_000_000)
		total := bytesCost + itemsCost
		st.Lines = append(st.Lines, Line{
			CustomerID:     c.CustomerID,
			Name:           c.Name,
			Items:          c.Items,
			Bytes:          c.Bytes,
			BytesCostMicro: bytesCost,
			ItemsCostMicro: itemsCost,
			TotalMicro:     total,
			Overridden:     overridden,
		})
		st.TotalMicro += total
	}
	sort.SliceStable(st.Lines, func(i, j int) bool {
		if st.Lines[i].TotalMicro != st.Lines[j].TotalMicro {
			return st.Lines[i].TotalMicro > st.Lines[j].TotalMicro
		}
		return st.Lines[i].Name < st.Lines[j].Name
	})
	return st
}

// mulDiv computes price*qty/divisor exactly (big.Int) and returns micro-units.
// The big.Int intermediate avoids the int64 overflow that price×bytes hits for
// realistic volumes; the final division floors (a usage estimate, not a cent).
func mulDiv(price, qty, divisor int64) int64 {
	if price <= 0 || qty <= 0 || divisor <= 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(price), big.NewInt(qty))
	n.Quo(n, big.NewInt(divisor))
	return n.Int64()
}
