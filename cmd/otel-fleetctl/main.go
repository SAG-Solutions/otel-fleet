// Command otel-fleetctl is the otel-fleet CLI: manage customers and pipelines
// programmatically and treat them as declarative config (GitOps). It talks to
// the control-plane REST API with a management-API token (otm_pat_).
//
//	export OTEL_FLEET_URL=https://otel-fleet.example.com
//	export OTEL_FLEET_TOKEN=otm_pat_...
//	otel-fleetctl customers
//	otel-fleetctl export -o fleet.yaml
//	otel-fleetctl apply -f fleet.yaml --dry-run
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `otel-fleetctl — manage otel-fleet as code

Usage:
  otel-fleetctl <command> [flags]

Commands:
  customers            list customers
  pipelines            list pipelines (all customers)
  export [-o FILE]     write customers + pipelines as declarative YAML (stdout if no -o)
  apply -f FILE        reconcile the target from a declarative YAML
                       [--dry-run] print planned changes without applying

Config (flags override env):
  OTEL_FLEET_URL    control-plane base URL (default http://localhost:8080)
  OTEL_FLEET_TOKEN  management-API token (otm_pat_...)
`)
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("a command is required")
	}
	cmd, rest := args[0], args[1:]
	client := newClient(env("OTEL_FLEET_URL", "http://localhost:8080"), os.Getenv("OTEL_FLEET_TOKEN"))

	switch cmd {
	case "customers":
		return cmdCustomers(client)
	case "pipelines":
		return cmdPipelines(client)
	case "export":
		return cmdExport(client, rest)
	case "apply":
		return cmdApply(client, rest)
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func requireToken(c *Client) error {
	if c.token == "" {
		return fmt.Errorf("OTEL_FLEET_TOKEN is not set (create one under Settings → API tokens)")
	}
	return nil
}

func cmdCustomers(c *Client) error {
	if err := requireToken(c); err != nil {
		return err
	}
	customers, err := c.listCustomers()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tCLIENT ID\tSTATUS\tQUOTA/s\tRETENTION")
	for _, c := range customers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.Slug, c.Name, c.ClientID, c.Status, optInt(c.RateLimitItemsPerSec, "∞"), optInt(c.RetentionDays, "default"))
	}
	return w.Flush()
}

func cmdPipelines(c *Client) error {
	if err := requireToken(c); err != nil {
		return err
	}
	customers, err := c.listCustomers()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CUSTOMER\tPIPELINE\tCLASS\tACTIVE\tLATEST")
	for _, cust := range customers {
		pipes, err := c.listPipelines(cust.ID)
		if err != nil {
			return err
		}
		for _, p := range pipes {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", cust.Slug, p.Name, p.TargetClass, optIntP(p.ActiveVersion, "-"), optIntP(p.LatestVersion, "-"))
		}
	}
	return w.Flush()
}

func optInt(p *int, dflt string) string {
	if p == nil {
		return dflt
	}
	return fmt.Sprintf("%d", *p)
}
func optIntP(p *int, dflt string) string { return optInt(p, dflt) }

// parseFlags is a tiny flag reader: -o/--out, -f/--file, --dry-run.
func parseFlags(args []string) (out, file string, dryRun bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "--out":
			i++
			if i >= len(args) {
				return "", "", false, fmt.Errorf("%s requires a value", a)
			}
			out = args[i]
		case a == "-f" || a == "--file":
			i++
			if i >= len(args) {
				return "", "", false, fmt.Errorf("%s requires a value", a)
			}
			file = args[i]
		case a == "--dry-run":
			dryRun = true
		default:
			return "", "", false, fmt.Errorf("unexpected argument %q", a)
		}
	}
	return out, file, dryRun, nil
}

func marshalYAML(v any) ([]byte, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return []byte(b.String()), nil
}

// sortCustomers keeps export output deterministic.
func sortCustomers(cs []Customer) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].Slug < cs[j].Slug })
}
