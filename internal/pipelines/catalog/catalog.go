// Package catalog is the curated list of collector components customers may
// use in forwarding pipelines. Each component carries a JSON Schema (draft
// 2020-12) covering the commonly-used fields; the schema both drives the UI
// builder form and gates structural validation.
package catalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Component kinds.
const (
	KindProcessor = "processor"
	KindExporter  = "exporter"
)

// Component is one curated collector component.
type Component struct {
	Type        string
	Kind        string
	DisplayName string
	Description string
	DocsURL     string
	// SchemaJSON is the JSON Schema (draft 2020-12) for the component config.
	SchemaJSON string
	// DefaultsJSON is the default config applied when the component is added.
	DefaultsJSON string

	compileOnce sync.Once
	compiled    *jsonschema.Schema
	compileErr  error
}

// Schema returns the schema as a generic JSON object (for the API response).
func (c *Component) Schema() map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(c.SchemaJSON), &m); err != nil {
		panic(fmt.Sprintf("catalog: %s/%s schema is not valid JSON: %v", c.Kind, c.Type, err))
	}
	return m
}

// Defaults returns the default config as a generic JSON object.
func (c *Component) Defaults() map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(c.DefaultsJSON), &m); err != nil {
		panic(fmt.Sprintf("catalog: %s/%s defaults are not valid JSON: %v", c.Kind, c.Type, err))
	}
	return m
}

// CompiledSchema compiles (once) and returns the JSON Schema.
func (c *Component) CompiledSchema() (*jsonschema.Schema, error) {
	c.compileOnce.Do(func() {
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		url := "otel-fleet://catalog/" + c.Kind + "/" + c.Type + ".json"
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(c.SchemaJSON))
		if err != nil {
			c.compileErr = fmt.Errorf("catalog %s/%s: parse schema: %w", c.Kind, c.Type, err)
			return
		}
		if err := compiler.AddResource(url, doc); err != nil {
			c.compileErr = fmt.Errorf("catalog %s/%s: add schema: %w", c.Kind, c.Type, err)
			return
		}
		c.compiled, c.compileErr = compiler.Compile(url)
	})
	return c.compiled, c.compileErr
}

// ValidateConfig validates a node config against the component schema.
// The returned error is a *jsonschema.ValidationError for schema violations.
func (c *Component) ValidateConfig(config map[string]any) error {
	sch, err := c.CompiledSchema()
	if err != nil {
		return err
	}
	if config == nil {
		config = map[string]any{}
	}
	return sch.Validate(normalize(config))
}

// normalize converts config values into the shapes the validator expects
// (json.Number from UseNumber decoding is not understood by jsonschema/v6).
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalize(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalize(val)
		}
		return out
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	default:
		return v
	}
}

// Lookup finds a component by kind and type.
func Lookup(kind, typ string) (*Component, bool) {
	for _, c := range all {
		if c.Kind == kind && c.Type == typ {
			return c, true
		}
	}
	return nil, false
}

// Processors returns the curated processors in catalog order.
func Processors() []*Component { return byKind(KindProcessor) }

// Exporters returns the curated exporters in catalog order.
func Exporters() []*Component { return byKind(KindExporter) }

func byKind(kind string) []*Component {
	out := []*Component{}
	for _, c := range all {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// All returns every catalog component.
func All() []*Component { return all }

// Common schema fragments.
const (
	durationSchema = `{"type": "string", "pattern": "^[0-9]+(ns|us|ms|s|m|h)$", "description": "Go duration, e.g. 5s"}`
	// headersSchema marks values as secrets ("format": "password"): UIs mask
	// them, and the pipeline service encrypts them with the master key before
	// they reach pipeline_versions.graph (see internal/pipelines/secrets.go).
	headersSchema = `{
		"type": "object",
		"description": "Additional headers sent with every request.",
		"additionalProperties": {"type": "string", "format": "password"}
	}`
	sendingQueueSchema = `{
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"enabled": {"type": "boolean"},
			"queue_size": {"type": "integer", "minimum": 1}
		}
	}`
	retryOnFailureSchema = `{
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"enabled": {"type": "boolean"}
		}
	}`
	tlsSchema = `{
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"insecure": {"type": "boolean", "description": "Disable TLS (plaintext connection)."},
			"insecure_skip_verify": {"type": "boolean", "description": "Skip certificate verification."}
		}
	}`
	compressionSchema = `{"type": "string", "enum": ["gzip", "zstd", "snappy", "none"]}`
	errorModeSchema   = `{"type": "string", "enum": ["ignore", "silent", "propagate"]}`
)

var all = []*Component{
	// --- processors ---
	{
		Type:        "batch",
		Kind:        KindProcessor,
		DisplayName: "Batch",
		Description: "Batches telemetry before export to reduce request count.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector/tree/main/processor/batchprocessor",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"properties": {
				"timeout": ` + durationSchema + `,
				"send_batch_size": {"type": "integer", "minimum": 0},
				"send_batch_max_size": {"type": "integer", "minimum": 0}
			}
		}`,
		DefaultsJSON: `{"timeout": "5s", "send_batch_size": 8192}`,
	},
	{
		Type:        "memory_limiter",
		Kind:        KindProcessor,
		DisplayName: "Memory Limiter",
		Description: "Refuses/drops data when the collector approaches its memory limit.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector/tree/main/processor/memorylimiterprocessor",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"required": ["check_interval"],
			"properties": {
				"check_interval": ` + durationSchema + `,
				"limit_percentage": {"type": "integer", "minimum": 1, "maximum": 100},
				"spike_limit_percentage": {"type": "integer", "minimum": 0, "maximum": 100},
				"limit_mib": {"type": "integer", "minimum": 0},
				"spike_limit_mib": {"type": "integer", "minimum": 0}
			}
		}`,
		DefaultsJSON: `{"check_interval": "1s", "limit_percentage": 80, "spike_limit_percentage": 20}`,
	},
	{
		Type:        "filter",
		Kind:        KindProcessor,
		DisplayName: "Filter",
		Description: "Drops telemetry matching OTTL conditions.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/filterprocessor",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"properties": {
				"error_mode": ` + errorModeSchema + `,
				"logs": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"log_record": {"type": "array", "items": {"type": "string"}, "description": "OTTL conditions; matching records are dropped."}
					}
				},
				"traces": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"span": {"type": "array", "items": {"type": "string"}},
						"spanevent": {"type": "array", "items": {"type": "string"}}
					}
				},
				"metrics": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"metric": {"type": "array", "items": {"type": "string"}},
						"datapoint": {"type": "array", "items": {"type": "string"}}
					}
				}
			}
		}`,
		DefaultsJSON: `{"error_mode": "ignore"}`,
	},
	{
		Type:        "transform",
		Kind:        KindProcessor,
		DisplayName: "Transform",
		Description: "Modifies telemetry with OTTL statements.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/transformprocessor",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"properties": {
				"error_mode": ` + errorModeSchema + `,
				"log_statements": {"type": "array", "items": {"type": "string"}, "description": "Flat OTTL statements applied to logs."},
				"trace_statements": {"type": "array", "items": {"type": "string"}},
				"metric_statements": {"type": "array", "items": {"type": "string"}}
			}
		}`,
		DefaultsJSON: `{"error_mode": "ignore"}`,
	},
	{
		Type:        "attributes",
		Kind:        KindProcessor,
		DisplayName: "Attributes",
		Description: "Inserts, updates, deletes or hashes span/log/metric attributes.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/attributesprocessor",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"required": ["actions"],
			"properties": {
				"actions": {
					"type": "array",
					"items": {
						"type": "object",
						"additionalProperties": false,
						"required": ["key", "action"],
						"properties": {
							"key": {"type": "string", "minLength": 1},
							"action": {"type": "string", "enum": ["insert", "update", "upsert", "delete", "hash", "extract", "convert"]},
							"value": {},
							"from_attribute": {"type": "string"},
							"pattern": {"type": "string"},
							"converted_type": {"type": "string", "enum": ["int", "double", "string"]}
						}
					}
				}
			}
		}`,
		DefaultsJSON: `{"actions": []}`,
	},
	{
		Type:        "resource",
		Kind:        KindProcessor,
		DisplayName: "Resource",
		Description: "Modifies resource attributes (applies to all signals).",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/resourceprocessor",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"required": ["attributes"],
			"properties": {
				"attributes": {
					"type": "array",
					"items": {
						"type": "object",
						"additionalProperties": false,
						"required": ["key", "action"],
						"properties": {
							"key": {"type": "string", "minLength": 1},
							"action": {"type": "string", "enum": ["insert", "update", "upsert", "delete", "hash", "extract", "convert"]},
							"value": {},
							"from_attribute": {"type": "string"},
							"pattern": {"type": "string"}
						}
					}
				}
			}
		}`,
		DefaultsJSON: `{"attributes": []}`,
	},

	// --- exporters ---
	{
		Type:        "otlp",
		Kind:        KindExporter,
		DisplayName: "OTLP (gRPC)",
		Description: "Sends telemetry to an OTLP gRPC endpoint.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector/tree/main/exporter/otlpexporter",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"required": ["endpoint"],
			"properties": {
				"endpoint": {"type": "string", "minLength": 1, "description": "host:port of the OTLP gRPC endpoint."},
				"tls": ` + tlsSchema + `,
				"compression": ` + compressionSchema + `,
				"timeout": ` + durationSchema + `,
				"headers": ` + headersSchema + `,
				"sending_queue": ` + sendingQueueSchema + `,
				"retry_on_failure": ` + retryOnFailureSchema + `
			}
		}`,
		DefaultsJSON: `{"endpoint": "backend.example.com:4317", "tls": {"insecure": false}, "compression": "gzip"}`,
	},
	{
		Type:        "otlphttp",
		Kind:        KindExporter,
		DisplayName: "OTLP (HTTP)",
		Description: "Sends telemetry to an OTLP/HTTP endpoint.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector/tree/main/exporter/otlphttpexporter",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"required": ["endpoint"],
			"properties": {
				"endpoint": {"type": "string", "minLength": 1, "description": "Base URL, e.g. https://otlp.example.com:4318."},
				"tls": ` + tlsSchema + `,
				"compression": ` + compressionSchema + `,
				"timeout": ` + durationSchema + `,
				"headers": ` + headersSchema + `,
				"sending_queue": ` + sendingQueueSchema + `,
				"retry_on_failure": ` + retryOnFailureSchema + `
			}
		}`,
		DefaultsJSON: `{"endpoint": "https://backend.example.com:4318", "compression": "gzip"}`,
	},
	{
		Type:        "debug",
		Kind:        KindExporter,
		DisplayName: "Debug",
		Description: "Logs telemetry to the collector's stdout (development only).",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector/tree/main/exporter/debugexporter",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"properties": {
				"verbosity": {"type": "string", "enum": ["basic", "normal", "detailed"]},
				"sampling_initial": {"type": "integer", "minimum": 0},
				"sampling_thereafter": {"type": "integer", "minimum": 0}
			}
		}`,
		DefaultsJSON: `{"verbosity": "basic"}`,
	},
	{
		Type:        "clickhouse",
		Kind:        KindExporter,
		DisplayName: "ClickHouse",
		Description: "Writes telemetry to ClickHouse tables.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/exporter/clickhouseexporter",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"required": ["endpoint"],
			"properties": {
				"endpoint": {"type": "string", "minLength": 1, "description": "ClickHouse DSN, e.g. tcp://host:9000."},
				"database": {"type": "string"},
				"username": {"type": "string"},
				"password": {"type": "string", "format": "password"},
				"logs_table_name": {"type": "string"},
				"traces_table_name": {"type": "string"},
				"metrics_table_name": {"type": "string"},
				"ttl": ` + durationSchema + `,
				"timeout": ` + durationSchema + `,
				"sending_queue": ` + sendingQueueSchema + `,
				"retry_on_failure": ` + retryOnFailureSchema + `
			}
		}`,
		DefaultsJSON: `{"endpoint": "tcp://clickhouse.example.com:9000", "database": "otel"}`,
	},
	{
		Type:        "prometheusremotewrite",
		Kind:        KindExporter,
		DisplayName: "Prometheus Remote Write",
		Description: "Sends metrics to a Prometheus remote-write endpoint (metrics only).",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/exporter/prometheusremotewriteexporter",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"required": ["endpoint"],
			"properties": {
				"endpoint": {"type": "string", "minLength": 1, "description": "Remote-write URL, e.g. https://vm.example.com/api/v1/write."},
				"tls": ` + tlsSchema + `,
				"timeout": ` + durationSchema + `,
				"headers": ` + headersSchema + `,
				"external_labels": {"type": "object", "additionalProperties": {"type": "string"}},
				"remote_write_queue": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"enabled": {"type": "boolean"},
						"queue_size": {"type": "integer", "minimum": 1},
						"num_consumers": {"type": "integer", "minimum": 1}
					}
				},
				"resource_to_telemetry_conversion": {
					"type": "object",
					"additionalProperties": false,
					"properties": {"enabled": {"type": "boolean"}}
				}
			}
		}`,
		DefaultsJSON: `{"endpoint": "https://metrics.example.com/api/v1/write"}`,
	},
	{
		Type:        "file",
		Kind:        KindExporter,
		DisplayName: "File",
		Description: "Writes telemetry to a file on the forwarding collector.",
		DocsURL:     "https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/exporter/fileexporter",
		SchemaJSON: `{
			"type": "object",
			"additionalProperties": false,
			"required": ["path"],
			"properties": {
				"path": {"type": "string", "minLength": 1},
				"format": {"type": "string", "enum": ["json", "proto"]},
				"flush_interval": ` + durationSchema + `,
				"rotation": {
					"type": "object",
					"additionalProperties": false,
					"properties": {
						"max_megabytes": {"type": "integer", "minimum": 1},
						"max_days": {"type": "integer", "minimum": 1},
						"max_backups": {"type": "integer", "minimum": 0}
					}
				}
			}
		}`,
		DefaultsJSON: `{"path": "/var/lib/otelcol/export.json", "format": "json"}`,
	},
}
