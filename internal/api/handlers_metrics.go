package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/stats"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// QueryMetricsRange proxies a range PromQL query to VictoriaMetrics. Admin-only
// (enforced by Guard via the /api/v1/metrics prefix) — it can read every
// metric. Powers the infrastructure metrics view.
func (s *Server) QueryMetricsRange(ctx context.Context, request apigen.QueryMetricsRangeRequestObject) (apigen.QueryMetricsRangeResponseObject, error) {
	p := request.Params
	if !p.End.After(p.Start) {
		return apigen.QueryMetricsRange400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "'end' must be after 'start'"}}, nil
	}
	step, err := stats.ParseStep(&p.Step)
	if err != nil {
		return apigen.QueryMetricsRange400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: err.Error()}}, nil
	}

	series, err := s.stats.QueryRange(ctx, p.Query, p.Start, p.End, step)
	switch {
	case errors.Is(err, stats.ErrBadQuery):
		return apigen.QueryMetricsRange400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: err.Error()}}, nil
	case errors.Is(err, stats.ErrUpstreamUnavailable):
		return apigen.QueryMetricsRange503JSONResponse{UpstreamUnavailableJSONResponse: apigen.UpstreamUnavailableJSONResponse{Code: codeUpstream, Message: "metrics store unavailable"}}, nil
	case err != nil:
		return nil, err
	}

	return apigen.QueryMetricsRange200JSONResponse{Series: toMetricSeries(series)}, nil
}

// QueryCustomerMetricsRange runs a range PromQL query scoped to one customer:
// VictoriaMetrics applies extra_filters[] tenant_id="<clientId>" to every
// selector, so a portal user can run arbitrary PromQL but only sees their own
// tenant. Access-checked (not admin-only).
func (s *Server) QueryCustomerMetricsRange(ctx context.Context, request apigen.QueryCustomerMetricsRangeRequestObject) (apigen.QueryCustomerMetricsRangeResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	cust, err := s.store.GetCustomer(ctx, request.CustomerId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.QueryCustomerMetricsRange404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	p := request.Params
	if !p.End.After(p.Start) {
		return apigen.QueryCustomerMetricsRange400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: "'end' must be after 'start'"}}, nil
	}
	step, err := stats.ParseStep(&p.Step)
	if err != nil {
		return apigen.QueryCustomerMetricsRange400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: err.Error()}}, nil
	}

	filter := fmt.Sprintf(`{tenant_id=%q}`, cust.ClientID)
	series, err := s.stats.QueryRangeScoped(ctx, p.Query, p.Start, p.End, step, []string{filter})
	switch {
	case errors.Is(err, stats.ErrBadQuery):
		return apigen.QueryCustomerMetricsRange400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: err.Error()}}, nil
	case errors.Is(err, stats.ErrUpstreamUnavailable):
		return apigen.QueryCustomerMetricsRange503JSONResponse{UpstreamUnavailableJSONResponse: apigen.UpstreamUnavailableJSONResponse{Code: codeUpstream, Message: "metrics store unavailable"}}, nil
	case err != nil:
		return nil, err
	}
	return apigen.QueryCustomerMetricsRange200JSONResponse{Series: toMetricSeries(series)}, nil
}

func toMetricSeries(series []stats.MetricSeries) []apigen.MetricSeries {
	out := make([]apigen.MetricSeries, 0, len(series))
	for _, ser := range series {
		pts := make([]apigen.MetricPoint, 0, len(ser.Points))
		for _, pt := range ser.Points {
			pts = append(pts, apigen.MetricPoint{Ts: pt.TS, Value: float32(pt.Value)})
		}
		out = append(out, apigen.MetricSeries{Labels: ser.Labels, Points: pts})
	}
	return out
}
