package opamp

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/open-telemetry/opamp-go/protobufs"

	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/tenants"
)

// capsWithConnSettings is testCaps plus AcceptsOpAMPConnectionSettings, so the
// handler will offer a per-agent token.
var capsWithConnSettings = testCaps |
	uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsOpAMPConnectionSettings)

func describeMsgCaps(uid []byte, caps uint64) *protobufs.AgentToServer {
	m := describeMsg(uid)
	m.Capabilities = caps
	return m
}

func authHeaderReq(token string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://x/v1/opamp", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestAuthenticateAgentToken(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	gen, _ := tenants.GenerateAgentToken()
	agentID := uuid.New()
	custID := uuid.New()
	f.tokenAgents = []store.AgentAuth{
		{AgentID: agentID, CustomerID: custID, TokenHash: gen.Hash, CustomerStatus: store.CustomerActive},
	}

	auth, err := h.Authenticate(authHeaderReq(gen.Secret))
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !auth.ViaAgentToken || auth.AgentID == nil || *auth.AgentID != agentID {
		t.Errorf("want per-agent auth for %v, got %+v", agentID, auth)
	}
	if auth.CustomerID != custID {
		t.Errorf("customer = %v, want %v", auth.CustomerID, custID)
	}

	// Unknown token → error.
	other, _ := tenants.GenerateAgentToken()
	if _, err := h.Authenticate(authHeaderReq(other.Secret)); err == nil {
		t.Error("unknown agent token must be rejected")
	}

	// Suspended customer → error.
	f.tokenAgents[0].CustomerStatus = store.CustomerSuspended
	if _, err := h.Authenticate(authHeaderReq(gen.Secret)); err == nil {
		t.Error("suspended customer must be rejected")
	}
}

func TestOffersAgentTokenOnBootstrapConnect(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	auth := testAuth() // bootstrap auth (ViaAgentToken=false)
	uid := testUID()

	resp := h.HandleMessage(context.Background(), &fakeConn{}, auth, describeMsgCaps(uid, capsWithConnSettings))

	if resp.ConnectionSettings == nil || resp.ConnectionSettings.Opamp == nil {
		t.Fatal("expected an OpAMP connection-settings offer")
	}
	if resp.Capabilities&uint64(protobufs.ServerCapabilities_ServerCapabilities_OffersConnectionSettings) == 0 {
		t.Error("response must advertise OffersConnectionSettings")
	}
	var authHeader string
	for _, hdr := range resp.ConnectionSettings.Opamp.GetHeaders().GetHeaders() {
		if hdr.GetKey() == "Authorization" {
			authHeader = hdr.GetValue()
		}
	}
	tok, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok {
		t.Fatalf("offer Authorization header malformed: %q", authHeader)
	}
	if _, ok := tenants.ParseAgentTokenPrefix(tok); !ok {
		t.Errorf("offered token %q is not a per-agent token", tok)
	}
	// The issued token must have been persisted for the enrolled agent.
	if len(f.enrolled) != 1 {
		t.Fatalf("want 1 enrollment, got %d", len(f.enrolled))
	}
	if _, stored := f.agentTokens[f.enrolled[0].ID]; !stored {
		t.Error("SetAgentToken was not called for the enrolled agent")
	}
}

func TestNoAgentTokenOfferWithoutCapability(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	// testCaps lacks AcceptsOpAMPConnectionSettings.
	resp := h.HandleMessage(context.Background(), &fakeConn{}, testAuth(), describeMsg(testUID()))
	if resp.ConnectionSettings != nil {
		t.Error("must not offer a token to an agent that cannot accept connection settings")
	}
	if len(f.agentTokens) != 0 {
		t.Error("must not issue a token when no offer is made")
	}
}

func TestNoAgentTokenOfferOnAgentTokenConnection(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	uid := testUID()
	agentID := uuid.New()
	custID := uuid.New()
	// Pre-existing agent already known by instance_uid.
	f.agents[string(uid)] = store.Agent{ID: agentID, InstanceUID: uid, CustomerID: &custID, Class: store.AgentClassEdge}

	auth := ConnAuth{CustomerID: custID, AgentID: &agentID, ViaAgentToken: true}
	resp := h.HandleMessage(context.Background(), &fakeConn{}, auth, describeMsgCaps(uid, capsWithConnSettings))

	if resp.ConnectionSettings != nil {
		t.Error("a connection already using a per-agent token must not be re-offered one")
	}
}

func TestAgentTokenInstanceMismatchRejected(t *testing.T) {
	f := newFakeStore()
	h := testHandler(f)
	uid := testUID()
	realAgent := uuid.New()
	custID := uuid.New()
	f.agents[string(uid)] = store.Agent{ID: realAgent, InstanceUID: uid, CustomerID: &custID, Class: store.AgentClassEdge}

	// Token belongs to a DIFFERENT agent than the instance_uid resolves to.
	otherAgent := uuid.New()
	auth := ConnAuth{CustomerID: custID, AgentID: &otherAgent, ViaAgentToken: true}
	conn := &fakeConn{}
	resp := h.HandleMessage(context.Background(), conn, auth, describeMsgCaps(uid, capsWithConnSettings))

	if resp.ErrorResponse == nil {
		t.Fatal("expected rejection when a per-agent token is used for another instance_uid")
	}
	if !conn.disconnected {
		t.Error("mismatched connection must be disconnected")
	}
}
