// Package opamp implements the OpAMP server for edge-agent fleet management:
// bootstrap-token authentication, agent enrollment and lifecycle tracking,
// and pushing the customers' rendered edge configs to their agents.
package opamp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/open-telemetry/opamp-go/protobufs"
	"github.com/open-telemetry/opamp-go/server/types"

	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/tenants"
)

// Store is the persistence subset the OpAMP module needs.
type Store interface {
	ActiveBootstrapTokensByPrefix(ctx context.Context, prefix string) ([]store.EnrollToken, error)
	AgentsByTokenPrefix(ctx context.Context, prefix string) ([]store.AgentAuth, error)
	SetAgentToken(ctx context.Context, id uuid.UUID, prefix string, hash []byte, at time.Time) error
	EnrollAgent(ctx context.Context, a store.NewAgent) (store.Agent, error)
	GetAgentByInstanceUID(ctx context.Context, instanceUID []byte) (store.Agent, error)
	ListAgents(ctx context.Context, f store.AgentFilter) ([]store.Agent, error)
	UpdateAgentDescription(ctx context.Context, id uuid.UUID, name, agentVersion *string, description []byte, capabilities *int64) error
	SetAgentConnected(ctx context.Context, id uuid.UUID, connected bool, at time.Time) error
	SetAgentAssignedConfig(ctx context.Context, id uuid.UUID, hash []byte) error
	SetAgentAckedConfig(ctx context.Context, id uuid.UUID, hash []byte) error
	SetAgentEffectiveConfig(ctx context.Context, id uuid.UUID, yaml string, hash []byte) error
	SetAgentRemoteConfigStatus(ctx context.Context, id uuid.UUID, status string, errorMessage *string, eventType *string, detail any) error
	SetAgentHealth(ctx context.Context, id uuid.UUID, health []byte, healthy bool, flipEvent *string) error
	TouchAgents(ctx context.Context, seen map[uuid.UUID]time.Time) error
}

// ConfigRenderer renders the desired merged edge config of one customer from
// database state (implemented by *pipelines.Service).
type ConfigRenderer interface {
	RenderEdgeCurrent(ctx context.Context, customerID uuid.UUID) (string, error)
}

// FleetEventSink receives agent lifecycle events for out-of-band consumers
// (the webhook dispatcher). eventType is one of the store.WebhookEventAgent*
// constants. Implementations must not block — the OpAMP message path calls
// this inline.
type FleetEventSink interface {
	AgentEvent(eventType string, agentID, customerID uuid.UUID, detail map[string]any)
}

// ConnAuth is the customer binding of an authenticated OpAMP connection.
type ConnAuth struct {
	TokenID    uuid.UUID // bootstrap token used, if any
	CustomerID uuid.UUID
	// AgentID is set when the connection authenticated with a per-agent token;
	// nil when it used a bootstrap token. ViaAgentToken mirrors this.
	AgentID       *uuid.UUID
	ViaAgentToken bool
}

// serverCapabilities is what this control plane implements.
const serverCapabilities = uint64(protobufs.ServerCapabilities_ServerCapabilities_AcceptsStatus |
	protobufs.ServerCapabilities_ServerCapabilities_OffersRemoteConfig |
	protobufs.ServerCapabilities_ServerCapabilities_AcceptsEffectiveConfig)

// Handler holds the OpAMP message-handling logic, factored out of the
// transport so it is testable without a live WebSocket.
type Handler struct {
	store  Store
	render ConfigRenderer
	reg    *registry
	events FleetEventSink // nil: no subscriber
	// publicEndpoint is the externally reachable OpAMP URL offered to agents in
	// per-agent-token connection settings; empty = offer headers only (the
	// agent keeps its current endpoint).
	publicEndpoint string
	log            *slog.Logger
}

// NewHandler wires the message handler. publicEndpoint is the externally
// reachable OpAMP WebSocket URL (OTEL_FLEET_OPAMP_PUBLIC_ENDPOINT); it may be
// empty, in which case per-agent-token offers carry only the new header.
func NewHandler(st Store, render ConfigRenderer, publicEndpoint string, log *slog.Logger) *Handler {
	return &Handler{store: st, render: render, reg: newRegistry(), publicEndpoint: publicEndpoint, log: log}
}

// SetEventSink registers the fleet-event subscriber (webhook dispatcher).
// Must be called before the server starts accepting connections.
func (h *Handler) SetEventSink(sink FleetEventSink) { h.events = sink }

// emitEvent forwards an agent lifecycle event to the sink, if any.
func (h *Handler) emitEvent(eventType string, agentID, customerID uuid.UUID, detail map[string]any) {
	if h.events != nil {
		h.events.AgentEvent(eventType, agentID, customerID, detail)
	}
}

// Authenticate validates the bearer token of an incoming OpAMP request. Two
// credential kinds are accepted:
//
//   - a per-agent token (otm_at_<prefix>_<secret>) issued to an enrolled agent;
//   - a per-customer bootstrap token (otm_bt_<prefix>_<secret>) for enrollment
//     and as a fallback for agents that have not flipped to a per-agent token.
//
// Both are looked up by their clear prefix and compared in constant time.
func (h *Handler) Authenticate(r *http.Request) (ConnAuth, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return ConnAuth{}, errors.New("missing bearer token")
	}
	if prefix, ok := tenants.ParseAgentTokenPrefix(token); ok {
		return h.authenticateAgentToken(r.Context(), prefix, token)
	}
	if prefix, ok := tenants.ParseBootstrapTokenPrefix(token); ok {
		return h.authenticateBootstrapToken(r.Context(), prefix, token)
	}
	return ConnAuth{}, errors.New("malformed token")
}

func (h *Handler) authenticateAgentToken(ctx context.Context, prefix, token string) (ConnAuth, error) {
	agents, err := h.store.AgentsByTokenPrefix(ctx, prefix)
	if err != nil {
		return ConnAuth{}, fmt.Errorf("agent token lookup failed: %w", err)
	}
	hash := tenants.HashAPIKey(token)
	for i := range agents {
		if subtle.ConstantTimeCompare(hash, agents[i].TokenHash) == 1 {
			if agents[i].CustomerStatus != store.CustomerActive {
				return ConnAuth{}, fmt.Errorf("customer is %s", agents[i].CustomerStatus)
			}
			id := agents[i].AgentID
			return ConnAuth{CustomerID: agents[i].CustomerID, AgentID: &id, ViaAgentToken: true}, nil
		}
	}
	return ConnAuth{}, errors.New("unknown agent token")
}

func (h *Handler) authenticateBootstrapToken(ctx context.Context, prefix, token string) (ConnAuth, error) {
	toks, err := h.store.ActiveBootstrapTokensByPrefix(ctx, prefix)
	if err != nil {
		return ConnAuth{}, fmt.Errorf("token lookup failed: %w", err)
	}
	hash := tenants.HashAPIKey(token)
	var match *store.EnrollToken
	for i := range toks {
		if subtle.ConstantTimeCompare(hash, toks[i].TokenHash) == 1 {
			match = &toks[i]
			break
		}
	}
	if match == nil {
		return ConnAuth{}, errors.New("unknown bootstrap token")
	}
	if err := tenants.TokenUsable(*match, time.Now()); err != nil {
		return ConnAuth{}, err
	}
	return ConnAuth{TokenID: match.TokenID, CustomerID: match.CustomerID}, nil
}

// HandleMessage processes one AgentToServer message on an authenticated
// connection and builds the response.
func (h *Handler) HandleMessage(ctx context.Context, conn types.Connection, auth ConnAuth, msg *protobufs.AgentToServer) *protobufs.ServerToAgent {
	resp := &protobufs.ServerToAgent{
		InstanceUid:  msg.GetInstanceUid(),
		Capabilities: serverCapabilities,
	}
	uid := msg.GetInstanceUid()
	if len(uid) == 0 {
		resp.ErrorResponse = errorResponse("missing instance_uid")
		return resp
	}
	now := time.Now()

	prev := h.reg.get(uid)
	newConn := prev == nil || prev.conn != conn

	// Resolve (or enroll) the agent behind this instance UID.
	st := prev
	if st == nil {
		var reject bool
		var askFullState bool
		st, askFullState, reject = h.resolveAgent(ctx, conn, auth, uid, msg)
		if reject {
			resp.ErrorResponse = errorResponse("instance uid is bound to another customer")
			if conn != nil {
				_ = conn.Disconnect()
			}
			return resp
		}
		if askFullState {
			resp.Flags |= uint64(protobufs.ServerToAgentFlags_ServerToAgentFlags_ReportFullState)
			return resp
		}
	} else if st.customerID != auth.CustomerID {
		h.log.Warn("opamp: connection customer does not match agent binding",
			"instance_uid", hex.EncodeToString(uid), "agent", st.agentID,
			"agent_customer", st.customerID, "conn_customer", auth.CustomerID)
		resp.ErrorResponse = errorResponse("instance uid is bound to another customer")
		if conn != nil {
			_ = conn.Disconnect()
		}
		return resp
	}
	// A per-agent token authorizes exactly its own agent: reject it if the
	// message's instance_uid resolves to a different agent.
	if auth.ViaAgentToken && auth.AgentID != nil && st.agentID != *auth.AgentID {
		h.log.Warn("opamp: per-agent token presented for a different instance_uid",
			"token_agent", *auth.AgentID, "resolved_agent", st.agentID,
			"instance_uid", hex.EncodeToString(uid))
		resp.ErrorResponse = errorResponse("token does not match this agent")
		if conn != nil {
			_ = conn.Disconnect()
		}
		return resp
	}

	st.conn = conn
	st.lastSeen = now
	st.dirtySeen = true

	// Capabilities are only re-sent when they change.
	if msg.GetCapabilities() != 0 {
		st.capabilities = msg.GetCapabilities()
	}

	// Description refresh for known agents (enrollment already stored it).
	if msg.GetAgentDescription() != nil && prev != nil {
		name, version := agentIdentity(msg.GetAgentDescription())
		desc, err := descriptionJSON(msg.GetAgentDescription())
		if err == nil {
			var caps *int64
			if msg.GetCapabilities() != 0 {
				c := int64(msg.GetCapabilities())
				caps = &c
			}
			if err := h.store.UpdateAgentDescription(ctx, st.agentID, name, version, desc, caps); err != nil {
				h.log.Error("opamp: update agent description failed", "agent", st.agentID, "err", err)
			}
		}
	}

	h.reg.put(st)

	// Connected transition: row update + agent_events row, once per connection.
	if newConn {
		if err := h.store.SetAgentConnected(ctx, st.agentID, true, now); err != nil {
			h.log.Error("opamp: mark agent connected failed", "agent", st.agentID, "err", err)
		}
	}

	if rcs := msg.GetRemoteConfigStatus(); rcs != nil {
		h.handleRemoteConfigStatus(ctx, st, rcs)
	}
	if ec := msg.GetEffectiveConfig(); ec != nil {
		h.handleEffectiveConfig(ctx, st, ec)
	}
	if health := msg.GetHealth(); health != nil {
		h.handleHealth(ctx, st, health)
	}

	// Config offer: on connect and on (re-)described agents, compare the
	// desired config hash against what the agent last acknowledged/reported
	// and offer the rendered edge config when they differ. Activation pushes
	// go through EdgeConfigChanged instead.
	if newConn || msg.GetAgentDescription() != nil {
		h.maybeOfferConfig(ctx, st, resp)
	}

	// Issue and offer a per-agent token to connections still using the
	// bootstrap token, so they can flip off it — revoking the bootstrap token
	// then never disturbs an enrolled agent.
	if newConn && !auth.ViaAgentToken {
		h.maybeOfferAgentToken(ctx, st, resp)
	}

	return resp
}

// maybeOfferAgentToken mints a fresh per-agent token, stores its hash (the
// previous one stops working immediately), and offers it via
// OpAMPConnectionSettings so the supervisor reconnects presenting it. Only
// agents that advertise AcceptsOpAMPConnectionSettings are offered one; others
// keep using the bootstrap token.
func (h *Handler) maybeOfferAgentToken(ctx context.Context, st *agentConn, resp *protobufs.ServerToAgent) {
	if st.tokenOffered {
		return
	}
	if st.capabilities&uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsOpAMPConnectionSettings) == 0 {
		return
	}
	gen, err := tenants.GenerateAgentToken()
	if err != nil {
		h.log.Error("opamp: generate agent token failed", "agent", st.agentID, "err", err)
		return
	}
	if err := h.store.SetAgentToken(ctx, st.agentID, gen.Prefix, gen.Hash, time.Now()); err != nil {
		h.log.Error("opamp: store agent token failed", "agent", st.agentID, "err", err)
		return
	}
	st.tokenOffered = true
	h.reg.update(st.instanceUID, func(a *agentConn) { a.tokenOffered = true })

	opampSettings := &protobufs.OpAMPConnectionSettings{
		Headers: &protobufs.Headers{Headers: []*protobufs.Header{
			{Key: "Authorization", Value: "Bearer " + gen.Secret},
		}},
	}
	if h.publicEndpoint != "" {
		opampSettings.DestinationEndpoint = h.publicEndpoint
	}
	offerHash := sha256.Sum256([]byte(gen.Prefix)) // stable per issued token
	resp.Capabilities |= uint64(protobufs.ServerCapabilities_ServerCapabilities_OffersConnectionSettings)
	resp.ConnectionSettings = &protobufs.ConnectionSettingsOffers{
		Hash:  offerHash[:],
		Opamp: opampSettings,
	}
	h.log.Info("opamp: offering per-agent token", "agent", st.agentID, "token_prefix", gen.Prefix)
}

// resolveAgent looks up the agent for an instance UID this server has no
// in-memory state for, enrolling it when unknown. reject is true on a
// customer mismatch; askFullState is true when the agent is unknown and the
// message carries no AgentDescription to enroll from.
func (h *Handler) resolveAgent(ctx context.Context, conn types.Connection, auth ConnAuth, uid []byte, msg *protobufs.AgentToServer) (st *agentConn, askFullState, reject bool) {
	agent, err := h.store.GetAgentByInstanceUID(ctx, uid)
	switch {
	case err == nil:
		if agent.CustomerID == nil || *agent.CustomerID != auth.CustomerID {
			h.log.Warn("opamp: agent enrolled under a different customer",
				"instance_uid", hex.EncodeToString(uid), "agent", agent.ID, "conn_customer", auth.CustomerID)
			return nil, false, true
		}
		st = &agentConn{
			conn:               conn,
			instanceUID:        append([]byte(nil), uid...),
			agentID:            agent.ID,
			customerID:         auth.CustomerID,
			remoteConfigStatus: agent.RemoteConfigStatus,
			healthy:            agent.Healthy,
			lastAckedHash:      agent.AckedConfigHash,
		}
		if agent.Capabilities != nil {
			st.capabilities = uint64(*agent.Capabilities)
		}
		return st, false, false

	case errors.Is(err, store.ErrNotFound):
		if msg.GetAgentDescription() == nil {
			return nil, true, false
		}
		name, version := agentIdentity(msg.GetAgentDescription())
		desc, err := descriptionJSON(msg.GetAgentDescription())
		if err != nil {
			h.log.Error("opamp: encode agent description failed", "err", err)
			desc = nil
		}
		agent, err := h.store.EnrollAgent(ctx, store.NewAgent{
			ID:           uuid.New(),
			InstanceUID:  append([]byte(nil), uid...),
			CustomerID:   auth.CustomerID,
			Class:        store.AgentClassEdge,
			Name:         name,
			AgentVersion: version,
			Description:  desc,
			Capabilities: int64(msg.GetCapabilities()),
			EnrolledVia:  auth.TokenID,
		})
		if errors.Is(err, store.ErrConflict) {
			// Lost an enrollment race; re-resolve from the winning row.
			return h.resolveAgent(ctx, conn, auth, uid, &protobufs.AgentToServer{InstanceUid: uid})
		}
		if err != nil {
			h.log.Error("opamp: enroll agent failed", "instance_uid", hex.EncodeToString(uid), "err", err)
			return nil, true, false
		}
		h.log.Info("opamp: agent enrolled", "agent", agent.ID, "customer", auth.CustomerID,
			"instance_uid", hex.EncodeToString(uid))
		return &agentConn{
			conn:               conn,
			instanceUID:        append([]byte(nil), uid...),
			agentID:            agent.ID,
			customerID:         auth.CustomerID,
			capabilities:       msg.GetCapabilities(),
			remoteConfigStatus: agent.RemoteConfigStatus,
		}, false, false

	default:
		h.log.Error("opamp: agent lookup failed", "instance_uid", hex.EncodeToString(uid), "err", err)
		return nil, true, false
	}
}

// handleRemoteConfigStatus maps the OpAMP status onto the row and records
// config_applied/config_failed events on transitions. A transition is a
// status change OR a new config hash reaching a terminal status (applied →
// applied with a different hash is a fresh apply, not a repeat).
func (h *Handler) handleRemoteConfigStatus(ctx context.Context, st *agentConn, rcs *protobufs.RemoteConfigStatus) {
	status := mapRemoteConfigStatus(rcs.GetStatus())
	acked := rcs.GetLastRemoteConfigHash()
	newHash := len(acked) > 0 && !bytes.Equal(acked, st.lastAckedHash)
	if len(acked) > 0 {
		st.lastAckedHash = append([]byte(nil), acked...)
		h.reg.update(st.instanceUID, func(a *agentConn) { a.lastAckedHash = st.lastAckedHash })
		// Persist the acknowledged hash: it is the authoritative in-sync signal
		// (compared against assigned_config_hash), and it must survive a
		// reconnect where the agent does not re-report status.
		if newHash {
			if err := h.store.SetAgentAckedConfig(ctx, st.agentID, st.lastAckedHash); err != nil {
				h.log.Error("opamp: persist acked config hash failed", "agent", st.agentID, "err", err)
			}
		}
	} else if status == store.RemoteConfigUnset {
		// The agent explicitly reports having no remote config (e.g. restart
		// with lost state): forget the seeded hash so the connect-time offer
		// re-sends the desired config.
		st.lastAckedHash = nil
		h.reg.update(st.instanceUID, func(a *agentConn) { a.lastAckedHash = nil })
		if err := h.store.SetAgentAckedConfig(ctx, st.agentID, nil); err != nil {
			h.log.Error("opamp: clear acked config hash failed", "agent", st.agentID, "err", err)
		}
	}
	terminal := status == store.RemoteConfigApplied || status == store.RemoteConfigFailed
	if status == st.remoteConfigStatus && !(newHash && terminal) {
		return
	}
	var errMsg *string
	var eventType *string
	detail := map[string]any{"hash": hex.EncodeToString(rcs.GetLastRemoteConfigHash())}
	switch status {
	case store.RemoteConfigApplied:
		e := store.AgentEventConfigApplied
		eventType = &e
	case store.RemoteConfigFailed:
		e := store.AgentEventConfigFailed
		eventType = &e
		if m := rcs.GetErrorMessage(); m != "" {
			errMsg = &m
			detail["error"] = m
		}
	}
	if err := h.store.SetAgentRemoteConfigStatus(ctx, st.agentID, status, errMsg, eventType, detail); err != nil {
		h.log.Error("opamp: set remote config status failed", "agent", st.agentID, "err", err)
		return
	}
	if status == store.RemoteConfigFailed {
		h.emitEvent(store.WebhookEventAgentConfigFailed, st.agentID, st.customerID, detail)
	}
	st.remoteConfigStatus = status
	h.reg.update(st.instanceUID, func(a *agentConn) { a.remoteConfigStatus = status })
}

// handleEffectiveConfig stores the reported config body and its SHA-256.
func (h *Handler) handleEffectiveConfig(ctx context.Context, st *agentConn, ec *protobufs.EffectiveConfig) {
	body, ok := effectiveConfigBody(ec)
	if !ok {
		return
	}
	sum := sha256.Sum256(body)
	if err := h.store.SetAgentEffectiveConfig(ctx, st.agentID, string(body), sum[:]); err != nil {
		h.log.Error("opamp: set effective config failed", "agent", st.agentID, "err", err)
	}
}

// handleHealth stores the health tree and records healthy/unhealthy events on
// flips (including the first report).
func (h *Handler) handleHealth(ctx context.Context, st *agentConn, health *protobufs.ComponentHealth) {
	healthy := health.GetHealthy()
	var flip *string
	if st.healthy == nil || *st.healthy != healthy {
		e := store.AgentEventHealthy
		if !healthy {
			e = store.AgentEventUnhealthy
		}
		flip = &e
	}
	payload, err := healthJSON(health)
	if err != nil {
		h.log.Error("opamp: encode health failed", "agent", st.agentID, "err", err)
		return
	}
	if err := h.store.SetAgentHealth(ctx, st.agentID, payload, healthy, flip); err != nil {
		h.log.Error("opamp: set health failed", "agent", st.agentID, "err", err)
		return
	}
	if flip != nil && !healthy {
		detail := map[string]any{}
		if s := health.GetStatus(); s != "" {
			detail["status"] = s
		}
		if e := health.GetLastError(); e != "" {
			detail["lastError"] = e
		}
		h.emitEvent(store.WebhookEventAgentUnhealthy, st.agentID, st.customerID, detail)
	}
	st.healthy = &healthy
	h.reg.update(st.instanceUID, func(a *agentConn) { a.healthy = &healthy })
}

// maybeOfferConfig renders the customer's desired edge config and attaches a
// remote-config offer to the response when the agent is not running it yet.
func (h *Handler) maybeOfferConfig(ctx context.Context, st *agentConn, resp *protobufs.ServerToAgent) {
	if st.capabilities&uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig) == 0 {
		return
	}
	desired, err := h.render.RenderEdgeCurrent(ctx, st.customerID)
	if err != nil {
		h.log.Error("opamp: render edge config failed", "customer", st.customerID, "err", err)
		return
	}
	hash := sha256.Sum256([]byte(desired))
	if err := h.store.SetAgentAssignedConfig(ctx, st.agentID, hash[:]); err != nil {
		h.log.Error("opamp: set assigned config hash failed", "agent", st.agentID, "err", err)
	}
	if bytes.Equal(hash[:], st.lastAckedHash) {
		return
	}
	resp.RemoteConfig = remoteConfig(desired, hash[:])
	// Also ask for full state: if the agent is already running this config (our
	// acknowledged-hash knowledge is stale, e.g. after a control-plane restart),
	// it will not re-apply and would otherwise never re-report its
	// RemoteConfigStatus — leaving us unable to confirm it is in sync.
	resp.Flags |= uint64(protobufs.ServerToAgentFlags_ServerToAgentFlags_ReportFullState)
	h.log.Info("opamp: offering remote config", "agent", st.agentID,
		"hash", hex.EncodeToString(hash[:]))
}

// HandleConnectionClose marks the agent behind a closed connection as
// disconnected (row + event) and drops it from the registry.
func (h *Handler) HandleConnectionClose(conn types.Connection) {
	st := h.reg.removeConn(conn)
	if st == nil || st.agentID == uuid.Nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	at := st.lastSeen
	if at.IsZero() {
		at = time.Now()
	}
	if err := h.store.SetAgentConnected(ctx, st.agentID, false, at); err != nil {
		h.log.Error("opamp: mark agent disconnected failed", "agent", st.agentID, "err", err)
	}
	h.emitEvent(store.WebhookEventAgentOffline, st.agentID, st.customerID, nil)
}

// EdgeConfigChanged implements pipelines.EdgeNotifier: re-render the
// customer's edge config and push it to all its connected agents. Offline
// agents pick the new config up on reconnect (hash comparison in
// maybeOfferConfig). There is NO automatic rollback on a FAILED status in
// this phase: the supervisor reverts to its last good config locally; the
// control plane records and surfaces the failure (remote_config_status =
// failed + config_failed event).
func (h *Handler) EdgeConfigChanged(ctx context.Context, customerID uuid.UUID) (pushed, offline int, err error) {
	desired, err := h.render.RenderEdgeCurrent(ctx, customerID)
	if err != nil {
		return 0, 0, fmt.Errorf("render edge config: %w", err)
	}
	hash := sha256.Sum256([]byte(desired))

	for _, st := range h.reg.connsForCustomer(customerID) {
		if st.capabilities&uint64(protobufs.AgentCapabilities_AgentCapabilities_AcceptsRemoteConfig) == 0 {
			continue
		}
		if err := h.store.SetAgentAssignedConfig(ctx, st.agentID, hash[:]); err != nil {
			h.log.Error("opamp: set assigned config hash failed", "agent", st.agentID, "err", err)
		}
		if st.conn == nil {
			continue
		}
		msg := &protobufs.ServerToAgent{
			InstanceUid:  st.instanceUID,
			Capabilities: serverCapabilities,
			RemoteConfig: remoteConfig(desired, hash[:]),
			// Request full state so a forced re-sync still yields a fresh
			// RemoteConfigStatus even when the pushed config is unchanged (the
			// supervisor dedupes identical configs and would not re-report).
			Flags: uint64(protobufs.ServerToAgentFlags_ServerToAgentFlags_ReportFullState),
		}
		if err := st.conn.Send(ctx, msg); err != nil {
			// Plain-HTTP transports cannot be pushed to; they poll.
			h.log.Warn("opamp: config push failed", "agent", st.agentID, "err", err)
			continue
		}
		pushed++
	}

	edge := store.AgentClassEdge
	disconnected := false
	agents, err := h.store.ListAgents(ctx, store.AgentFilter{Class: &edge, CustomerID: &customerID, Connected: &disconnected})
	if err != nil {
		h.log.Warn("opamp: count offline agents failed", "customer", customerID, "err", err)
		return pushed, 0, nil
	}
	return pushed, len(agents), nil
}

// IsConnected reports whether the agent with the given instance UID currently
// has a live OpAMP connection (used by the delete-agent 409 check).
func (h *Handler) IsConnected(instanceUID []byte) bool { return h.reg.isConnected(instanceUID) }

// FlushSeen writes all unflushed heartbeat timestamps to the store.
func (h *Handler) FlushSeen(ctx context.Context) {
	seen := h.reg.drainDirtySeen()
	if len(seen) == 0 {
		return
	}
	if err := h.store.TouchAgents(ctx, seen); err != nil {
		h.log.Error("opamp: flush last_seen_at failed", "agents", len(seen), "err", err)
	}
}

// --- protobuf mapping helpers ---

func errorResponse(msg string) *protobufs.ServerErrorResponse {
	return &protobufs.ServerErrorResponse{
		Type:         protobufs.ServerErrorResponseType_ServerErrorResponseType_BadRequest,
		ErrorMessage: msg,
	}
}

// remoteConfig builds the ServerToAgent config offer: a single unnamed YAML
// entry, hashed with SHA-256 over the body.
func remoteConfig(body string, hash []byte) *protobufs.AgentRemoteConfig {
	return &protobufs.AgentRemoteConfig{
		Config: &protobufs.AgentConfigMap{
			ConfigMap: map[string]*protobufs.AgentConfigFile{
				"": {Body: []byte(body), ContentType: "text/yaml"},
			},
		},
		ConfigHash: hash,
	}
}

func mapRemoteConfigStatus(s protobufs.RemoteConfigStatuses) string {
	switch s {
	case protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLYING:
		return store.RemoteConfigApplying
	case protobufs.RemoteConfigStatuses_RemoteConfigStatuses_APPLIED:
		return store.RemoteConfigApplied
	case protobufs.RemoteConfigStatuses_RemoteConfigStatuses_FAILED:
		return store.RemoteConfigFailed
	default:
		return store.RemoteConfigUnset
	}
}

// effectiveConfigBody extracts the reported config body: the unnamed entry
// when present, otherwise the single entry of the map.
func effectiveConfigBody(ec *protobufs.EffectiveConfig) ([]byte, bool) {
	cm := ec.GetConfigMap().GetConfigMap()
	if len(cm) == 0 {
		return nil, false
	}
	if f, ok := cm[""]; ok {
		return f.GetBody(), true
	}
	if len(cm) == 1 {
		for _, f := range cm {
			return f.GetBody(), true
		}
	}
	// Multiple named entries: concatenate deterministically is overkill for
	// this phase; take the unnamed contract seriously and refuse.
	return nil, false
}

// agentIdentity derives the display name (host.name, falling back to
// service.name) and version (service.version) from the identifying
// attributes.
func agentIdentity(d *protobufs.AgentDescription) (name, version *string) {
	var hostName, serviceName string
	for _, kv := range d.GetIdentifyingAttributes() {
		switch kv.GetKey() {
		case "host.name":
			hostName = kv.GetValue().GetStringValue()
		case "service.name":
			serviceName = kv.GetValue().GetStringValue()
		case "service.version":
			if v := kv.GetValue().GetStringValue(); v != "" {
				version = &v
			}
		}
	}
	for _, kv := range d.GetNonIdentifyingAttributes() {
		if kv.GetKey() == "host.name" && hostName == "" {
			hostName = kv.GetValue().GetStringValue()
		}
	}
	if hostName != "" {
		return &hostName, version
	}
	if serviceName != "" {
		return &serviceName, version
	}
	return nil, version
}

// descriptionJSON flattens the AgentDescription into a JSONB payload.
func descriptionJSON(d *protobufs.AgentDescription) ([]byte, error) {
	out := map[string]any{
		"identifying":    attrsToMap(d.GetIdentifyingAttributes()),
		"nonIdentifying": attrsToMap(d.GetNonIdentifyingAttributes()),
	}
	return json.Marshal(out)
}

func attrsToMap(kvs []*protobufs.KeyValue) map[string]any {
	out := map[string]any{}
	for _, kv := range kvs {
		out[kv.GetKey()] = anyValueToGo(kv.GetValue())
	}
	return out
}

func anyValueToGo(v *protobufs.AnyValue) any {
	switch t := v.GetValue().(type) {
	case *protobufs.AnyValue_StringValue:
		return t.StringValue
	case *protobufs.AnyValue_BoolValue:
		return t.BoolValue
	case *protobufs.AnyValue_IntValue:
		return t.IntValue
	case *protobufs.AnyValue_DoubleValue:
		return t.DoubleValue
	case *protobufs.AnyValue_BytesValue:
		return hex.EncodeToString(t.BytesValue)
	case *protobufs.AnyValue_ArrayValue:
		items := make([]any, 0, len(t.ArrayValue.GetValues()))
		for _, item := range t.ArrayValue.GetValues() {
			items = append(items, anyValueToGo(item))
		}
		return items
	case *protobufs.AnyValue_KvlistValue:
		return attrsToMap(t.KvlistValue.GetValues())
	default:
		return nil
	}
}

// healthJSON encodes the ComponentHealth tree as JSONB.
func healthJSON(hlt *protobufs.ComponentHealth) ([]byte, error) {
	return json.Marshal(componentHealthToMap(hlt))
}

func componentHealthToMap(hlt *protobufs.ComponentHealth) map[string]any {
	out := map[string]any{
		"healthy": hlt.GetHealthy(),
	}
	if s := hlt.GetStatus(); s != "" {
		out["status"] = s
	}
	if e := hlt.GetLastError(); e != "" {
		out["lastError"] = e
	}
	if ts := hlt.GetStartTimeUnixNano(); ts != 0 {
		out["startTime"] = time.Unix(0, int64(ts)).UTC().Format(time.RFC3339Nano)
	}
	if ts := hlt.GetStatusTimeUnixNano(); ts != 0 {
		out["statusTime"] = time.Unix(0, int64(ts)).UTC().Format(time.RFC3339Nano)
	}
	if m := hlt.GetComponentHealthMap(); len(m) > 0 {
		children := map[string]any{}
		for k, v := range m {
			children[k] = componentHealthToMap(v)
		}
		out["components"] = children
	}
	return out
}
