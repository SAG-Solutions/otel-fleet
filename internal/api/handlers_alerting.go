package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

func toAlertRule(r store.AlertRule) apigen.AlertRule {
	channels := r.ChannelIDs
	if channels == nil {
		channels = []uuid.UUID{}
	}
	out := apigen.AlertRule{
		Id:            r.ID,
		Name:          r.Name,
		Metric:        apigen.AlertMetric(r.Metric),
		Comparison:    apigen.AlertComparison(r.Comparison),
		Threshold:     float32(r.Threshold),
		WindowSeconds: r.WindowSeconds,
		CustomerId:    r.CustomerID,
		ChannelIds:    channels,
		Enabled:       r.Enabled,
		CreatedAt:     r.CreatedAt,
	}
	if r.Query != "" {
		q := r.Query
		out.Query = &q
	}
	return out
}

func validAlertMetric(m string) bool {
	return m == store.AlertMetricIngestItems || m == store.AlertMetricErrorLogs || m == store.AlertMetricPromQL
}

func validAlertComparison(c string) bool {
	return c == store.AlertComparisonBelow || c == store.AlertComparisonAbove
}

func (s *Server) ListAlertRules(ctx context.Context, _ apigen.ListAlertRulesRequestObject) (apigen.ListAlertRulesResponseObject, error) {
	rules, err := s.store.ListAlertRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.AlertRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, toAlertRule(r))
	}
	return apigen.ListAlertRules200JSONResponse{Rules: out}, nil
}

func (s *Server) CreateAlertRule(ctx context.Context, request apigen.CreateAlertRuleRequestObject) (apigen.CreateAlertRuleResponseObject, error) {
	b := request.Body
	metric, comparison := string(b.Metric), string(b.Comparison)
	if !validAlertMetric(metric) {
		return apigen.CreateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "unknown metric"}}, nil
	}
	if !validAlertComparison(comparison) {
		return apigen.CreateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "comparison must be below or above"}}, nil
	}
	if b.WindowSeconds < 60 {
		return apigen.CreateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "windowSeconds must be >= 60"}}, nil
	}
	query := ""
	if b.Query != nil {
		query = *b.Query
	}
	if metric == store.AlertMetricPromQL {
		if query == "" {
			return apigen.CreateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "promql metric requires a query"}}, nil
		}
		if b.CustomerId != nil {
			return apigen.CreateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "promql rules are cluster-wide; customerId must be null"}}, nil
		}
	} else {
		query = "" // non-promql rules carry no query
	}
	nr := store.NewAlertRule{
		ID:            uuid.New(),
		Name:          b.Name,
		Metric:        metric,
		Query:         query,
		Comparison:    comparison,
		Threshold:     float64(b.Threshold),
		WindowSeconds: b.WindowSeconds,
		CustomerID:    b.CustomerId,
		Enabled:       b.Enabled == nil || *b.Enabled,
	}
	if b.ChannelIds != nil {
		nr.ChannelIDs = *b.ChannelIds
	}
	created, err := s.store.CreateAlertRule(ctx, nr, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "alertrule.create",
		EntityType:  "alert_rule",
		EntityID:    nr.ID.String(),
		Payload:     map[string]any{"name": nr.Name, "metric": metric},
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.CreateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "unknown customerId"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.CreateAlertRule201JSONResponse(toAlertRule(created)), nil
}

func (s *Server) UpdateAlertRule(ctx context.Context, request apigen.UpdateAlertRuleRequestObject) (apigen.UpdateAlertRuleResponseObject, error) {
	b := request.Body
	upd := store.AlertRuleUpdate{Name: b.Name, Query: b.Query, WindowSeconds: b.WindowSeconds, Enabled: b.Enabled}
	if b.Metric != nil {
		m := string(*b.Metric)
		if !validAlertMetric(m) {
			return apigen.UpdateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "unknown metric"}}, nil
		}
		upd.Metric = &m
	}
	if b.Comparison != nil {
		c := string(*b.Comparison)
		if !validAlertComparison(c) {
			return apigen.UpdateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "comparison must be below or above"}}, nil
		}
		upd.Comparison = &c
	}
	if b.Threshold != nil {
		th := float64(*b.Threshold)
		upd.Threshold = &th
	}
	if b.WindowSeconds != nil && *b.WindowSeconds < 60 {
		return apigen.UpdateAlertRule400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "windowSeconds must be >= 60"}}, nil
	}
	if b.ChannelIds != nil {
		upd.ChannelIDs = *b.ChannelIds
	}
	updated, err := s.store.UpdateAlertRule(ctx, request.RuleId, upd, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "alertrule.update",
		EntityType:  "alert_rule",
		EntityID:    request.RuleId.String(),
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.UpdateAlertRule404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "alert rule not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.UpdateAlertRule200JSONResponse(toAlertRule(updated)), nil
}

func toMaintenanceWindow(w store.MaintenanceWindow) apigen.MaintenanceWindow {
	return apigen.MaintenanceWindow{
		Id: w.ID, Name: w.Name, StartsAt: w.StartsAt, EndsAt: w.EndsAt, CreatedAt: w.CreatedAt,
	}
}

func (s *Server) ListMaintenanceWindows(ctx context.Context, _ apigen.ListMaintenanceWindowsRequestObject) (apigen.ListMaintenanceWindowsResponseObject, error) {
	ws, err := s.store.ListMaintenanceWindows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.MaintenanceWindow, 0, len(ws))
	for _, w := range ws {
		out = append(out, toMaintenanceWindow(w))
	}
	return apigen.ListMaintenanceWindows200JSONResponse{Windows: out}, nil
}

func (s *Server) CreateMaintenanceWindow(ctx context.Context, request apigen.CreateMaintenanceWindowRequestObject) (apigen.CreateMaintenanceWindowResponseObject, error) {
	b := request.Body
	if !b.EndsAt.After(b.StartsAt) {
		return apigen.CreateMaintenanceWindow400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "endsAt must be after startsAt"}}, nil
	}
	nw := store.NewMaintenanceWindow{ID: uuid.New(), Name: b.Name, StartsAt: b.StartsAt, EndsAt: b.EndsAt}
	created, err := s.store.CreateMaintenanceWindow(ctx, nw, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "maintenance_window.create",
		EntityType:  "maintenance_window",
		EntityID:    nw.ID.String(),
		Payload:     map[string]any{"name": nw.Name},
	}})
	if err != nil {
		return nil, err
	}
	return apigen.CreateMaintenanceWindow201JSONResponse(toMaintenanceWindow(created)), nil
}

func (s *Server) DeleteMaintenanceWindow(ctx context.Context, request apigen.DeleteMaintenanceWindowRequestObject) (apigen.DeleteMaintenanceWindowResponseObject, error) {
	err := s.store.DeleteMaintenanceWindow(ctx, request.WindowId, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "maintenance_window.delete",
		EntityType:  "maintenance_window",
		EntityID:    request.WindowId.String(),
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.DeleteMaintenanceWindow404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "maintenance window not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.DeleteMaintenanceWindow204Response{}, nil
}

func (s *Server) DeleteAlertRule(ctx context.Context, request apigen.DeleteAlertRuleRequestObject) (apigen.DeleteAlertRuleResponseObject, error) {
	err := s.store.DeleteAlertRule(ctx, request.RuleId, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "alertrule.delete",
		EntityType:  "alert_rule",
		EntityID:    request.RuleId.String(),
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.DeleteAlertRule404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "alert rule not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.DeleteAlertRule204Response{}, nil
}
