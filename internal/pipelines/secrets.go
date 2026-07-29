package pipelines

import (
	"encoding/base64"
	"fmt"

	"github.com/sag-solutions/otel-fleet/internal/crypto"
	"github.com/sag-solutions/otel-fleet/internal/pipelines/catalog"
)

// RedactedSentinel is what password-marked fields look like whenever a graph
// leaves the backend. Clients send it back unchanged to mean "keep the stored
// secret"; the plaintext never round-trips through the browser.
const RedactedSentinel = "__otel_fleet_redacted__"

// encKey marks an encrypted value inside a stored graph:
// {"$enc": "<base64 ciphertext>"} where a plaintext string would sit.
const encKey = "$enc"

// encValue extracts the ciphertext when v is an {"$enc": ...} marker.
func encValue(v any) ([]byte, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, false
	}
	s, ok := m[encKey].(string)
	if !ok {
		return nil, false
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, false
	}
	return b, true
}

func newEncValue(ciphertext []byte) map[string]any {
	return map[string]any{encKey: base64.StdEncoding.EncodeToString(ciphertext)}
}

// transformSecrets walks value alongside its catalog JSON Schema and replaces
// every field marked "format": "password" with fn's result. Containers along
// the way are copied; untouched values are shared.
func transformSecrets(value any, schema map[string]any, path string, fn func(path string, value any) (any, error)) (any, error) {
	if schema == nil {
		return value, nil
	}
	if f, _ := schema["format"].(string); f == "password" {
		return fn(path, value)
	}
	switch v := value.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		addl, _ := schema["additionalProperties"].(map[string]any)
		out := make(map[string]any, len(v))
		for k, val := range v {
			var sub map[string]any
			if props != nil {
				sub, _ = props[k].(map[string]any)
			}
			if sub == nil {
				sub = addl
			}
			nv, err := transformSecrets(val, sub, path+"."+k, fn)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		items, _ := schema["items"].(map[string]any)
		if items == nil {
			return value, nil
		}
		out := make([]any, len(v))
		for i, val := range v {
			nv, err := transformSecrets(val, items, fmt.Sprintf("%s[%d]", path, i), fn)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	default:
		return value, nil
	}
}

// transformGraphSecrets applies fn to every password-marked field of every
// known node in the graph. Unknown component types are skipped (structural
// validation reports them). Paths look like
// "exporters[0].config.headers.authorization".
func transformGraphSecrets(g Graph, fn func(path string, value any) (any, error)) (Graph, error) {
	sections := []struct {
		name  string
		kind  string
		nodes []Node
	}{
		{"processors", catalog.KindProcessor, g.Processors},
		{"exporters", catalog.KindExporter, g.Exporters},
	}
	out := g
	for si, sec := range sections {
		nodes := make([]Node, len(sec.nodes))
		copy(nodes, sec.nodes)
		for i, n := range nodes {
			comp, ok := catalog.Lookup(sec.kind, n.Type)
			if !ok || n.Config == nil {
				continue
			}
			base := fmt.Sprintf("%s[%d].config", sec.name, i)
			cfg, err := transformSecrets(n.Config, comp.Schema(), base, fn)
			if err != nil {
				return Graph{}, err
			}
			nodes[i].Config, _ = cfg.(map[string]any)
		}
		if si == 0 {
			out.Processors = nodes
		} else {
			out.Exporters = nodes
		}
	}
	return out, nil
}

// secretIssue is a validation problem found while preparing secrets; it maps
// onto the standard Issue shape.
func secretIssue(path, message string) Issue { return issueAt(path, message) }

// EncryptGraphSecrets prepares an incoming graph for storage: plaintext
// password values are encrypted with the master key, RedactedSentinel values
// are copied (still encrypted) from prev — the pipeline's latest stored
// version — at the same graph path. Already-encrypted markers pass through.
// Problems (missing master key, sentinel without a stored predecessor) come
// back as validation issues.
func EncryptGraphSecrets(g Graph, cipher *crypto.Cipher, prev *Graph) (Graph, []Issue, error) {
	// Index the previous version's secret fields by path for copy-forward.
	prevSecrets := map[string]any{}
	if prev != nil {
		if _, err := transformGraphSecrets(*prev, func(path string, value any) (any, error) {
			prevSecrets[path] = value
			return value, nil
		}); err != nil {
			return Graph{}, nil, err
		}
	}

	var issues []Issue
	out, err := transformGraphSecrets(g, func(path string, value any) (any, error) {
		if _, ok := encValue(value); ok {
			return value, nil // already encrypted (defensive; not an API shape)
		}
		s, ok := value.(string)
		if !ok {
			return value, nil // schema validation reports the wrong type
		}
		if s == RedactedSentinel {
			prevVal, ok := prevSecrets[path]
			if !ok {
				issues = append(issues, secretIssue(path, "value is redacted but no stored secret exists at this path; provide the actual value"))
				return value, nil
			}
			if _, isEnc := encValue(prevVal); isEnc {
				return prevVal, nil
			}
			// Legacy plaintext from before secret encryption: encrypt it now.
			s, _ = prevVal.(string)
		}
		ct, err := cipher.Encrypt([]byte(s))
		if err != nil {
			if cipher.Configured() {
				return nil, fmt.Errorf("encrypt secret at %s: %w", path, err)
			}
			issues = append(issues, secretIssue(path,
				fmt.Sprintf("%v; secret fields cannot be stored without it (example key: %s)",
					crypto.ErrNotConfigured, crypto.NewRandomKeyBase64())))
			return value, nil
		}
		return newEncValue(ct), nil
	})
	if err != nil {
		return Graph{}, nil, err
	}
	if len(issues) > 0 {
		return Graph{}, issues, nil
	}
	return out, nil, nil
}

// RedactGraphSecrets replaces every password-marked value (encrypted markers
// and legacy plaintext alike) with RedactedSentinel — applied whenever a
// stored graph leaves the backend.
func RedactGraphSecrets(g Graph) Graph {
	out, _ := transformGraphSecrets(g, func(_ string, value any) (any, error) {
		if value == nil {
			return value, nil
		}
		return RedactedSentinel, nil
	})
	return out
}

// DecryptGraphSecrets replaces encrypted markers with their plaintext —
// applied only where the real values are needed: rendering the forwarding /
// edge configs and `otelcol validate`. Legacy plaintext passes through.
func DecryptGraphSecrets(g Graph, cipher *crypto.Cipher) (Graph, error) {
	return transformGraphSecrets(g, func(path string, value any) (any, error) {
		ct, ok := encValue(value)
		if !ok {
			return value, nil
		}
		pt, err := cipher.Decrypt(ct)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret at %s: %w", path, err)
		}
		return string(pt), nil
	})
}
