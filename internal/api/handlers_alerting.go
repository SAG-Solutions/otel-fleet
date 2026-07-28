package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/jansagurna/otelfleet/internal/api/apigen"
	"github.com/jansagurna/otelfleet/internal/audit"
	"github.com/jansagurna/otelfleet/internal/store"
)

func toAlertRule(r store.AlertRule) apigen.AlertRule {
	channels := r.ChannelIDs
	if channels == nil {
		channels = []uuid.UUID{}
	}
	return apigen.AlertRule{
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
}

func validAlertMetric(m string) bool {
	return m == store.AlertMetricIngestItems || m == store.AlertMetricErrorLogs
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
	nr := store.NewAlertRule{
		ID:            uuid.New(),
		Name:          b.Name,
		Metric:        metric,
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
	upd := store.AlertRuleUpdate{Name: b.Name, WindowSeconds: b.WindowSeconds, Enabled: b.Enabled}
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
