package pipelines

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/crypto"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

func testCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(crypto.NewRandomKeyBase64())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// secretGraph is an otlphttp exporter with two header secrets (nested map
// under additionalProperties) plus a clickhouse exporter with a direct
// password property.
func secretGraph() Graph {
	return Graph{
		Signals: []string{"logs"},
		Exporters: []Node{
			{Type: "otlphttp", Config: map[string]any{
				"endpoint": "https://backend.example.com:4318",
				"headers": map[string]any{
					"authorization": "Bearer super-secret",
					"x-api-key":     "second-secret",
				},
			}},
			{Type: "clickhouse", Config: map[string]any{
				"endpoint": "tcp://ch.example.com:9000",
				"password": "ch-secret",
			}},
		},
	}
}

func secretAt(t *testing.T, g Graph, exporterIdx int, keys ...string) any {
	t.Helper()
	v := any(g.Exporters[exporterIdx].Config)
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("no map at %v in exporter %d", keys, exporterIdx)
		}
		v = m[k]
	}
	return v
}

func TestEncryptGraphSecretsWalk(t *testing.T) {
	cipher := testCipher(t)
	enc, issues, err := EncryptGraphSecrets(secretGraph(), cipher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}

	// Every password field became an {"$enc": ...} marker.
	for _, path := range [][]string{{"headers", "authorization"}, {"headers", "x-api-key"}} {
		if _, ok := encValue(secretAt(t, enc, 0, path...)); !ok {
			t.Errorf("otlphttp %v is not encrypted: %v", path, secretAt(t, enc, 0, path...))
		}
	}
	if _, ok := encValue(secretAt(t, enc, 1, "password")); !ok {
		t.Error("clickhouse password is not encrypted")
	}
	// Non-secret fields stay plain.
	if got := secretAt(t, enc, 0, "endpoint"); got != "https://backend.example.com:4318" {
		t.Errorf("endpoint changed: %v", got)
	}
	// No plaintext secret in the marshalled graph.
	raw, err := MarshalGraph(enc)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"super-secret", "second-secret", "ch-secret"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("stored graph contains plaintext %q", secret)
		}
	}

	// Decryption restores the plaintext.
	plain, err := DecryptGraphSecrets(enc, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if got := secretAt(t, plain, 0, "headers", "authorization"); got != "Bearer super-secret" {
		t.Errorf("decrypted authorization = %v", got)
	}
	if got := secretAt(t, plain, 1, "password"); got != "ch-secret" {
		t.Errorf("decrypted password = %v", got)
	}
	// The input graph is untouched.
	if got := secretAt(t, secretGraph(), 0, "headers", "authorization"); got != "Bearer super-secret" {
		t.Errorf("input graph mutated: %v", got)
	}
}

func TestEncryptGraphSecretsWithoutMasterKey(t *testing.T) {
	_, issues, err := EncryptGraphSecrets(secretGraph(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) == 0 {
		t.Fatal("no issues without master key")
	}
	if !strings.Contains(issues[0].Message, "master key not configured (set OTEL_FLEET_MASTER_KEY)") {
		t.Errorf("issue message = %q, want master-key hint", issues[0].Message)
	}
	if issues[0].Path == nil || !strings.Contains(*issues[0].Path, "exporters[0].config.headers") {
		t.Errorf("issue path = %v, want a headers path", issues[0].Path)
	}

	// Graphs without secret fields keep working without a key.
	plainGraph := Graph{Signals: []string{"logs"}, Exporters: []Node{{Type: "debug", Config: map[string]any{}}}}
	if _, issues, err = EncryptGraphSecrets(plainGraph, nil, nil); err != nil || len(issues) != 0 {
		t.Errorf("secret-free graph rejected without key: err=%v issues=%+v", err, issues)
	}
}

func TestRedactGraphSecrets(t *testing.T) {
	cipher := testCipher(t)
	enc, _, err := EncryptGraphSecrets(secretGraph(), cipher, nil)
	if err != nil {
		t.Fatal(err)
	}
	red := RedactGraphSecrets(enc)
	if got := secretAt(t, red, 0, "headers", "authorization"); got != RedactedSentinel {
		t.Errorf("redacted authorization = %v, want sentinel", got)
	}
	if got := secretAt(t, red, 1, "password"); got != RedactedSentinel {
		t.Errorf("redacted password = %v, want sentinel", got)
	}
	// Legacy plaintext graphs (pre-encryption rows) redact too.
	red = RedactGraphSecrets(secretGraph())
	if got := secretAt(t, red, 0, "headers", "authorization"); got != RedactedSentinel {
		t.Errorf("legacy plaintext not redacted: %v", got)
	}
}

func TestEncryptGraphSecretsCopyForward(t *testing.T) {
	cipher := testCipher(t)
	prev, _, err := EncryptGraphSecrets(secretGraph(), cipher, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The client edits the graph and sends the sentinel back for one secret
	// and a fresh value for another.
	next := secretGraph()
	next.Exporters[0].Config["headers"].(map[string]any)["authorization"] = RedactedSentinel
	next.Exporters[1].Config["password"] = "rotated-secret"

	enc, issues, err := EncryptGraphSecrets(next, cipher, &prev)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	// Sentinel copied the previous ciphertext verbatim.
	prevJSON, _ := json.Marshal(secretAt(t, prev, 0, "headers", "authorization"))
	nextJSON, _ := json.Marshal(secretAt(t, enc, 0, "headers", "authorization"))
	if string(prevJSON) != string(nextJSON) {
		t.Errorf("sentinel did not copy the stored ciphertext: prev %s next %s", prevJSON, nextJSON)
	}
	plain, err := DecryptGraphSecrets(enc, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if got := secretAt(t, plain, 0, "headers", "authorization"); got != "Bearer super-secret" {
		t.Errorf("copied secret decrypts to %v", got)
	}
	if got := secretAt(t, plain, 1, "password"); got != "rotated-secret" {
		t.Errorf("rotated secret decrypts to %v", got)
	}

	// A sentinel at a path with no stored predecessor is a validation error.
	orphan := secretGraph()
	orphan.Exporters[0].Config["headers"].(map[string]any)["x-new"] = RedactedSentinel
	_, issues, err = EncryptGraphSecrets(orphan, cipher, &prev)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Path == nil || *issues[0].Path != "exporters[0].config.headers.x-new" {
		t.Fatalf("orphan sentinel issues = %+v, want one at exporters[0].config.headers.x-new", issues)
	}

	// Without any previous version every sentinel is an error.
	first := secretGraph()
	first.Exporters[0].Config["headers"].(map[string]any)["authorization"] = RedactedSentinel
	_, issues, err = EncryptGraphSecrets(first, cipher, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) == 0 {
		t.Error("sentinel without previous version must be rejected")
	}
}

// secretStore fakes the store surface Create/CreateVersion/RenderCurrent hit.
type secretStore struct {
	store.Store
	cust        store.Customer
	pipe        store.Pipeline
	prevVersion *store.PipelineVersion
	active      []store.ActivePipeline
	captured    *store.NewPipelineVersion
}

func (f *secretStore) GetCustomer(context.Context, uuid.UUID) (store.Customer, error) {
	return f.cust, nil
}
func (f *secretStore) ListPipelines(context.Context, *uuid.UUID) ([]store.Pipeline, error) {
	return nil, nil
}
func (f *secretStore) ListActivePipelines(context.Context, string, *uuid.UUID) ([]store.ActivePipeline, error) {
	return f.active, nil
}
func (f *secretStore) GetPipeline(context.Context, uuid.UUID) (store.Pipeline, error) {
	return f.pipe, nil
}
func (f *secretStore) GetPipelineVersion(context.Context, uuid.UUID, int) (store.PipelineVersion, error) {
	return *f.prevVersion, nil
}
func (f *secretStore) CreatePipeline(_ context.Context, p store.NewPipeline, v store.NewPipelineVersion, _ []audit.Entry) (store.Pipeline, store.PipelineVersion, error) {
	f.captured = &v
	return store.Pipeline{ID: p.ID, CustomerID: p.CustomerID, Name: p.Name, TargetClass: p.TargetClass},
		store.PipelineVersion{ID: v.ID, PipelineID: p.ID, Version: 1, Graph: v.Graph, RenderedYAML: v.RenderedYAML, ConfigHash: v.ConfigHash, ValidationStatus: v.ValidationStatus}, nil
}
func (f *secretStore) CreatePipelineVersion(_ context.Context, v store.NewPipelineVersion, _ []audit.Entry) (store.PipelineVersion, error) {
	f.captured = &v
	return store.PipelineVersion{ID: v.ID, PipelineID: v.PipelineID, Version: 2, Graph: v.Graph, RenderedYAML: v.RenderedYAML, ConfigHash: v.ConfigHash, ValidationStatus: v.ValidationStatus}, nil
}

func secretService(f *secretStore, cipher *crypto.Cipher) *Service {
	return NewService(f, NewValidator("/nonexistent", slog.Default()), NewPublishDistributor(), cipher, slog.Default())
}

// TestCreateEncryptsAndRedactsStoredArtifacts drives the real Create path:
// the persisted graph carries ciphertext only and the persisted rendered_yaml
// carries the sentinel.
func TestCreateEncryptsAndRedactsStoredArtifacts(t *testing.T) {
	cipher := testCipher(t)
	f := &secretStore{cust: store.Customer{ID: uuid.New(), Slug: "acme", ClientID: "cust_a1b2c3d4", Status: store.CustomerActive}}
	svc := secretService(f, cipher)

	_, res, err := svc.Create(context.Background(), nil, f.cust.ID, "with secrets", ClassForwarding, secretGraph())
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Valid {
		t.Fatalf("create invalid: %+v", res)
	}
	if f.captured == nil {
		t.Fatal("no version persisted")
	}
	if strings.Contains(string(f.captured.Graph), "super-secret") {
		t.Error("persisted graph contains the plaintext secret")
	}
	if !strings.Contains(string(f.captured.Graph), `"$enc"`) {
		t.Error("persisted graph has no encrypted markers")
	}
	if strings.Contains(f.captured.RenderedYAML, "super-secret") {
		t.Error("persisted rendered_yaml contains the plaintext secret")
	}
	if !strings.Contains(f.captured.RenderedYAML, RedactedSentinel) {
		t.Error("persisted rendered_yaml is missing the sentinel")
	}
	if res.RenderedYAML == nil || strings.Contains(*res.RenderedYAML, "super-secret") {
		t.Error("API validation result leaks the plaintext secret")
	}
}

// TestCreateVersionCopyForwardFromLatest: sentinels sent by the editor
// resolve against the latest stored version through the real CreateVersion
// path.
func TestCreateVersionCopyForwardFromLatest(t *testing.T) {
	cipher := testCipher(t)
	prevGraph, _, err := EncryptGraphSecrets(secretGraph(), cipher, nil)
	if err != nil {
		t.Fatal(err)
	}
	prevRaw, err := MarshalGraph(prevGraph)
	if err != nil {
		t.Fatal(err)
	}
	latest := 1
	pipeID := uuid.New()
	f := &secretStore{
		pipe: store.Pipeline{ID: pipeID, CustomerID: uuid.New(), CustomerSlug: "acme", ClientID: "cust_a1b2c3d4",
			Name: "with secrets", TargetClass: ClassForwarding, LatestVersion: &latest},
		prevVersion: &store.PipelineVersion{Graph: prevRaw},
	}
	svc := secretService(f, cipher)

	next := secretGraph()
	next.Exporters[0].Config["headers"].(map[string]any)["authorization"] = RedactedSentinel
	next.Exporters[1].Config["password"] = RedactedSentinel

	_, res, err := svc.CreateVersion(context.Background(), nil, pipeID, next)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Valid {
		t.Fatalf("create version invalid: %+v", res)
	}
	stored, err := ParseGraph(f.captured.Graph)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := DecryptGraphSecrets(stored, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if got := secretAt(t, plain, 0, "headers", "authorization"); got != "Bearer super-secret" {
		t.Errorf("copied header decrypts to %v", got)
	}
	if got := secretAt(t, plain, 1, "password"); got != "ch-secret" {
		t.Errorf("copied password decrypts to %v", got)
	}
}

// TestCreateWithoutMasterKeyRejectsSecretGraphs: plaintext storage is over.
func TestCreateWithoutMasterKeyRejectsSecretGraphs(t *testing.T) {
	f := &secretStore{cust: store.Customer{ID: uuid.New(), Slug: "acme", ClientID: "cust_a1b2c3d4", Status: store.CustomerActive}}
	svc := secretService(f, nil)

	_, res, err := svc.Create(context.Background(), nil, f.cust.ID, "with secrets", ClassForwarding, secretGraph())
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.Valid {
		t.Fatal("secret graph accepted without master key")
	}
	if !strings.Contains(res.Issues[0].Message, "master key not configured (set OTEL_FLEET_MASTER_KEY)") {
		t.Errorf("issue = %q, want master-key error", res.Issues[0].Message)
	}
	if f.captured != nil {
		t.Error("version persisted despite missing master key")
	}
}

// TestRenderCurrentDecryptsSecrets: the forwarding config (ops endpoint,
// distributor, otelcol validate) carries the real plaintext values.
func TestRenderCurrentDecryptsSecrets(t *testing.T) {
	cipher := testCipher(t)
	enc, _, err := EncryptGraphSecrets(secretGraph(), cipher, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := MarshalGraph(enc)
	if err != nil {
		t.Fatal(err)
	}
	f := &secretStore{active: []store.ActivePipeline{{
		PipelineID: uuid.New(), PipelineName: "with secrets", CustomerID: uuid.New(),
		CustomerSlug: "acme", ClientID: "cust_a1b2c3d4", Graph: raw,
	}}}
	svc := secretService(f, cipher)

	cfg, err := svc.RenderCurrent(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, "Bearer super-secret") {
		t.Error("rendered forwarding config is missing the decrypted header")
	}
	if strings.Contains(cfg, RedactedSentinel) || strings.Contains(cfg, `$enc`) {
		t.Error("rendered forwarding config contains redaction/encryption artifacts")
	}

	// Edge configs decrypt the same way.
	edgeCfg, err := svc.RenderEdgeCurrent(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(edgeCfg, "Bearer super-secret") {
		t.Error("rendered edge config is missing the decrypted header")
	}
}
