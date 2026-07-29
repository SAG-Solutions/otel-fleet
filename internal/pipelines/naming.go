package pipelines

// Naming conventions — the metric contract. Collector self-telemetry carries
// component IDs in its labels; these helpers make per-pipeline attribution
// possible:
//
//	collector pipeline: <signal>/<customerSlug>__<pipelineSlug>
//	exporter/processor: <type>/<customerSlug>__<pipelineSlug>__<nodeName-or-index>
//
// so the `exporter` label of otelcol_exporter_* metrics matches
// ^[^/]+/<customerSlug>__<pipelineSlug>__ exactly for one pipeline.

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sag-solutions/otel-fleet/internal/tenants"
)

// PipelineSlug derives the pipeline slug from the pipeline name (same rules
// as customer slugs).
func PipelineSlug(name string) string { return tenants.DeriveSlug(name) }

// pipelineKey is the "<customerSlug>__<pipelineSlug>" part shared by all
// component IDs of one pipeline.
func pipelineKey(customerSlug, pipelineSlug string) string {
	return customerSlug + "__" + pipelineSlug
}

// CollectorPipelineID is the service.pipelines key for one customer pipeline
// and signal, e.g. "logs/acme-corp__backup".
func CollectorPipelineID(signal, customerSlug, pipelineSlug string) string {
	return signal + "/" + pipelineKey(customerSlug, pipelineSlug)
}

// nodeSuffix is the last ID segment of a node: its explicit name, or its
// index within the processor/exporter list.
func nodeSuffix(n Node, idx int) string {
	if n.Name != nil && *n.Name != "" {
		return *n.Name
	}
	return strconv.Itoa(idx)
}

// ComponentID is the namespaced collector component ID for a graph node,
// e.g. "otlp/acme-corp__backup__0".
func ComponentID(n Node, idx int, customerSlug, pipelineSlug string) string {
	return n.Type + "/" + pipelineKey(customerSlug, pipelineSlug) + "__" + nodeSuffix(n, idx)
}

// ExporterLabelRegex matches the `exporter` label of collector self-telemetry
// for exactly the exporters of one pipeline. PromQL label regexes are fully
// anchored (implicit ^...$), hence the trailing wildcard.
func ExporterLabelRegex(customerSlug, pipelineSlug string) string {
	return "^[^/]+/" + regexp.QuoteMeta(pipelineKey(customerSlug, pipelineSlug)) + "__.*"
}

// ComponentType extracts the component type from a collector component ID
// ("otlp/acme__backup__0" -> "otlp").
func ComponentType(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	return id
}

// nodeNamePattern restricts explicit node names: they become part of
// collector component IDs and Prometheus label values, so keep them tame.
var nodeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidNodeName reports whether an explicit node name is acceptable.
func ValidNodeName(name string) bool { return nodeNamePattern.MatchString(name) }
