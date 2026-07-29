// Package api implements the REST layer: the oapi-codegen strict-server
// handlers, session/CSRF/RBAC middleware, the HTTP router and the ops
// (metrics/health) endpoints.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/jansagurna/otelfleet/internal/api/apigen"
)

// Error codes used across the API.
const (
	codeUnauthorized = "unauthorized"
	codeForbidden    = "forbidden"
	codeNotFound     = "not_found"
	codeBadRequest   = "bad_request"
	codeConflict     = "conflict"
	codeInternal     = "internal"
	codeUpstream     = "upstream_unavailable"
	codeRateLimited  = "rate_limited"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apigen.Error{Code: code, Message: message})
}

// errorResponse is a status+Error pair that can stand in for any strict-server
// response object; per-operation wrappers below implement the generated
// Visit* interfaces so handlers can return statuses (400/503) that the
// OpenAPI contract does not enumerate explicitly.
type errorResponse struct {
	status int
	body   apigen.Error
}

func errResp(status int, code, message string) errorResponse {
	return errorResponse{status: status, body: apigen.Error{Code: code, Message: message}}
}

func (e errorResponse) write(w http.ResponseWriter) error {
	writeJSON(w, e.status, e.body)
	return nil
}

type statsOverviewErrResponse struct{ errorResponse }

func (e statsOverviewErrResponse) VisitGetStatsOverviewResponse(w http.ResponseWriter) error {
	return e.write(w)
}

type throughputErrResponse struct{ errorResponse }

func (e throughputErrResponse) VisitGetCustomerThroughputResponse(w http.ResponseWriter) error {
	return e.write(w)
}

type costStatsErrResponse struct{ errorResponse }

func (e costStatsErrResponse) VisitGetCostStatsResponse(w http.ResponseWriter) error {
	return e.write(w)
}

type createAPIKeyErrResponse struct{ errorResponse }

func (e createAPIKeyErrResponse) VisitCreateApiKeyResponse(w http.ResponseWriter) error {
	return e.write(w)
}

type devLoginErrResponse struct{ errorResponse }

func (e devLoginErrResponse) VisitDevLoginResponse(w http.ResponseWriter) error {
	return e.write(w)
}

type createPipelineErrResponse struct{ errorResponse }

func (e createPipelineErrResponse) VisitCreatePipelineResponse(w http.ResponseWriter) error {
	return e.write(w)
}

type stageStatsErrResponse struct{ errorResponse }

func (e stageStatsErrResponse) VisitGetPipelineStageStatsResponse(w http.ResponseWriter) error {
	return e.write(w)
}

type queryLogsErrResponse struct{ errorResponse }

func (e queryLogsErrResponse) VisitQueryLogsResponse(w http.ResponseWriter) error { return e.write(w) }

func queryLogsErr(status int, code, msg string) queryLogsErrResponse {
	return queryLogsErrResponse{errResp(status, code, msg)}
}

type queryTracesErrResponse struct{ errorResponse }

func (e queryTracesErrResponse) VisitQueryTracesResponse(w http.ResponseWriter) error {
	return e.write(w)
}

func queryTracesErr(status int, code, msg string) queryTracesErrResponse {
	return queryTracesErrResponse{errResp(status, code, msg)}
}

type getTraceErrResponse struct{ errorResponse }

func (e getTraceErrResponse) VisitGetTraceResponse(w http.ResponseWriter) error { return e.write(w) }

func getTraceErr(status int, code, msg string) getTraceErrResponse {
	return getTraceErrResponse{errResp(status, code, msg)}
}
