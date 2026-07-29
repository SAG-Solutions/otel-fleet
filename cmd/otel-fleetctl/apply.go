package main

import (
	"fmt"
	"os"
)

// Spec is the declarative fleet config (export output / apply input).
type Spec struct {
	Customers []CustomerSpec `yaml:"customers"`
}

// CustomerSpec is one customer plus its pipelines.
type CustomerSpec struct {
	Name                 string         `yaml:"name"`
	Slug                 string         `yaml:"slug,omitempty"`
	RateLimitItemsPerSec *int           `yaml:"rateLimitItemsPerSec,omitempty"`
	RetentionDays        *int           `yaml:"retentionDays,omitempty"`
	Pipelines            []PipelineSpec `yaml:"pipelines,omitempty"`
}

// PipelineSpec is one pipeline's desired graph.
type PipelineSpec struct {
	Name        string        `yaml:"name"`
	TargetClass string        `yaml:"targetClass"`
	Graph       PipelineGraph `yaml:"graph"`
}

func cmdExport(c *Client, args []string) error {
	if err := requireToken(c); err != nil {
		return err
	}
	out, _, _, err := parseFlags(args)
	if err != nil {
		return err
	}

	customers, err := c.listCustomers()
	if err != nil {
		return err
	}
	sortCustomers(customers)

	spec := Spec{}
	for _, cust := range customers {
		if cust.Status == "deleted" {
			continue
		}
		cs := CustomerSpec{Name: cust.Name, Slug: cust.Slug, RateLimitItemsPerSec: cust.RateLimitItemsPerSec, RetentionDays: cust.RetentionDays}
		pipes, err := c.listPipelines(cust.ID)
		if err != nil {
			return err
		}
		for _, p := range pipes {
			ver := p.ActiveVersion
			if ver == nil {
				ver = p.LatestVersion
			}
			if ver == nil {
				continue // no versions yet
			}
			pv, err := c.getPipelineVersion(p.ID, *ver)
			if err != nil {
				return err
			}
			cs.Pipelines = append(cs.Pipelines, PipelineSpec{Name: p.Name, TargetClass: p.TargetClass, Graph: pv.Graph})
		}
		spec.Customers = append(spec.Customers, cs)
	}

	data, err := marshalYAML(spec)
	if err != nil {
		return err
	}
	header := "# otel-fleet declarative config — exported. Secrets are redacted\n" +
		"# (__otel_fleet_redacted__); fill them in before applying to a fresh target.\n"
	data = append([]byte(header), data...)
	if out == "" {
		_, err = os.Stdout.Write(data)
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d customer(s) to %s\n", len(spec.Customers), out)
	return nil
}

func cmdApply(c *Client, args []string) error {
	if err := requireToken(c); err != nil {
		return err
	}
	_, file, dryRun, err := parseFlags(args)
	if err != nil {
		return err
	}
	if file == "" {
		return fmt.Errorf("apply requires -f FILE")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var spec Spec
	if err := yamlUnmarshalStrict(raw, &spec); err != nil {
		return fmt.Errorf("parse %s: %w", file, err)
	}

	// Index existing customers by slug for reconciliation.
	existing, err := c.listCustomers()
	if err != nil {
		return err
	}
	bySlug := map[string]Customer{}
	for _, cu := range existing {
		bySlug[cu.Slug] = cu
	}

	plan := newPlan(dryRun)
	for _, cs := range spec.Customers {
		cust, ok := bySlug[cs.Slug]
		if !ok && cs.Slug == "" {
			// Match by name when no slug is given (best effort).
			for _, cu := range existing {
				if cu.Name == cs.Name {
					cust, ok = cu, true
					break
				}
			}
		}
		if !ok {
			plan.add("create customer %q", cs.Name)
			if !dryRun {
				created, err := c.createCustomer(cs.Name, cs.Slug)
				if err != nil {
					return fmt.Errorf("create customer %q: %w", cs.Name, err)
				}
				cust = created
				ok = true // fall through so quota/retention are set in this same apply
			}
		}
		// Reconcile name/quota/retention.
		if ok {
			upd := map[string]any{}
			if cust.Name != cs.Name {
				upd["name"] = cs.Name
			}
			if !intEq(cust.RateLimitItemsPerSec, cs.RateLimitItemsPerSec) {
				upd["rateLimitItemsPerSec"] = cs.RateLimitItemsPerSec
			}
			if !intEq(cust.RetentionDays, cs.RetentionDays) {
				upd["retentionDays"] = cs.RetentionDays
			}
			if len(upd) > 0 {
				plan.add("update customer %q %v", cs.Slug, keysOf(upd))
				if !dryRun {
					if err := c.updateCustomer(cust.ID, upd); err != nil {
						return fmt.Errorf("update customer %q: %w", cs.Slug, err)
					}
				}
			}
		}
		if dryRun && cust.ID == "" {
			// Can't reconcile pipelines of a not-yet-created customer in dry-run.
			for _, p := range cs.Pipelines {
				plan.add("  would create pipeline %q (targetClass=%s) for new customer %q", p.Name, p.TargetClass, cs.Name)
			}
			continue
		}
		if err := applyPipelines(c, plan, cust, cs.Pipelines, dryRun); err != nil {
			return err
		}
	}
	plan.summary()
	return nil
}

func applyPipelines(c *Client, plan *planPrinter, cust Customer, specs []PipelineSpec, dryRun bool) error {
	existing, err := c.listPipelines(cust.ID)
	if err != nil {
		return err
	}
	byName := map[string]Pipeline{}
	for _, p := range existing {
		byName[p.Name] = p
	}
	for _, ps := range specs {
		p, ok := byName[ps.Name]
		if !ok {
			plan.add("create pipeline %q for %q + activate", ps.Name, cust.Slug)
			if !dryRun {
				created, err := c.createPipeline(cust.ID, ps.Name, defaultClass(ps.TargetClass), ps.Graph)
				if err != nil {
					return fmt.Errorf("create pipeline %q: %w", ps.Name, err)
				}
				if err := c.activateVersion(created.ID, 1); err != nil {
					return fmt.Errorf("activate pipeline %q v1: %w", ps.Name, err)
				}
			}
			continue
		}
		// Compare the desired graph against the active/latest version.
		cur := p.ActiveVersion
		if cur == nil {
			cur = p.LatestVersion
		}
		changed := true
		if cur != nil {
			pv, err := c.getPipelineVersion(p.ID, *cur)
			if err != nil {
				return err
			}
			changed = !graphEqual(pv.Graph, ps.Graph)
		}
		if !changed {
			plan.skip("pipeline %q/%q unchanged", cust.Slug, ps.Name)
			continue
		}
		plan.add("new version + activate for pipeline %q/%q", cust.Slug, ps.Name)
		if !dryRun {
			ver, err := c.createPipelineVersion(p.ID, ps.Graph)
			if err != nil {
				return fmt.Errorf("new version for %q: %w", ps.Name, err)
			}
			if err := c.activateVersion(p.ID, ver.Version); err != nil {
				return fmt.Errorf("activate %q v%d: %w", ps.Name, ver.Version, err)
			}
		}
	}
	return nil
}

func defaultClass(c string) string {
	if c == "" {
		return "forwarding"
	}
	return c
}
