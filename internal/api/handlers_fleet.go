package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/tenants"
)

// --- mapping helpers ---

func hexPtr(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	s := hex.EncodeToString(b)
	return &s
}

// configInSync is true/false when the agent has acknowledged a config, nil
// otherwise. It compares the assigned (desired) hash against the hash the
// agent confirmed over OpAMP (RemoteConfigStatus.last_remote_config_hash) —
// NOT the effective-config hash, which is a re-serialization that never
// matches the assigned hash.
func configInSync(assigned, acked []byte) *bool {
	if len(assigned) == 0 || len(acked) == 0 {
		return nil
	}
	v := bytes.Equal(assigned, acked)
	return &v
}

// agentLabels decodes the JSONB label map, always returning a non-nil map.
func agentLabels(raw []byte) map[string]string {
	labels := map[string]string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &labels)
	}
	return labels
}

func toAgent(a store.Agent) apigen.Agent {
	labels := agentLabels(a.Labels)
	return apigen.Agent{
		Id:                 a.ID,
		InstanceUid:        hex.EncodeToString(a.InstanceUID),
		Class:              apigen.AgentClass(a.Class),
		CustomerId:         a.CustomerID,
		CustomerName:       a.CustomerName,
		Name:               a.Name,
		DisplayName:        a.DisplayName,
		Labels:             &labels,
		AgentVersion:       a.AgentVersion,
		Connected:          a.Connected,
		Healthy:            a.Healthy,
		LastSeenAt:         a.LastSeenAt,
		RemoteConfigStatus: apigen.RemoteConfigStatus(a.RemoteConfigStatus),
		RemoteConfigError:  a.RemoteConfigError,
		AssignedConfigHash: hexPtr(a.AssignedConfigHash),
		ReportedConfigHash: hexPtr(a.ReportedConfigHash),
		ConfigInSync:       configInSync(a.AssignedConfigHash, a.AckedConfigHash),
		CreatedAt:          a.CreatedAt,
	}
}

func toAgentDetail(a store.Agent) (apigen.AgentDetail, error) {
	base := toAgent(a)
	detail := apigen.AgentDetail{
		Id:                 base.Id,
		InstanceUid:        base.InstanceUid,
		Class:              base.Class,
		CustomerId:         base.CustomerId,
		CustomerName:       base.CustomerName,
		Name:               base.Name,
		DisplayName:        base.DisplayName,
		Labels:             base.Labels,
		AgentVersion:       base.AgentVersion,
		Connected:          base.Connected,
		Healthy:            base.Healthy,
		LastSeenAt:         base.LastSeenAt,
		RemoteConfigStatus: base.RemoteConfigStatus,
		RemoteConfigError:  base.RemoteConfigError,
		AssignedConfigHash: base.AssignedConfigHash,
		ReportedConfigHash: base.ReportedConfigHash,
		ConfigInSync:       base.ConfigInSync,
		CreatedAt:          base.CreatedAt,
	}
	if len(a.Description) > 0 {
		var m map[string]any
		if err := json.Unmarshal(a.Description, &m); err != nil {
			return apigen.AgentDetail{}, err
		}
		detail.Description = &m
	}
	if len(a.Health) > 0 {
		var m map[string]any
		if err := json.Unmarshal(a.Health, &m); err != nil {
			return apigen.AgentDetail{}, err
		}
		detail.Health = &m
	}
	return detail, nil
}

func toBootstrapToken(t store.BootstrapToken) apigen.BootstrapToken {
	return apigen.BootstrapToken{
		Id:          t.ID,
		CustomerId:  t.CustomerID,
		Name:        t.Name,
		TokenPrefix: t.TokenPrefix,
		MaxUses:     t.MaxUses,
		UsedCount:   t.UsedCount,
		CreatedAt:   t.CreatedAt,
		ExpiresAt:   t.ExpiresAt,
		RevokedAt:   t.RevokedAt,
	}
}

// --- agents ---

func (s *Server) ListAgents(ctx context.Context, request apigen.ListAgentsRequestObject) (apigen.ListAgentsResponseObject, error) {
	f := store.AgentFilter{
		CustomerID: request.Params.CustomerId,
		Connected:  request.Params.Connected,
	}
	if request.Params.Class != nil {
		v := string(*request.Params.Class)
		f.Class = &v
	}
	agents, err := s.store.ListAgents(ctx, f)
	if err != nil {
		return nil, err
	}
	allowed, scoped := customerScope(ctx)
	out := make([]apigen.Agent, 0, len(agents))
	for _, a := range agents {
		// Scoped users see only their customers' agents; gateway agents (no
		// customer) are visible to unscoped users only.
		if scoped && (a.CustomerID == nil || !allowed[*a.CustomerID]) {
			continue
		}
		out = append(out, toAgent(a))
	}
	return apigen.ListAgents200JSONResponse{Agents: out}, nil
}

func (s *Server) GetAgent(ctx context.Context, request apigen.GetAgentRequestObject) (apigen.GetAgentResponseObject, error) {
	a, err := s.store.GetAgent(ctx, request.AgentId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.GetAgent404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireCustomerAccess(ctx, a.CustomerID); err != nil {
		return nil, err
	}
	detail, err := toAgentDetail(a)
	if err != nil {
		return nil, err
	}
	return apigen.GetAgent200JSONResponse(detail), nil
}

func (s *Server) DeleteAgent(ctx context.Context, request apigen.DeleteAgentRequestObject) (apigen.DeleteAgentResponseObject, error) {
	a, err := s.store.GetAgent(ctx, request.AgentId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.DeleteAgent404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireCustomerAccess(ctx, a.CustomerID); err != nil {
		return nil, err
	}
	// Refuse deleting a live agent. The `connected` column is written by the
	// OpAMP tier on connect/disconnect transitions, so this check works across
	// process boundaries (the API tier need not reach the OpAMP registry).
	if a.Connected {
		return apigen.DeleteAgent409JSONResponse{ConflictJSONResponse: apigen.ConflictJSONResponse{Code: codeConflict, Message: "agent is currently connected"}}, nil
	}
	err = s.store.DeleteAgent(ctx, request.AgentId, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "agent.delete",
		EntityType:  "agent",
		EntityID:    request.AgentId.String(),
		CustomerID:  a.CustomerID,
		Payload:     map[string]any{"instance_uid": hex.EncodeToString(a.InstanceUID)},
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.DeleteAgent404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.DeleteAgent204Response{}, nil
}

func (s *Server) UpdateAgent(ctx context.Context, request apigen.UpdateAgentRequestObject) (apigen.UpdateAgentResponseObject, error) {
	a, err := s.store.GetAgent(ctx, request.AgentId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.UpdateAgent404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireCustomerAccess(ctx, a.CustomerID); err != nil {
		return nil, err
	}

	// Display name: null/empty clears the operator override.
	var displayName *string
	if request.Body.DisplayName != nil {
		if trimmed := strings.TrimSpace(*request.Body.DisplayName); trimmed != "" {
			displayName = &trimmed
		}
	}
	// Labels: only rewritten when the field is present (nil = leave unchanged).
	var labels []byte
	if request.Body.Labels != nil {
		labels, err = json.Marshal(*request.Body.Labels)
		if err != nil {
			return apigen.UpdateAgent400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "invalid labels"}}, nil
		}
	}

	updated, err := s.store.UpdateAgentMeta(ctx, request.AgentId, displayName, labels, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "agent.update",
		EntityType:  "agent",
		EntityID:    request.AgentId.String(),
		CustomerID:  a.CustomerID,
		Payload:     map[string]any{"instance_uid": hex.EncodeToString(a.InstanceUID)},
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.UpdateAgent404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.UpdateAgent200JSONResponse(toAgent(updated)), nil
}

// SyncAgent forces a re-push of the agent's customer edge config. Because edge
// config is rendered per-customer, this re-syncs every one of that customer's
// edge agents; the OpAMP tier performs the push out of band (LISTEN/NOTIFY).
func (s *Server) SyncAgent(ctx context.Context, request apigen.SyncAgentRequestObject) (apigen.SyncAgentResponseObject, error) {
	a, err := s.store.GetAgent(ctx, request.AgentId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.SyncAgent404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireCustomerAccess(ctx, a.CustomerID); err != nil {
		return nil, err
	}
	if a.Class != store.AgentClassEdge || a.CustomerID == nil {
		return apigen.SyncAgent400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "re-sync applies to edge agents only; gateway configs are managed outside OpAMP"}}, nil
	}
	_, detail, err := s.pipelines.ResyncEdge(ctx, *a.CustomerID)
	if err != nil {
		return nil, err
	}
	if err := s.store.WriteAuditEntries(ctx, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "agent.sync",
		EntityType:  "agent",
		EntityID:    request.AgentId.String(),
		CustomerID:  a.CustomerID,
		Payload:     map[string]any{"instance_uid": hex.EncodeToString(a.InstanceUID)},
	}}); err != nil {
		s.log.Warn("audit write failed for agent.sync", "agent", request.AgentId, "err", err)
	}
	return apigen.SyncAgent202JSONResponse{Detail: detail}, nil
}

func (s *Server) GetAgentConfig(ctx context.Context, request apigen.GetAgentConfigRequestObject) (apigen.GetAgentConfigResponseObject, error) {
	a, err := s.store.GetAgent(ctx, request.AgentId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.GetAgentConfig404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireCustomerAccess(ctx, a.CustomerID); err != nil {
		return nil, err
	}

	// Assigned = the current desired config, re-rendered from database state.
	// Gateway replicas are managed outside OpAMP: "".
	assigned := ""
	if a.Class == store.AgentClassEdge && a.CustomerID != nil {
		assigned, err = s.pipelines.RenderEdgeCurrent(ctx, *a.CustomerID)
		if err != nil {
			return nil, err
		}
	}
	reported := ""
	if a.ReportedConfigYAML != nil {
		reported = *a.ReportedConfigYAML
	}
	return apigen.GetAgentConfig200JSONResponse{AssignedYaml: assigned, ReportedYaml: reported}, nil
}

func (s *Server) ListAgentEvents(ctx context.Context, request apigen.ListAgentEventsRequestObject) (apigen.ListAgentEventsResponseObject, error) {
	a, err := s.store.GetAgent(ctx, request.AgentId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.ListAgentEvents404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := requireCustomerAccess(ctx, a.CustomerID); err != nil {
		return nil, err
	}
	limit := 50
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	events, err := s.store.ListAgentEvents(ctx, request.AgentId, limit)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.ListAgentEvents404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "agent not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]apigen.AgentEvent, 0, len(events))
	for _, e := range events {
		ev := apigen.AgentEvent{
			Id:        e.ID,
			EventType: apigen.AgentEventEventType(e.EventType),
			CreatedAt: e.CreatedAt,
		}
		if len(e.Detail) > 0 {
			var m map[string]any
			if err := json.Unmarshal(e.Detail, &m); err != nil {
				return nil, err
			}
			ev.Detail = &m
		}
		out = append(out, ev)
	}
	return apigen.ListAgentEvents200JSONResponse{Events: out}, nil
}

// --- bootstrap tokens ---

func (s *Server) ListBootstrapTokens(ctx context.Context, request apigen.ListBootstrapTokensRequestObject) (apigen.ListBootstrapTokensResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	toks, err := s.store.ListBootstrapTokens(ctx, request.CustomerId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.ListBootstrapTokens404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]apigen.BootstrapToken, 0, len(toks))
	for _, t := range toks {
		out = append(out, toBootstrapToken(t))
	}
	return apigen.ListBootstrapTokens200JSONResponse{Tokens: out}, nil
}

func (s *Server) CreateBootstrapToken(ctx context.Context, request apigen.CreateBootstrapTokenRequestObject) (apigen.CreateBootstrapTokenResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	maxUses := 0
	if request.Body.MaxUses != nil {
		maxUses = *request.Body.MaxUses
	}
	created, err := s.tenants.CreateBootstrapToken(ctx, actorID(ctx), request.CustomerId, request.Body.Name, request.Body.ExpiresAt, maxUses)
	switch {
	case errors.Is(err, tenants.ErrInvalidName):
		// The contract enumerates no 400; the router's response error handler
		// maps badRequestError to a 400 Error body.
		return nil, badRequestError{err}
	case errors.Is(err, store.ErrNotFound):
		return apigen.CreateBootstrapToken404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	case err != nil:
		return nil, err
	}
	base := toBootstrapToken(created.Token)
	return apigen.CreateBootstrapToken201JSONResponse{
		Id:          base.Id,
		CustomerId:  base.CustomerId,
		Name:        base.Name,
		TokenPrefix: base.TokenPrefix,
		MaxUses:     base.MaxUses,
		UsedCount:   base.UsedCount,
		CreatedAt:   base.CreatedAt,
		ExpiresAt:   base.ExpiresAt,
		RevokedAt:   base.RevokedAt,
		Secret:      created.Secret,
	}, nil
}

func (s *Server) RevokeBootstrapToken(ctx context.Context, request apigen.RevokeBootstrapTokenRequestObject) (apigen.RevokeBootstrapTokenResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	err := s.tenants.RevokeBootstrapToken(ctx, actorID(ctx), request.CustomerId, request.TokenId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.RevokeBootstrapToken404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "bootstrap token not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.RevokeBootstrapToken204Response{}, nil
}
