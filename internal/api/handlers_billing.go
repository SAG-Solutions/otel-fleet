package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/billing"
	"github.com/sag-solutions/otel-fleet/internal/stats"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

func toBillingSettings(b store.BillingSettings) apigen.BillingSettings {
	return apigen.BillingSettings{
		PricePerGibMicro:          b.PricePerGiBMicro,
		PricePerMillionItemsMicro: b.PricePerMillionItemsMicro,
		Currency:                  b.Currency,
		UpdatedAt:                 b.UpdatedAt,
	}
}

func (s *Server) GetBillingSettings(ctx context.Context, _ apigen.GetBillingSettingsRequestObject) (apigen.GetBillingSettingsResponseObject, error) {
	b, err := s.store.GetBillingSettings(ctx)
	if err != nil {
		return nil, err
	}
	return apigen.GetBillingSettings200JSONResponse(toBillingSettings(b)), nil
}

func (s *Server) UpdateBillingSettings(ctx context.Context, request apigen.UpdateBillingSettingsRequestObject) (apigen.UpdateBillingSettingsResponseObject, error) {
	body := request.Body
	upd := store.BillingSettingsUpdate{
		PricePerGiBMicro:          body.PricePerGibMicro,
		PricePerMillionItemsMicro: body.PricePerMillionItemsMicro,
	}
	if body.PricePerGibMicro != nil && *body.PricePerGibMicro < 0 {
		return apigen.UpdateBillingSettings400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "pricePerGibMicro must be >= 0"}}, nil
	}
	if body.PricePerMillionItemsMicro != nil && *body.PricePerMillionItemsMicro < 0 {
		return apigen.UpdateBillingSettings400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "pricePerMillionItemsMicro must be >= 0"}}, nil
	}
	if body.Currency != nil {
		if len(*body.Currency) != 3 {
			return apigen.UpdateBillingSettings400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "currency must be a 3-letter code"}}, nil
		}
		upd.Currency = body.Currency
	}

	updated, err := s.store.UpdateBillingSettings(ctx, upd, actorID(ctx), []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "billing.settings.update",
		EntityType:  "billing_settings",
		EntityID:    "singleton",
	}})
	if err != nil {
		return nil, err
	}
	return apigen.UpdateBillingSettings200JSONResponse(toBillingSettings(updated)), nil
}

func (s *Server) GetBillingStatement(ctx context.Context, request apigen.GetBillingStatementRequestObject) (apigen.GetBillingStatementResponseObject, error) {
	month := request.Params.Month
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return apigen.GetBillingStatement400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "month must be YYYY-MM"}}, nil
	}
	from := start.UTC()
	to := from.AddDate(0, 1, 0)

	settings, err := s.store.GetBillingSettings(ctx)
	if err != nil {
		return nil, err
	}
	overrides, err := s.store.ListBillingOverrides(ctx)
	if err != nil {
		return nil, err
	}
	byCustomer := make(map[uuid.UUID]store.BillingOverride, len(overrides))
	for _, o := range overrides {
		byCustomer[o.CustomerID] = o
	}
	costs, err := s.stats.GetCost(ctx, from, to)
	if errors.Is(err, stats.ErrUpstreamUnavailable) {
		return nil, badRequestError{errors.New("usage backend unavailable")}
	}
	if err != nil {
		return nil, err
	}

	st := billing.Compute(month, costs, settings, byCustomer)
	resp := apigen.GetBillingStatement200JSONResponse{
		Month:                     st.Month,
		Currency:                  st.Currency,
		PricePerGibMicro:          st.PricePerGiBMicro,
		PricePerMillionItemsMicro: st.PricePerMillionItemsMicro,
		TotalMicro:                st.TotalMicro,
		Lines:                     make([]apigen.BillingLine, 0, len(st.Lines)),
	}
	for _, l := range st.Lines {
		resp.Lines = append(resp.Lines, apigen.BillingLine{
			CustomerId:     l.CustomerID,
			Name:           l.Name,
			Items:          l.Items,
			Bytes:          l.Bytes,
			BytesCostMicro: l.BytesCostMicro,
			ItemsCostMicro: l.ItemsCostMicro,
			TotalMicro:     l.TotalMicro,
			Overridden:     l.Overridden,
		})
	}
	return resp, nil
}

// ListBillingOverrides returns every per-customer price override, enriched with
// the customer's current name for display.
func (s *Server) ListBillingOverrides(ctx context.Context, _ apigen.ListBillingOverridesRequestObject) (apigen.ListBillingOverridesResponseObject, error) {
	overrides, err := s.store.ListBillingOverrides(ctx)
	if err != nil {
		return nil, err
	}
	refs, err := s.store.ListCustomerRefs(ctx)
	if err != nil {
		return nil, err
	}
	nameByID := make(map[uuid.UUID]string, len(refs))
	for _, r := range refs {
		nameByID[r.ID] = r.Name
	}
	resp := apigen.ListBillingOverrides200JSONResponse{Overrides: make([]apigen.BillingOverride, 0, len(overrides))}
	for _, o := range overrides {
		resp.Overrides = append(resp.Overrides, toBillingOverride(o, nameByID[o.CustomerID]))
	}
	return resp, nil
}

func toBillingOverride(o store.BillingOverride, name string) apigen.BillingOverride {
	return apigen.BillingOverride{
		CustomerId:                o.CustomerID,
		CustomerName:              name,
		PricePerGibMicro:          o.PricePerGiBMicro,
		PricePerMillionItemsMicro: o.PricePerMillionItemsMicro,
		UpdatedAt:                 o.UpdatedAt,
	}
}

// SetBillingOverride upserts a customer's price override. At least one price
// must be provided; a null price inherits the global rate for that dimension.
func (s *Server) SetBillingOverride(ctx context.Context, request apigen.SetBillingOverrideRequestObject) (apigen.SetBillingOverrideResponseObject, error) {
	body := request.Body
	if body.PricePerGibMicro == nil && body.PricePerMillionItemsMicro == nil {
		return apigen.SetBillingOverride400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "provide at least one price, or DELETE to revert to global pricing"}}, nil
	}
	if body.PricePerGibMicro != nil && *body.PricePerGibMicro < 0 {
		return apigen.SetBillingOverride400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "pricePerGibMicro must be >= 0"}}, nil
	}
	if body.PricePerMillionItemsMicro != nil && *body.PricePerMillionItemsMicro < 0 {
		return apigen.SetBillingOverride400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "pricePerMillionItemsMicro must be >= 0"}}, nil
	}

	cust, err := s.store.GetCustomer(ctx, request.CustomerId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.SetBillingOverride404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	}
	if err != nil {
		return nil, err
	}

	custID := request.CustomerId
	saved, err := s.store.SetBillingOverride(ctx, custID, body.PricePerGibMicro, body.PricePerMillionItemsMicro, actorID(ctx), []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "billing.override.set",
		EntityType:  "billing_price_override",
		EntityID:    custID.String(),
		CustomerID:  &custID,
		Payload: map[string]any{
			"pricePerGibMicro":          body.PricePerGibMicro,
			"pricePerMillionItemsMicro": body.PricePerMillionItemsMicro,
		},
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.SetBillingOverride404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.SetBillingOverride200JSONResponse(toBillingOverride(saved, cust.Name)), nil
}

// DeleteBillingOverride removes a customer's override, reverting to global
// pricing.
func (s *Server) DeleteBillingOverride(ctx context.Context, request apigen.DeleteBillingOverrideRequestObject) (apigen.DeleteBillingOverrideResponseObject, error) {
	custID := request.CustomerId
	err := s.store.DeleteBillingOverride(ctx, custID, []audit.Entry{{
		ActorUserID: actorID(ctx),
		Action:      "billing.override.delete",
		EntityType:  "billing_price_override",
		EntityID:    custID.String(),
		CustomerID:  &custID,
	}})
	if errors.Is(err, store.ErrNotFound) {
		return apigen.DeleteBillingOverride404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "override not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.DeleteBillingOverride204Response{}, nil
}
