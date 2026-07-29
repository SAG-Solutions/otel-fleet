package opamp

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/open-telemetry/opamp-go/server/types"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

// --- fakes ---

type fakeConn struct {
	mu           sync.Mutex
	sent         []*protobufs.ServerToAgent
	disconnected bool
}

func (c *fakeConn) Connection() net.Conn { return nil }
func (c *fakeConn) Send(_ context.Context, msg *protobufs.ServerToAgent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	return nil
}
func (c *fakeConn) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnected = true
	return nil
}

var _ types.Connection = (*fakeConn)(nil)

// fakeStore records fleet mutations; agents are kept in memory by instance UID.
type fakeStore struct {
	mu     sync.Mutex
	agents map[string]store.Agent

	enrolled     []store.NewAgent
	connected    []bool // sequence of SetAgentConnected values
	rcStatuses   []string
	rcEvents     []string
	healthEvents []string
	effective    []string
	assigned     [][]byte
	acked        [][]byte
	touched      []map[uuid.UUID]time.Time

	agentTokens  map[uuid.UUID]string // agentID -> stored token prefix
	tokenAgents  []store.AgentAuth    // returned by AgentsByTokenPrefix
}

func newFakeStore() *fakeStore {
	return &fakeStore{agents: map[string]store.Agent{}, agentTokens: map[uuid.UUID]string{}}
}

func (f *fakeStore) ActiveBootstrapTokensByPrefix(context.Context, string) ([]store.EnrollToken, error) {
	return nil, nil
}

func (f *fakeStore) AgentsByTokenPrefix(_ context.Context, prefix string) ([]store.AgentAuth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.AgentAuth
	for _, a := range f.tokenAgents {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeStore) SetAgentToken(_ context.Context, id uuid.UUID, prefix string, _ []byte, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentTokens[id] = prefix
	return nil
}

func (f *fakeStore) EnrollAgent(_ context.Context, a store.NewAgent) (store.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.agents[string(a.InstanceUID)]; ok {
		return store.Agent{}, store.ErrConflict
	}
	f.enrolled = append(f.enrolled, a)
	caps := a.Capabilities
	cust := a.CustomerID
	ag := store.Agent{
		ID: a.ID, InstanceUID: a.InstanceUID, CustomerID: &cust, Class: a.Class,
		Name: a.Name, AgentVersion: a.AgentVersion, Capabilities: &caps,
		RemoteConfigStatus: store.RemoteConfigUnset, CreatedAt: time.Now(),
	}
	f.agents[string(a.InstanceUID)] = ag
	return ag, nil
}

func (f *fakeStore) GetAgentByInstanceUID(_ context.Context, uid []byte) (store.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.agents[string(uid)]; ok {
		return a, nil
	}
	return store.Agent{}, store.ErrNotFound
}

func (f *fakeStore) ListAgents(context.Context, store.AgentFilter) ([]store.Agent, error) {
	return nil, nil
}

func (f *fakeStore) UpdateAgentDescription(context.Context, uuid.UUID, *string, *string, []byte, *int64) error {
	return nil
}

func (f *fakeStore) SetAgentConnected(_ context.Context, _ uuid.UUID, connected bool, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connected = append(f.connected, connected)
	return nil
}

func (f *fakeStore) SetAgentAssignedConfig(_ context.Context, _ uuid.UUID, hash []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assigned = append(f.assigned, hash)
	return nil
}

func (f *fakeStore) SetAgentAckedConfig(_ context.Context, _ uuid.UUID, hash []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, hash)
	return nil
}

func (f *fakeStore) SetAgentEffectiveConfig(_ context.Context, _ uuid.UUID, yaml string, _ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.effective = append(f.effective, yaml)
	return nil
}

func (f *fakeStore) SetAgentRemoteConfigStatus(_ context.Context, _ uuid.UUID, status string, _ *string, eventType *string, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rcStatuses = append(f.rcStatuses, status)
	if eventType != nil {
		f.rcEvents = append(f.rcEvents, *eventType)
	}
	return nil
}

func (f *fakeStore) SetAgentHealth(_ context.Context, _ uuid.UUID, _ []byte, _ bool, flipEvent *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if flipEvent != nil {
		f.healthEvents = append(f.healthEvents, *flipEvent)
	}
	return nil
}

func (f *fakeStore) TouchAgents(_ context.Context, seen map[uuid.UUID]time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, seen)
	return nil
}

type fakeRenderer struct{ yaml string }

func (r fakeRenderer) RenderEdgeCurrent(context.Context, uuid.UUID) (string, error) {
	return r.yaml, nil
}

// --- helpers ---

const testConfig = "receivers:\n  otlp: {}\n"

var testCaps = uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig |
	protobufs.AgentCapabilities_AgentCapabilities_ReportsEffectiveConfig |
	protobufs.AgentCapabilities_AgentCapabilities_ReportsHealth)

func testHandler(f *fakeStore) *Handler {
	return NewHandler(f, fakeRenderer{yaml: testConfig}, "", slog.Default())
}

func testAuth() ConnAuth { return ConnAuth{TokenID: uuid.New(), CustomerID: uuid.New()} }

func testUID() []byte {
	u := uuid.New()
	return u[:]
}

func describeMsg(uid []byte) *protobufs.AgentToServer {
	return &protobufs.AgentToServer{
		InstanceUid:  uid,
		Capabilities: testCaps,
		AgentDescription: &protobufs.AgentDescription{
			IdentifyingAttributes: []*protobufs.KeyValue{
				{Key: "host.name", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "edge-1"}}},
				{Key: "service.version", Value: &protobufs.AnyValue{Value: &protobufs.AnyValue_StringValue{StringValue: "0.9.9"}}},
			},
		},
	}
}

// --- tests ---

func TestEnrollmentInsertsAgentAndOffersConfig(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	auth := testAuth()
	conn := &fakeConn{}
	uid := testUID()

	resp := h.HandleMessage(context.Background(), conn, auth, describeMsg(uid))

	if len(f.enrolled) != 1 {
		t.Fatalf("want 1 enrollment, got %d", len(f.enrolled))
	}
	e := f.enrolled[0]
	if e.CustomerID != auth.CustomerID || e.EnrolledVia != auth.TokenID {
		t.Errorf("enrollment binding wrong: customer %v token %v", e.CustomerID, e.EnrolledVia)
	}
	if e.Class != store.AgentClassEdge {
		t.Errorf("class = %q, want edge", e.Class)
	}
	if e.Name == nil || *e.Name != "edge-1" {
		t.Errorf("name = %v, want edge-1", e.Name)
	}
	if e.AgentVersion == nil || *e.AgentVersion != "0.9.9" {
		t.Errorf("version = %v, want 0.9.9", e.AgentVersion)
	}

	// New connection → connected transition.
	if len(f.connected) != 1 || !f.connected[0] {
		t.Errorf("want SetAgentConnected(true), got %v", f.connected)
	}

	// Unknown reported hash → offer the desired config.
	if resp.RemoteConfig == nil {
		t.Fatal("expected a remote config offer on enrollment")
	}
	want := sha256.Sum256([]byte(testConfig))
	if string(resp.RemoteConfig.ConfigHash) != string(want[:]) {
		t.Error("offer hash must be SHA-256 of the body")
	}
	body := resp.RemoteConfig.GetConfig().GetConfigMap()[""]
	if body == nil || string(body.Body) != testConfig || body.ContentType != "text/yaml" {
		t.Errorf("offer body wrong: %+v", body)
	}
	if len(f.assigned) == 0 || string(f.assigned[0]) != string(want[:]) {
		t.Error("assigned_config_hash must be updated with the offered hash")
	}
}

func TestEnrollmentWithoutDescriptionAsksFullState(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	conn := &fakeConn{}

	resp := h.HandleMessage(context.Background(), conn, testAuth(), &protobufs.AgentToServer{InstanceUid: testUID()})
	if len(f.enrolled) != 0 {
		t.Fatal("must not enroll without an AgentDescription")
	}
	if resp.Flags&uint64(protobufs.ServerToAgentFlags_ServerToAgentFlags_ReportFullState) == 0 {
		t.Error("must request full state from an unknown agent")
	}
}

func TestCustomerMismatchRejected(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	uid := testUID()

	// Enroll under customer A.
	authA := testAuth()
	connA := &fakeConn{}
	h.HandleMessage(context.Background(), connA, authA, describeMsg(uid))
	h.HandleConnectionClose(connA)

	// Same instance UID presented with customer B's token.
	authB := testAuth()
	connB := &fakeConn{}
	resp := h.HandleMessage(context.Background(), connB, authB, describeMsg(uid))
	if resp.ErrorResponse == nil {
		t.Fatal("customer mismatch must produce an error response")
	}
	if !connB.disconnected {
		t.Error("customer mismatch must close the connection")
	}
	if h.IsConnected(uid) {
		t.Error("mismatched connection must not register the agent")
	}
}

func TestConnectDisconnectEvents(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	conn := &fakeConn{}
	uid := testUID()

	h.HandleMessage(context.Background(), conn, testAuth(), describeMsg(uid))
	if !h.IsConnected(uid) {
		t.Fatal("agent must be registered as connected")
	}
	h.HandleConnectionClose(conn)
	if h.IsConnected(uid) {
		t.Fatal("agent must be gone after connection close")
	}
	if len(f.connected) != 2 || !f.connected[0] || f.connected[1] {
		t.Errorf("want [true false] connected transitions, got %v", f.connected)
	}

	// Heartbeats must not write rows; only the flusher touches the store.
	h2 := testHandler(f)
	conn2 := &fakeConn{}
	uid2 := testUID()
	h2.HandleMessage(context.Background(), conn2, testAuth(), describeMsg(uid2))
	before := len(f.touched)
	h2.HandleMessage(context.Background(), conn2, testAuth(), &protobufs.AgentToServer{InstanceUid: uid2}) // heartbeat
	if len(f.touched) != before {
		t.Error("heartbeat must not hit the store directly")
	}
	h2.FlushSeen(context.Background())
	if len(f.touched) != before+1 {
		t.Error("FlushSeen must write the batched heartbeat")
	}
}

func TestRemoteConfigStatusTransitions(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	conn := &fakeConn{}
	uid := testUID()
	auth := testAuth()

	h.HandleMessage(context.Background(), conn, auth, describeMsg(uid))
	hash := sha256.Sum256([]byte(testConfig))

	send := func(status protobufs.RemoteConfigStatuses, errMsg string) {
		h.HandleMessage(context.Background(), conn, auth, &protobufs.AgentToServer{
			InstanceUid: uid,
			RemoteConfigStatus: &protobufs.RemoteConfigStatus{
				LastRemoteConfigHash: hash[:],
				Status:               status,
				ErrorMessage:         errMsg,
			},
		})
	}

	send(protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLYING, "")
	send(protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED, "")
	send(protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED, "") // no transition
	send(protobufs.RemoteConfigStatuses_RemoteConfigStatuses_FAILED, "boom")

	// A new hash reaching a terminal status is a fresh apply, even without a
	// status change.
	hash = sha256.Sum256([]byte("new config"))
	send(protobufs.RemoteConfigStatuses_RemoteConfigStatuses_FAILED, "boom again")

	wantStatuses := []string{store.RemoteConfigApplying, store.RemoteConfigApplied, store.RemoteConfigFailed, store.RemoteConfigFailed}
	if len(f.rcStatuses) != len(wantStatuses) {
		t.Fatalf("statuses = %v, want %v", f.rcStatuses, wantStatuses)
	}
	for i, w := range wantStatuses {
		if f.rcStatuses[i] != w {
			t.Errorf("status[%d] = %q, want %q", i, f.rcStatuses[i], w)
		}
	}
	wantEvents := []string{store.AgentEventConfigApplied, store.AgentEventConfigFailed, store.AgentEventConfigFailed}
	if len(f.rcEvents) != len(wantEvents) {
		t.Fatalf("events = %v, want %v", f.rcEvents, wantEvents)
	}
	for i, w := range wantEvents {
		if f.rcEvents[i] != w {
			t.Errorf("event[%d] = %q, want %q", i, f.rcEvents[i], w)
		}
	}
}

func TestDesiredHashComparisonGatesOffer(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	conn := &fakeConn{}
	uid := testUID()
	auth := testAuth()

	// Enrollment offers the config (agent has nothing yet).
	resp := h.HandleMessage(context.Background(), conn, auth, describeMsg(uid))
	if resp.RemoteConfig == nil {
		t.Fatal("expected initial offer")
	}
	hash := sha256.Sum256([]byte(testConfig))

	// Agent acknowledges the config; a re-description must not re-offer.
	h.HandleMessage(context.Background(), conn, auth, &protobufs.AgentToServer{
		InstanceUid: uid,
		RemoteConfigStatus: &protobufs.RemoteConfigStatus{
			LastRemoteConfigHash: hash[:],
			Status:               protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED,
		},
	})
	resp = h.HandleMessage(context.Background(), conn, auth, describeMsg(uid))
	if resp.RemoteConfig != nil {
		t.Error("agent already on the desired hash must not be re-offered")
	}

	// Reconnect of an agent whose acked hash is stale → offer again.
	h.HandleConnectionClose(conn)
	f.mu.Lock()
	ag := f.agents[string(uid)]
	ag.ReportedConfigHash = []byte("stale-hash")
	f.agents[string(uid)] = ag
	f.mu.Unlock()
	conn2 := &fakeConn{}
	resp = h.HandleMessage(context.Background(), conn2, auth, describeMsg(uid))
	if resp.RemoteConfig == nil {
		t.Error("stale agent must be offered the desired config on reconnect")
	}
}

func TestReconnectWithLostStateIsReoffered(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	uid := testUID()
	auth := testAuth()

	// Enroll, ack the config, disconnect.
	conn := &fakeConn{}
	h.HandleMessage(context.Background(), conn, auth, describeMsg(uid))
	hash := sha256.Sum256([]byte(testConfig))
	h.HandleMessage(context.Background(), conn, auth, &protobufs.AgentToServer{
		InstanceUid: uid,
		RemoteConfigStatus: &protobufs.RemoteConfigStatus{
			LastRemoteConfigHash: hash[:],
			Status:               protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED,
		},
	})
	h.HandleConnectionClose(conn)
	f.mu.Lock()
	ag := f.agents[string(uid)]
	ag.ReportedConfigHash = hash[:] // row says the agent runs the desired config
	f.agents[string(uid)] = ag
	f.mu.Unlock()

	// Reconnect reporting UNSET with no hash (agent lost its local state):
	// must be re-offered despite the row's matching reported hash.
	conn2 := &fakeConn{}
	msg := describeMsg(uid)
	msg.RemoteConfigStatus = &protobufs.RemoteConfigStatus{
		Status: protobufs.RemoteConfigStatuses_RemoteConfigStatuses_UNSET,
	}
	resp := h.HandleMessage(context.Background(), conn2, auth, msg)
	if resp.RemoteConfig == nil {
		t.Error("agent reporting UNSET remote config state must be re-offered")
	}
}

func TestEffectiveConfigStored(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	conn := &fakeConn{}
	uid := testUID()
	auth := testAuth()

	h.HandleMessage(context.Background(), conn, auth, describeMsg(uid))
	h.HandleMessage(context.Background(), conn, auth, &protobufs.AgentToServer{
		InstanceUid: uid,
		EffectiveConfig: &protobufs.EffectiveConfig{
			ConfigMap: &protobufs.AgentConfigMap{
				ConfigMap: map[string]*protobufs.AgentConfigFile{
					"": {Body: []byte(testConfig), ContentType: "text/yaml"},
				},
			},
		},
	})
	if len(f.effective) != 1 || f.effective[0] != testConfig {
		t.Errorf("effective config not stored: %v", f.effective)
	}
}

func TestHealthFlipsProduceEvents(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	conn := &fakeConn{}
	uid := testUID()
	auth := testAuth()

	h.HandleMessage(context.Background(), conn, auth, describeMsg(uid))
	send := func(healthy bool) {
		h.HandleMessage(context.Background(), conn, auth, &protobufs.AgentToServer{
			InstanceUid: uid,
			Health:      &protobufs.ComponentHealth{Healthy: healthy},
		})
	}
	send(true)
	send(true) // no flip
	send(false)
	want := []string{store.AgentEventHealthy, store.AgentEventUnhealthy}
	if len(f.healthEvents) != 2 || f.healthEvents[0] != want[0] || f.healthEvents[1] != want[1] {
		t.Errorf("health events = %v, want %v", f.healthEvents, want)
	}
}

func TestEdgeConfigChangedPushesToConnectedAgents(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	auth := testAuth()

	connA, connB := &fakeConn{}, &fakeConn{}
	h.HandleMessage(context.Background(), connA, auth, describeMsg(testUID()))
	h.HandleMessage(context.Background(), connB, auth, describeMsg(testUID()))
	// A different customer's agent must not receive the push.
	otherConn := &fakeConn{}
	h.HandleMessage(context.Background(), otherConn, testAuth(), describeMsg(testUID()))

	pushed, _, err := h.EdgeConfigChanged(context.Background(), auth.CustomerID)
	if err != nil {
		t.Fatal(err)
	}
	if pushed != 2 {
		t.Fatalf("pushed = %d, want 2", pushed)
	}
	for name, c := range map[string]*fakeConn{"A": connA, "B": connB} {
		if len(c.sent) != 1 || c.sent[0].RemoteConfig == nil {
			t.Errorf("conn %s: want 1 pushed remote config, got %d", name, len(c.sent))
		}
	}
	if len(otherConn.sent) != 0 {
		t.Error("other customer's agent must not receive the push")
	}
}
