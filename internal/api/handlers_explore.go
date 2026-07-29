package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/query"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

func (s *Server) QueryLogs(ctx context.Context, request apigen.QueryLogsRequestObject) (apigen.QueryLogsResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	p := request.Params
	if !p.To.After(p.From) {
		return queryLogsErr(http.StatusBadRequest, codeBadRequest, "'to' must be after 'from'"), nil
	}
	q := query.LogQuery{From: p.From, To: p.To, Before: p.Before}
	if p.Q != nil {
		q.Text = *p.Q
	}
	if p.Service != nil {
		q.Service = *p.Service
	}
	if p.MinSeverity != nil {
		q.MinSeverity = int32(*p.MinSeverity)
	}
	if p.Limit != nil {
		q.Limit = *p.Limit
	}

	logs, next, err := s.query.QueryLogs(ctx, request.CustomerId, q)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apigen.QueryLogs404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	case errors.Is(err, query.ErrUpstreamUnavailable):
		return queryLogsErr(http.StatusServiceUnavailable, codeUpstream, "telemetry store unavailable"), nil
	case err != nil:
		return nil, err
	}

	out := apigen.QueryLogs200JSONResponse{Logs: make([]apigen.LogRecord, 0, len(logs)), NextBefore: next}
	for _, l := range logs {
		rec := apigen.LogRecord{
			Timestamp:      l.Timestamp,
			SeverityText:   l.SeverityText,
			SeverityNumber: int(l.SeverityNumber),
			ServiceName:    l.ServiceName,
			Body:           l.Body,
			Attributes:     ptrMap(l.Attributes),
		}
		if l.TraceID != "" {
			rec.TraceId = &l.TraceID
		}
		if l.SpanID != "" {
			rec.SpanId = &l.SpanID
		}
		out.Logs = append(out.Logs, rec)
	}
	return out, nil
}

func (s *Server) QueryTraces(ctx context.Context, request apigen.QueryTracesRequestObject) (apigen.QueryTracesResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	p := request.Params
	if !p.To.After(p.From) {
		return queryTracesErr(http.StatusBadRequest, codeBadRequest, "'to' must be after 'from'"), nil
	}
	q := query.TraceQuery{From: p.From, To: p.To, Before: p.Before}
	if p.Service != nil {
		q.Service = *p.Service
	}
	if p.Name != nil {
		q.Name = *p.Name
	}
	if p.MinDurationMs != nil {
		q.MinDurationMs = float64(*p.MinDurationMs)
	}
	if p.ErrorsOnly != nil {
		q.ErrorsOnly = *p.ErrorsOnly
	}
	if p.Limit != nil {
		q.Limit = *p.Limit
	}

	traces, next, err := s.query.QueryTraces(ctx, request.CustomerId, q)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apigen.QueryTraces404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	case errors.Is(err, query.ErrUpstreamUnavailable):
		return queryTracesErr(http.StatusServiceUnavailable, codeUpstream, "telemetry store unavailable"), nil
	case err != nil:
		return nil, err
	}

	out := apigen.QueryTraces200JSONResponse{Traces: make([]apigen.TraceSummary, 0, len(traces)), NextBefore: next}
	for _, t := range traces {
		out.Traces = append(out.Traces, apigen.TraceSummary{
			TraceId:     t.TraceID,
			RootName:    t.RootName,
			RootService: t.RootService,
			StartTime:   t.StartTime,
			DurationMs:  float32(t.DurationMs),
			SpanCount:   int(t.SpanCount),
			ErrorCount:  int(t.ErrorCount),
		})
	}
	return out, nil
}

func (s *Server) GetTrace(ctx context.Context, request apigen.GetTraceRequestObject) (apigen.GetTraceResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	spans, err := s.query.GetTrace(ctx, request.CustomerId, request.TraceId)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apigen.GetTrace404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	case errors.Is(err, query.ErrUpstreamUnavailable):
		return getTraceErr(http.StatusServiceUnavailable, codeUpstream, "telemetry store unavailable"), nil
	case err != nil:
		return nil, err
	}
	out := apigen.GetTrace200JSONResponse{Spans: make([]apigen.Span, 0, len(spans))}
	for _, sp := range spans {
		span := apigen.Span{
			SpanId:     sp.SpanID,
			Name:       sp.Name,
			Service:    sp.Service,
			Kind:       sp.Kind,
			StartTime:  sp.StartTime,
			DurationMs: float32(sp.DurationMs),
			StatusCode: sp.StatusCode,
			Attributes: ptrMap(sp.Attributes),
		}
		if sp.ParentSpanID != "" {
			span.ParentSpanId = &sp.ParentSpanID
		}
		if sp.StatusMessage != "" {
			span.StatusMessage = &sp.StatusMessage
		}
		out.Spans = append(out.Spans, span)
	}
	return out, nil
}

// ptrMap returns a pointer to the map for the optional attributes field, or nil
// when empty so it is omitted from the JSON.
func ptrMap(m map[string]string) *map[string]string {
	if len(m) == 0 {
		return nil
	}
	return &m
}
