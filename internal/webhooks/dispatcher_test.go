package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/crypto"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeStore implements the dispatcher's Store.
type fakeStore struct {
	mu       sync.Mutex
	webhooks []store.Webhook
	agent    store.Agent
	agentErr error
}

func (f *fakeStore) ListWebhooks(context.Context) ([]store.Webhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.Webhook(nil), f.webhooks...), nil
}

func (f *fakeStore) GetAgent(context.Context, uuid.UUID) (store.Agent, error) {
	return f.agent, f.agentErr
}

func newCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.New(crypto.NewRandomKeyBase64())
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return c
}

func TestSignature(t *testing.T) {
	secret := []byte("whsec")
	body := []byte(`{"event":"test"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := Signature(secret, body); got != want {
		t.Fatalf("Signature = %q, want %q", got, want)
	}
}

func TestDeliverSignsWhenSecretPresent(t *testing.T) {
	cipher := newCipher(t)
	enc, err := cipher.Encrypt([]byte("top-secret"))
	if err != nil {
		t.Fatal(err)
	}

	var gotSig, gotEvent, gotDelivery string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Otelfleet-Signature")
		gotEvent = r.Header.Get("X-Otelfleet-Event")
		gotDelivery = r.Header.Get("X-Otelfleet-Delivery")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(&fakeStore{}, cipher, testLogger())
	body := []byte(`{"event":"agent_offline"}`)
	status, err := d.Deliver(context.Background(), store.Webhook{URL: srv.URL, SecretEnc: enc}, "agent_offline", body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("Deliver: status=%d err=%v", status, err)
	}
	if gotSig != Signature([]byte("top-secret"), gotBody) {
		t.Errorf("signature header %q does not match body HMAC", gotSig)
	}
	if gotEvent != "agent_offline" {
		t.Errorf("event header = %q", gotEvent)
	}
	if _, err := uuid.Parse(gotDelivery); err != nil {
		t.Errorf("delivery header %q is not a uuid", gotDelivery)
	}
}

func TestDeliverUnsignedWhenNoSecret(t *testing.T) {
	var hadSig bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hadSig = r.Header.Get("X-Otelfleet-Signature") != ""
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	d := New(&fakeStore{}, nil, testLogger()) // nil cipher, no secret → must not touch it
	status, err := d.Deliver(context.Background(), store.Webhook{URL: srv.URL}, "test", []byte("{}"))
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("Deliver: status=%d err=%v", status, err)
	}
	if hadSig {
		t.Error("unsigned webhook must not send a signature header")
	}
}

func TestSendTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	d := New(&fakeStore{}, nil, testLogger())

	ok, msg := d.SendTest(context.Background(), store.Webhook{URL: srv.URL, Name: "x"})
	if !ok {
		t.Fatalf("SendTest ok=false: %s", msg)
	}

	ok, _ = d.SendTest(context.Background(), store.Webhook{URL: "http://127.0.0.1:1/dead"})
	if ok {
		t.Error("SendTest against a dead endpoint must report failure")
	}
}

func TestDispatchFiltersBySubscriptionAndEnabled(t *testing.T) {
	var offlineHits, otherHits atomic.Int32
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		offlineHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer offline.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()

	fs := &fakeStore{
		agentErr: context.Canceled, // force the IDs-only payload path
		webhooks: []store.Webhook{
			{ID: uuid.New(), URL: offline.URL, Events: []string{store.WebhookEventAgentOffline}, Enabled: true},
			{ID: uuid.New(), URL: other.URL, Events: []string{store.WebhookEventAgentUnhealthy}, Enabled: true},
			{ID: uuid.New(), URL: offline.URL, Events: []string{store.WebhookEventAgentOffline}, Enabled: false},
		},
	}
	d := New(fs, nil, testLogger())
	d.dispatch(context.Background(), Event{Type: store.WebhookEventAgentOffline, AgentID: uuid.New()})
	d.wg.Wait()

	if offlineHits.Load() != 1 {
		t.Errorf("offline webhook hits = %d, want 1 (enabled+subscribed only)", offlineHits.Load())
	}
	if otherHits.Load() != 0 {
		t.Errorf("unrelated-event webhook hits = %d, want 0", otherHits.Load())
	}
}

func TestDeliverWithRetryRecoversAfter500(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(&fakeStore{}, nil, testLogger())
	d.backoff = []time.Duration{time.Millisecond} // fast retry for the test
	d.deliverWithRetry(context.Background(), store.Webhook{ID: uuid.New(), URL: srv.URL}, "test", []byte("{}"))
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2 (500 then 200)", attempts.Load())
	}
}

func TestDeliverWithRetryStopsOn4xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	d := New(&fakeStore{}, nil, testLogger())
	d.backoff = []time.Duration{time.Millisecond, time.Millisecond}
	d.deliverWithRetry(context.Background(), store.Webhook{ID: uuid.New(), URL: srv.URL}, "test", []byte("{}"))
	if attempts.Load() != 1 {
		t.Errorf("attempts = %d, want 1 (4xx is permanent, no retry)", attempts.Load())
	}
}

func TestSlackChannelFormatsMessageAndSkipsSignature(t *testing.T) {
	name := "edge-1"
	cname := "ACME"
	cid := uuid.New()
	var gotBody []byte
	var gotSig, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Otelfleet-Signature")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// A Slack channel with a (spurious) secret must still not sign, and must
	// send a Slack message body {"text": ...} rather than the generic payload.
	cipher := newCipher(t)
	enc, err := cipher.Encrypt([]byte("ignored"))
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeStore{
		agent: store.Agent{Name: &name, Class: store.AgentClassEdge, CustomerID: &cid, CustomerName: &cname},
		webhooks: []store.Webhook{
			{ID: uuid.New(), Type: store.WebhookTypeSlack, URL: srv.URL, SecretEnc: enc, Enabled: true, Events: []string{store.WebhookEventAgentUnhealthy}},
		},
	}
	d := New(fs, cipher, testLogger())
	d.dispatch(context.Background(), Event{Type: store.WebhookEventAgentUnhealthy, AgentID: uuid.New(), OccurredAt: time.Unix(0, 0).UTC(), Detail: map[string]any{"lastError": "boom"}})
	d.wg.Wait()

	if gotSig != "" {
		t.Errorf("Slack delivery must not carry an HMAC signature, got %q", gotSig)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q", gotContentType)
	}
	var msg struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(gotBody, &msg); err != nil {
		t.Fatalf("Slack body is not JSON: %v (%s)", err, gotBody)
	}
	for _, want := range []string{"Agent unhealthy", "edge-1", "ACME", "boom"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("Slack message missing %q; got:\n%s", want, msg.Text)
		}
	}
}

func TestSlackMessageColorsBySeverity(t *testing.T) {
	// An alert payload with a severity renders as a coloured Slack attachment.
	msg := slackMessage(Payload{
		Event:      AlertFiring,
		OccurredAt: time.Unix(0, 0).UTC(),
		Detail:     map[string]any{"severity": store.AlertSeverityCritical, "rule": "cpu hot"},
	})
	atts, ok := msg["attachments"].([]map[string]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("expected one attachment, got %#v", msg)
	}
	if atts[0]["color"] != severityColor(store.AlertSeverityCritical) {
		t.Errorf("attachment color = %v, want critical color", atts[0]["color"])
	}
	text, _ := atts[0]["text"].(string)
	if !strings.Contains(text, "cpu hot") || !strings.Contains(text, "Alert firing") {
		t.Errorf("attachment text missing rule/title: %q", text)
	}
	// A payload without severity stays a plain {text} message.
	plain := slackMessage(Payload{Event: "test", OccurredAt: time.Unix(0, 0).UTC()})
	if _, ok := plain["text"].(string); !ok {
		t.Errorf("non-severity message should be plain text, got %#v", plain)
	}
}

func TestBuildPayloadEnrichesFromStore(t *testing.T) {
	name := "edge-1"
	cname := "ACME"
	cid := uuid.New()
	fs := &fakeStore{agent: store.Agent{Name: &name, Class: store.AgentClassEdge, CustomerID: &cid, CustomerName: &cname}}
	d := New(fs, nil, testLogger())

	p := d.buildPayload(context.Background(), Event{Type: store.WebhookEventAgentOffline, AgentID: uuid.New(), Detail: map[string]any{"k": "v"}})
	if p.Agent == nil || p.Agent.Name == nil || *p.Agent.Name != "edge-1" {
		t.Fatalf("payload agent not enriched: %+v", p.Agent)
	}
	if p.Agent.CustomerName == nil || *p.Agent.CustomerName != "ACME" {
		t.Error("customer name not enriched")
	}
	if p.Detail["k"] != "v" {
		t.Error("detail not carried through")
	}
}

func TestPublishDropsOldestOnOverflow(t *testing.T) {
	d := New(&fakeStore{}, nil, testLogger())
	// Fill beyond capacity without a running consumer: never blocks.
	for i := 0; i < queueSize+50; i++ {
		d.Publish(Event{Type: store.WebhookEventAgentOffline, AgentID: uuid.New()})
	}
	if len(d.queue) > queueSize {
		t.Fatalf("queue length %d exceeds capacity %d", len(d.queue), queueSize)
	}
}
