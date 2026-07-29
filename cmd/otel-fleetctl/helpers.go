package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// planPrinter renders the reconciliation plan and, in dry-run, makes clear
// nothing was applied.
type planPrinter struct {
	dryRun  int
	changes int
}

func (p *planPrinter) add(format string, a ...any) {
	prefix := "apply:"
	if p.isDry() {
		prefix = "would:"
	}
	fmt.Printf("  %s %s\n", prefix, fmt.Sprintf(format, a...))
	p.changes++
}

func (p *planPrinter) skip(format string, a ...any) {
	fmt.Printf("  skip:  %s\n", fmt.Sprintf(format, a...))
}

func (p *planPrinter) isDry() bool { return p.dryRun == 1 }

func (p *planPrinter) summary() {
	if p.changes == 0 {
		fmt.Println("everything up to date")
		return
	}
	if p.isDry() {
		fmt.Printf("%d change(s) planned (dry-run; nothing applied)\n", p.changes)
		return
	}
	fmt.Printf("%d change(s) applied\n", p.changes)
}

// planPrinter.dryRun is an int so a zero value means "not dry-run"; set via
// this constructor for clarity.
func newPlan(dryRun bool) *planPrinter {
	p := &planPrinter{}
	if dryRun {
		p.dryRun = 1
	}
	return p
}

func yamlUnmarshalStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

func intEq(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// graphEqual compares two pipeline graphs by their canonical JSON. Map keys are
// sorted by encoding/json, so equality is order-insensitive for configs but
// order-sensitive for the processor/exporter/signal lists (which is correct).
func graphEqual(a, b PipelineGraph) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}
