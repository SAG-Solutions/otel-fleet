package api

import (
	"context"
	"errors"
	"time"

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
	costs, err := s.stats.GetCost(ctx, from, to)
	if errors.Is(err, stats.ErrUpstreamUnavailable) {
		return nil, badRequestError{errors.New("usage backend unavailable")}
	}
	if err != nil {
		return nil, err
	}

	st := billing.Compute(month, costs, settings)
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
		})
	}
	return resp, nil
}
