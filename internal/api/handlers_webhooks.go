package api

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/webhooks"
)

func toWebhook(w store.Webhook) apigen.Webhook {
	events := make([]apigen.WebhookEvent, 0, len(w.Events))
	for _, e := range w.Events {
		events = append(events, apigen.WebhookEvent(e))
	}
	return apigen.Webhook{
		Id:        w.ID,
		Type:      apigen.WebhookType(w.Type),
		Name:      w.Name,
		Url:       w.URL,
		Events:    events,
		Enabled:   w.Enabled,
		HasSecret: len(w.SecretEnc) > 0,
		CreatedAt: w.CreatedAt,
	}
}

// webhookType normalizes the optional channel type (default generic) and
// rejects unknown values.
func webhookType(t *apigen.WebhookType) (string, error) {
	if t == nil {
		return store.WebhookTypeGeneric, nil
	}
	switch string(*t) {
	case store.WebhookTypeGeneric, store.WebhookTypeSlack, store.WebhookTypePagerDuty, store.WebhookTypeOpsgenie:
		return string(*t), nil
	default:
		return "", badRequestError{errors.New("unknown channel type " + string(*t))}
	}
}

// validateChannelURLSecret enforces the per-type URL and secret rules; it
// returns a user-facing message when invalid, or "" when acceptable. URL is
// required and validated for webhook/slack; optional for pagerduty/opsgenie
// (empty = default vendor endpoint) but validated when provided. PagerDuty and
// Opsgenie require a secret (routing key / API key).
func validateChannelURLSecret(chType, url, secret string) string {
	if store.SecretedWebhookType(chType) {
		if url != "" {
			if err := webhooks.ValidateURL(url); err != nil {
				return err.Error()
			}
		}
		if secret == "" {
			if chType == store.WebhookTypePagerDuty {
				return "PagerDuty channels require a secret (the integration routing key)"
			}
			return "Opsgenie channels require a secret (the GenieKey API key)"
		}
		return ""
	}
	if err := webhooks.ValidateURL(url); err != nil {
		return err.Error()
	}
	return ""
}

// validateWebhookEvents rejects unknown event types and empty subscriptions.
func validateWebhookEvents(events []apigen.WebhookEvent) ([]string, error) {
	if len(events) == 0 {
		return nil, badRequestError{errors.New("at least one event is required")}
	}
	out := make([]string, 0, len(events))
	for _, e := range events {
		switch string(e) {
		case store.WebhookEventAgentOffline, store.WebhookEventAgentConfigFailed, store.WebhookEventAgentUnhealthy:
			out = append(out, string(e))
		default:
			return nil, badRequestError{errors.New("unknown webhook event " + string(e))}
		}
	}
	return out, nil
}

func (s *Server) ListWebhooks(ctx context.Context, _ apigen.ListWebhooksRequestObject) (apigen.ListWebhooksResponseObject, error) {
	hooks, err := s.store.ListWebhooks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apigen.Webhook, 0, len(hooks))
	for _, w := range hooks {
		out = append(out, toWebhook(w))
	}
	return apigen.ListWebhooks200JSONResponse{Webhooks: out}, nil
}

func (s *Server) CreateWebhook(ctx context.Context, request apigen.CreateWebhookRequestObject) (apigen.CreateWebhookResponseObject, error) {
	body := request.Body
	if body.Name == "" {
		return apigen.CreateWebhook400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "name is required"}}, nil
	}
	events, err := validateWebhookEvents(body.Events)
	if err != nil {
		return nil, err
	}
	chType, err := webhookType(body.Type)
	if err != nil {
		return nil, err
	}
	url := ""
	if body.Url != nil {
		url = *body.Url
	}
	secret := ""
	if body.Secret != nil {
		secret = *body.Secret
	}
	if msg := validateChannelURLSecret(chType, url, secret); msg != "" {
		return apigen.CreateWebhook400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: msg}}, nil
	}

	nw := store.NewWebhook{
		ID:        uuid.New(),
		Type:      chType,
		Name:      body.Name,
		URL:       url,
		Events:    events,
		Enabled:   body.Enabled == nil || *body.Enabled,
		CreatedBy: actorID(ctx),
	}
	// Slack channels carry no secret (Slack does not verify a signature).
	if chType != store.WebhookTypeSlack && secret != "" {
		enc, err := s.encryptClientSecret(secret)
		if err != nil {
			return nil, err
		}
		nw.SecretEnc = enc
	}

	created, err := s.store.CreateWebhook(ctx, nw, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "webhook.create",
		EntityType:  "webhook",
		EntityID:    nw.ID.String(),
		Payload:     map[string]any{"name": nw.Name, "url": nw.URL, "events": events},
	}})
	if err != nil {
		return nil, err
	}
	return apigen.CreateWebhook201JSONResponse(toWebhook(created)), nil
}

func (s *Server) UpdateWebhook(ctx context.Context, request apigen.UpdateWebhookRequestObject) (apigen.UpdateWebhookResponseObject, error) {
	body := request.Body
	upd := store.WebhookUpdate{Name: body.Name, URL: body.Url, Enabled: body.Enabled}
	if body.Type != nil {
		ct, err := webhookType(body.Type)
		if err != nil {
			return nil, err
		}
		upd.Type = &ct
	}
	// An empty URL clears it (pagerduty/opsgenie then use the default vendor
	// endpoint); any non-empty URL is validated.
	if body.Url != nil && *body.Url != "" {
		if err := webhooks.ValidateURL(*body.Url); err != nil {
			return apigen.UpdateWebhook400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: err.Error()}}, nil
		}
	}
	if body.Events != nil {
		events, err := validateWebhookEvents(*body.Events)
		if err != nil {
			return nil, err
		}
		upd.Events = events
	}
	// Tri-state secret: absent = keep, "" = remove signing, value = rotate.
	if body.Secret != nil {
		upd.SecretSet = true
		if *body.Secret != "" {
			enc, err := s.encryptClientSecret(*body.Secret)
			if err != nil {
				return nil, err
			}
			upd.SecretEnc = enc
		}
	}

	updated, err := s.store.UpdateWebhook(ctx, request.WebhookId, upd, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "webhook.update",
		EntityType:  "webhook",
		EntityID:    request.WebhookId.String(),
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.UpdateWebhook404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "webhook not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.UpdateWebhook200JSONResponse(toWebhook(updated)), nil
}

func (s *Server) DeleteWebhook(ctx context.Context, request apigen.DeleteWebhookRequestObject) (apigen.DeleteWebhookResponseObject, error) {
	err := s.store.DeleteWebhook(ctx, request.WebhookId, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "webhook.delete",
		EntityType:  "webhook",
		EntityID:    request.WebhookId.String(),
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.DeleteWebhook404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "webhook not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.DeleteWebhook204Response{}, nil
}

func (s *Server) TestWebhook(ctx context.Context, request apigen.TestWebhookRequestObject) (apigen.TestWebhookResponseObject, error) {
	wh, err := s.store.GetWebhook(ctx, request.WebhookId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.TestWebhook404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "webhook not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	if s.webhooks == nil {
		return apigen.TestWebhook200JSONResponse{Ok: false, Message: "webhook dispatcher not available"}, nil
	}
	ok, msg := s.webhooks.SendTest(ctx, wh)
	return apigen.TestWebhook200JSONResponse{Ok: ok, Message: msg}, nil
}
