package pipelines

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/sag-solutions/otel-fleet/internal/pipelines/catalog"
)

// Issue is one validation finding. Path points into the graph
// (e.g. exporters[0].config.endpoint) when attributable.
type Issue struct {
	Path    *string
	Message string
}

func issueAt(path, message string) Issue { return Issue{Path: &path, Message: message} }

// Result is the outcome of validating a pipeline graph.
type Result struct {
	Valid bool
	// Issues holds errors (and, for a missing collector binary, one warning).
	Issues []Issue
	// RenderedYAML is the per-pipeline fragment; set when rendering succeeded
	// (even if otelcol validate then failed).
	RenderedYAML *string
}

// Validator validates pipeline graphs: structurally against the catalog and
// authoritatively by rendering the full forwarding config and running
// `otelcol validate` with the real distro binary.
type Validator struct {
	// BinPath is the collector binary (env OTEL_FLEET_OTELCOL_BIN).
	BinPath string
	// Timeout bounds one otelcol validate run.
	Timeout time.Duration
	Log     *slog.Logger
}

// NewValidator creates a validator with the standard 15s timeout.
func NewValidator(binPath string, log *slog.Logger) *Validator {
	return &Validator{BinPath: binPath, Timeout: 15 * time.Second, Log: log}
}

// ValidateStructural checks the graph against the catalog: known components,
// schema-valid configs, sane signals, at least one exporter, usable node
// names and no duplicate component IDs.
func ValidateStructural(g Graph) []Issue {
	var issues []Issue

	if len(g.Signals) == 0 {
		issues = append(issues, issueAt("signals", "at least one signal is required"))
	}
	seenSig := map[string]bool{}
	for i, sig := range g.Signals {
		if !validSignal(sig) {
			issues = append(issues, issueAt(fmt.Sprintf("signals[%d]", i), fmt.Sprintf("unknown signal %q (want logs, traces or metrics)", sig)))
		} else if seenSig[sig] {
			issues = append(issues, issueAt(fmt.Sprintf("signals[%d]", i), fmt.Sprintf("duplicate signal %q", sig)))
		}
		seenSig[sig] = true
	}

	if len(g.Exporters) == 0 {
		issues = append(issues, issueAt("exporters", "at least one exporter is required"))
	}

	issues = append(issues, validateNodes("processors", catalog.KindProcessor, g.Processors)...)
	issues = append(issues, validateNodes("exporters", catalog.KindExporter, g.Exporters)...)
	return issues
}

func validateNodes(field, kindName string, nodes []Node) []Issue {
	var issues []Issue
	seenID := map[string]int{}
	for i, n := range nodes {
		base := fmt.Sprintf("%s[%d]", field, i)
		if n.Name != nil && *n.Name != "" && !ValidNodeName(*n.Name) {
			issues = append(issues, issueAt(base+".name", "node name must match ^[a-z0-9][a-z0-9-]{0,63}$"))
		}
		comp, ok := catalog.Lookup(kindName, n.Type)
		if !ok {
			issues = append(issues, issueAt(base+".type", fmt.Sprintf("unknown %s type %q", kindName, n.Type)))
			continue
		}
		// Duplicate component IDs (same type + same name/index suffix) would
		// silently merge in the rendered config.
		id := ComponentID(n, i, "c", "p")
		if prev, dup := seenID[id]; dup {
			issues = append(issues, issueAt(base, fmt.Sprintf("duplicate %s instance (same type and name as %s[%d]); set a distinct name", kindName, field, prev)))
		}
		seenID[id] = i

		if err := comp.ValidateConfig(n.Config); err != nil {
			var verr *jsonschema.ValidationError
			if errors.As(err, &verr) {
				issues = append(issues, schemaIssues(base+".config", verr)...)
			} else {
				issues = append(issues, issueAt(base+".config", err.Error()))
			}
		}
	}
	return issues
}

// schemaIssues flattens a jsonschema.ValidationError tree into path-ed issues.
func schemaIssues(base string, verr *jsonschema.ValidationError) []Issue {
	var issues []Issue
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		path := base
		for _, seg := range e.InstanceLocation {
			path += "." + seg
		}
		// Missing required properties: point at the property itself, the way
		// a UI wants to highlight the empty field.
		if req, ok := e.ErrorKind.(*kind.Required); ok && len(req.Missing) > 0 {
			for _, missing := range req.Missing {
				issues = append(issues, issueAt(path+"."+missing, "required property is missing"))
			}
			return
		}
		issues = append(issues, issueAt(path, e.ErrorKind.LocalizedString(localePrinter)))
	}
	walk(verr)
	return issues
}

// Validate runs both stages for a candidate pipeline: structural checks, then
// an authoritative `otelcol validate` over the full config of the candidate's
// target class — the forwarding config for forwarding pipelines, the
// customer's merged edge config for edge pipelines — with the candidate
// included (replacing its currently active version, if any). activeOthers
// must already be filtered to the same class (and, for edge, customer).
// A missing collector binary downgrades stage two to a warning issue — CI and
// fresh checkouts have no distro binary.
func (v *Validator) Validate(ctx context.Context, targetClass string, candidate RenderPipeline, activeOthers []RenderPipeline) Result {
	issues := ValidateStructural(candidate.Graph)
	if len(issues) > 0 {
		return Result{Valid: false, Issues: issues}
	}

	renderFragment, renderFull := RenderFragment, RenderForwardingConfig
	if targetClass == ClassEdge {
		renderFragment, renderFull = RenderEdgeFragment, RenderEdgeConfig
	}

	fragment, err := renderFragment(candidate)
	if err != nil {
		return Result{Valid: false, Issues: []Issue{{Message: "render: " + err.Error()}}}
	}

	full, err := renderFull(append(append([]RenderPipeline{}, activeOthers...), candidate))
	if err != nil {
		return Result{Valid: false, Issues: []Issue{{Message: "render " + targetClass + " config: " + err.Error()}}, RenderedYAML: &fragment}
	}

	if _, statErr := os.Stat(v.BinPath); statErr != nil {
		msg := fmt.Sprintf("warning: otelcol validate skipped: collector binary unavailable at %s", v.BinPath)
		if v.Log != nil {
			v.Log.Warn("otelcol validate skipped", "bin", v.BinPath, "err", statErr)
		}
		return Result{Valid: true, Issues: []Issue{{Message: msg}}, RenderedYAML: &fragment}
	}

	if out, err := v.runValidate(ctx, full); err != nil {
		return Result{Valid: false, Issues: []Issue{{Message: "otelcol validate failed: " + out}}, RenderedYAML: &fragment}
	}
	return Result{Valid: true, Issues: []Issue{}, RenderedYAML: &fragment}
}

// ValidateRendered runs `otelcol validate` over an already-rendered full
// config; used to sanity-check the empty-state config in tests/tools.
func (v *Validator) ValidateRendered(ctx context.Context, fullConfig string) (string, error) {
	return v.runValidate(ctx, fullConfig)
}

func (v *Validator) runValidate(ctx context.Context, fullConfig string) (string, error) {
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tmp, err := os.CreateTemp("", "otel-fleet-validate-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create temp config: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck
	if _, err := tmp.WriteString(fullConfig); err != nil {
		tmp.Close() //nolint:errcheck
		return "", fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp config: %w", err)
	}

	cmd := exec.CommandContext(ctx, v.BinPath, "validate", "--config="+tmp.Name())
	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err = cmd.Run()
	if v.Log != nil {
		v.Log.Info("otelcol validate ran", "bin", v.BinPath, "ok", err == nil)
	}
	if err != nil {
		out := strings.TrimSpace(stderr.String())
		if out == "" {
			out = strings.TrimSpace(stdout.String())
		}
		if out == "" {
			out = err.Error()
		}
		return condenseValidateOutput(out), err
	}
	return "", nil
}

// condenseValidateOutput trims otelcol stderr to the interesting part: the
// final "Error:" block if present.
func condenseValidateOutput(out string) string {
	if i := strings.LastIndex(out, "Error:"); i >= 0 {
		return strings.TrimSpace(out[i:])
	}
	return out
}

var localePrinter = message.NewPrinter(language.English)
