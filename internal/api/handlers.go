package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/sag-solutions/otel-fleet/internal/api/apigen"
	"github.com/sag-solutions/otel-fleet/internal/auth"
	"github.com/sag-solutions/otel-fleet/internal/authz"
	"github.com/sag-solutions/otel-fleet/internal/config"
	"github.com/sag-solutions/otel-fleet/internal/crypto"
	"github.com/sag-solutions/otel-fleet/internal/pipelines"
	"github.com/sag-solutions/otel-fleet/internal/query"
	"github.com/sag-solutions/otel-fleet/internal/stats"
	"github.com/sag-solutions/otel-fleet/internal/store"
	"github.com/sag-solutions/otel-fleet/internal/tenants"
)

// Server implements the OpenAPI strict-server interface.
type Server struct {
	cfg       *config.Config
	store     store.Store
	tenants   *tenants.Service
	pipelines *pipelines.Service
	stats     *stats.Service
	query     *query.Service
	sessions  *auth.Sessions
	authReg   *auth.Registry
	cipher    *crypto.Cipher // nil: master key not configured
	webhooks  WebhookTester  // nil: no dispatcher wired
	log       *slog.Logger
}

// WebhookTester is the dispatcher subset the API needs for the test endpoint.
type WebhookTester interface {
	SendTest(ctx context.Context, wh store.Webhook) (bool, string)
}

var _ apigen.StrictServerInterface = (*Server)(nil)

// NewServer wires the REST handlers.
func NewServer(cfg *config.Config, st store.Store, ten *tenants.Service, pipes *pipelines.Service, sts *stats.Service, qry *query.Service, sessions *auth.Sessions, authReg *auth.Registry, cipher *crypto.Cipher, webhooks WebhookTester, log *slog.Logger) *Server {
	return &Server{cfg: cfg, store: st, tenants: ten, pipelines: pipes, stats: sts, query: qry, sessions: sessions, authReg: authReg, cipher: cipher, webhooks: webhooks, log: log}
}

func actorID(ctx context.Context) *openapi_types.UUID {
	if p, ok := auth.PrincipalFrom(ctx); ok && p.User.ID != uuid.Nil {
		id := p.User.ID
		return &id
	}
	// No real user (e.g. an API token without a recorded creator): leave the
	// audit actor NULL rather than violating the users FK.
	return nil
}

func toCustomer(c store.Customer) apigen.Customer {
	return apigen.Customer{
		Id:                   c.ID,
		Slug:                 c.Slug,
		Name:                 c.Name,
		TenantId:             c.ClientID,
		Status:               apigen.CustomerStatus(c.Status),
		Region:               c.Region,
		RateLimitItemsPerSec: c.RateLimitItemsPerSec,
		RetentionDays:        c.RetentionDays,
		CreatedAt:            c.CreatedAt,
	}
}

func toAPIKey(k store.APIKey) apigen.ApiKey {
	return apigen.ApiKey{
		Id:         k.ID,
		CustomerId: k.CustomerID,
		Name:       k.Name,
		KeyPrefix:  k.KeyPrefix,
		CreatedAt:  k.CreatedAt,
		ExpiresAt:  k.ExpiresAt,
		RevokedAt:  k.RevokedAt,
		LastUsedAt: k.LastUsedAt,
	}
}

func toAPIKeyCreated(k store.APIKey, secret string) apigen.ApiKeyCreated {
	return apigen.ApiKeyCreated{
		Id:         k.ID,
		CustomerId: k.CustomerID,
		Name:       k.Name,
		KeyPrefix:  k.KeyPrefix,
		CreatedAt:  k.CreatedAt,
		ExpiresAt:  k.ExpiresAt,
		RevokedAt:  k.RevokedAt,
		LastUsedAt: k.LastUsedAt,
		Secret:     secret,
	}
}

// --- auth ---

// GetMe returns the session user plus the CSRF token for mutating requests.
func (s *Server) GetMe(ctx context.Context, _ apigen.GetMeRequestObject) (apigen.GetMeResponseObject, error) {
	p, ok := auth.PrincipalFrom(ctx)
	if !ok { // unreachable: Guard rejects unauthenticated requests first
		return apigen.GetMe401JSONResponse{UnauthorizedJSONResponse: apigen.UnauthorizedJSONResponse{Code: codeUnauthorized, Message: "authentication required"}}, nil
	}
	me := apigen.GetMe200JSONResponse{
		Id:           p.User.ID,
		Email:        openapi_types.Email(p.User.Email),
		DisplayName:  p.User.DisplayName,
		Role:         apigen.Role(p.User.Role),
		CsrfToken:    p.CSRFToken,
		AllCustomers: p.AllCustomers,
	}
	if !p.AllCustomers {
		ids := make([]openapi_types.UUID, 0, len(p.AllowedCustomers))
		for id := range p.AllowedCustomers {
			ids = append(ids, id)
		}
		me.ScopedCustomerIds = &ids
	}
	return me, nil
}

// ListAuthProviders is public: the login page needs it before any session.
// It offers the enabled database providers plus the environment provider.
func (s *Server) ListAuthProviders(ctx context.Context, _ apigen.ListAuthProvidersRequestObject) (apigen.ListAuthProvidersResponseObject, error) {
	list, err := s.authReg.LoginProviders(ctx)
	if err != nil {
		return nil, err
	}
	providers := []apigen.AuthProvider{}
	for _, p := range list {
		providers = append(providers, apigen.AuthProvider{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			LoginUrl:    "/auth/" + p.Name + "/start",
		})
	}
	return apigen.ListAuthProviders200JSONResponse{
		Providers:       providers,
		DevLoginEnabled: s.cfg.DevLogin,
	}, nil
}

// DevLogin signs in by bare email; enabled only with OTEL_FLEET_DEV_LOGIN=true.
func (s *Server) DevLogin(ctx context.Context, request apigen.DevLoginRequestObject) (apigen.DevLoginResponseObject, error) {
	if !s.cfg.DevLogin {
		return apigen.DevLogin403JSONResponse{ForbiddenJSONResponse: apigen.ForbiddenJSONResponse{Code: codeForbidden, Message: "dev login is disabled"}}, nil
	}
	email := strings.ToLower(strings.TrimSpace(string(request.Body.Email)))
	if email == "" || !strings.Contains(email, "@") {
		return devLoginErrResponse{errResp(http.StatusBadRequest, codeBadRequest, "invalid email")}, nil
	}
	role := authz.RoleViewer
	if s.cfg.IsAdminEmail(email) {
		role = authz.RoleAdmin
	}
	user, err := s.store.UpsertUserByIdentity(ctx, "dev", email, email, nil, role)
	if err != nil {
		return nil, err
	}
	if user.DisabledAt != nil {
		return apigen.DevLogin403JSONResponse{ForbiddenJSONResponse: apigen.ForbiddenJSONResponse{Code: codeForbidden, Message: "account disabled"}}, nil
	}
	if err := s.sessions.Login(ctx, user.ID); err != nil {
		return nil, err
	}
	return apigen.DevLogin204Response{}, nil
}

// Logout destroys the current session.
func (s *Server) Logout(ctx context.Context, _ apigen.LogoutRequestObject) (apigen.LogoutResponseObject, error) {
	if err := s.sessions.Logout(ctx); err != nil {
		return nil, err
	}
	return apigen.Logout204Response{}, nil
}

// --- customers ---

func (s *Server) ListCustomers(ctx context.Context, request apigen.ListCustomersRequestObject) (apigen.ListCustomersResponseObject, error) {
	var status *string
	if request.Params.Status != nil {
		v := string(*request.Params.Status)
		status = &v
	}
	customers, err := s.store.ListCustomers(ctx, status)
	if err != nil {
		return nil, err
	}
	allowed, scoped := customerScope(ctx)
	out := make([]apigen.Customer, 0, len(customers))
	for _, c := range customers {
		if scoped && !allowed[c.ID] {
			continue
		}
		out = append(out, toCustomer(c))
	}
	return apigen.ListCustomers200JSONResponse{Customers: out}, nil
}

func (s *Server) CreateCustomer(ctx context.Context, request apigen.CreateCustomerRequestObject) (apigen.CreateCustomerResponseObject, error) {
	// Managing the customer roster is a fleet-wide action; a tenant-scoped user
	// cannot create customers (they would then be outside their own scope).
	if err := requireUnscoped(ctx); err != nil {
		return nil, err
	}
	// Resolve + validate the data-residency region against the configured
	// registry (empty = the server default).
	region := s.cfg.DefaultRegion
	if request.Body.Region != nil && *request.Body.Region != "" {
		region = *request.Body.Region
	}
	if !s.cfg.HasRegion(region) {
		return apigen.CreateCustomer400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: fmt.Sprintf("unknown region %q (configured: %s)", region, strings.Join(s.cfg.RegionNames(), ", "))}}, nil
	}
	created, err := s.tenants.CreateCustomer(ctx, actorID(ctx), request.Body.Name, request.Body.Slug, region)
	switch {
	case errors.Is(err, tenants.ErrInvalidName), errors.Is(err, tenants.ErrInvalidSlug):
		return apigen.CreateCustomer400JSONResponse{BadRequestJSONResponse: apigen.BadRequestJSONResponse{Code: codeBadRequest, Message: err.Error()}}, nil
	case errors.Is(err, store.ErrSlugExists):
		return apigen.CreateCustomer409JSONResponse{ConflictJSONResponse: apigen.ConflictJSONResponse{Code: codeConflict, Message: "slug already exists"}}, nil
	case err != nil:
		return nil, err
	}
	return apigen.CreateCustomer201JSONResponse{
		Customer:      toCustomer(created.Customer),
		InitialApiKey: toAPIKeyCreated(created.Key, created.Secret),
	}, nil
}

// ListRegions returns the configured data-residency regions (drives the
// customer region selector). Available to any authenticated user.
func (s *Server) ListRegions(ctx context.Context, _ apigen.ListRegionsRequestObject) (apigen.ListRegionsResponseObject, error) {
	regions := make([]apigen.Region, 0, len(s.cfg.Regions))
	for _, r := range s.cfg.Regions {
		reg := apigen.Region{Name: r.Name}
		if r.DisplayName != "" {
			dn := r.DisplayName
			reg.DisplayName = &dn
		}
		regions = append(regions, reg)
	}
	return apigen.ListRegions200JSONResponse{Regions: regions, DefaultRegion: s.cfg.DefaultRegion}, nil
}

func (s *Server) GetCustomer(ctx context.Context, request apigen.GetCustomerRequestObject) (apigen.GetCustomerResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	c, err := s.store.GetCustomer(ctx, request.CustomerId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.GetCustomer404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.GetCustomer200JSONResponse(toCustomer(c)), nil
}

func (s *Server) UpdateCustomer(ctx context.Context, request apigen.UpdateCustomerRequestObject) (apigen.UpdateCustomerResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	upd := store.CustomerUpdate{Name: request.Body.Name}
	if request.Body.Status != nil {
		v := string(*request.Body.Status)
		upd.Status = &v
	}

	// Nullable PATCH semantics for the quota/retention fields: key present in
	// the JSON = set (an explicit null clears the value), key absent =
	// unchanged. The decoded body cannot distinguish null from absent, so key
	// presence comes from the raw body captured by the router middleware.
	upd.RateLimitItemsPerSec, upd.RetentionDays = quotaRetentionUpdate(ctx, request.Body)
	if v := upd.RateLimitItemsPerSec.Value; upd.RateLimitItemsPerSec.Set && v != nil && *v < 1 {
		return nil, badRequestError{errors.New("rateLimitItemsPerSec must be >= 1 (or null for unlimited)")}
	}
	if v := upd.RetentionDays.Value; upd.RetentionDays.Set && v != nil && (*v < 1 || *v > 30) {
		return nil, badRequestError{errors.New("retentionDays must be between 1 and 30 (or null for the global TTL)")}
	}
	c, err := s.tenants.UpdateCustomer(ctx, actorID(ctx), request.CustomerId, upd)
	switch {
	case errors.Is(err, tenants.ErrInvalidName):
		// The contract enumerates no 400 for PATCH; the router's response
		// error handler maps badRequestError to a 400 Error body.
		return nil, badRequestError{err}
	case errors.Is(err, store.ErrNotFound):
		return apigen.UpdateCustomer404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	case err != nil:
		return nil, err
	}
	return apigen.UpdateCustomer200JSONResponse(toCustomer(c)), nil
}

func (s *Server) DeleteCustomer(ctx context.Context, request apigen.DeleteCustomerRequestObject) (apigen.DeleteCustomerResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	err := s.tenants.DeleteCustomer(ctx, actorID(ctx), request.CustomerId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.DeleteCustomer404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.DeleteCustomer204Response{}, nil
}

// --- api keys ---

func (s *Server) ListApiKeys(ctx context.Context, request apigen.ListApiKeysRequestObject) (apigen.ListApiKeysResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	keys, err := s.store.ListAPIKeys(ctx, request.CustomerId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.ListApiKeys404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]apigen.ApiKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, toAPIKey(k))
	}
	return apigen.ListApiKeys200JSONResponse{ApiKeys: out}, nil
}

func (s *Server) CreateApiKey(ctx context.Context, request apigen.CreateApiKeyRequestObject) (apigen.CreateApiKeyResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	created, err := s.tenants.CreateAPIKey(ctx, actorID(ctx), request.CustomerId, request.Body.Name, request.Body.ExpiresAt)
	switch {
	case errors.Is(err, tenants.ErrInvalidName):
		return createAPIKeyErrResponse{errResp(http.StatusBadRequest, codeBadRequest, err.Error())}, nil
	case errors.Is(err, store.ErrNotFound):
		return apigen.CreateApiKey404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	case err != nil:
		return nil, err
	}
	return apigen.CreateApiKey201JSONResponse(toAPIKeyCreated(created.Key, created.Secret)), nil
}

func (s *Server) RevokeApiKey(ctx context.Context, request apigen.RevokeApiKeyRequestObject) (apigen.RevokeApiKeyResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	err := s.tenants.RevokeAPIKey(ctx, actorID(ctx), request.CustomerId, request.KeyId)
	if errors.Is(err, store.ErrNotFound) {
		return apigen.RevokeApiKey404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "api key not found"}}, nil
	}
	if err != nil {
		return nil, err
	}
	return apigen.RevokeApiKey204Response{}, nil
}

// --- stats ---

func (s *Server) GetStatsOverview(ctx context.Context, request apigen.GetStatsOverviewRequestObject) (apigen.GetStatsOverviewResponseObject, error) {
	from, to := request.Params.From, request.Params.To
	if !to.After(from) {
		return statsOverviewErrResponse{errResp(http.StatusBadRequest, codeBadRequest, "'to' must be after 'from'")}, nil
	}
	allowed, _ := customerScope(ctx)
	ov, err := s.stats.GetOverview(ctx, from, to, allowed)
	if errors.Is(err, stats.ErrUpstreamUnavailable) {
		return statsOverviewErrResponse{errResp(http.StatusServiceUnavailable, codeUpstream, "stats backend unavailable")}, nil
	}
	if err != nil {
		return nil, err
	}

	resp := apigen.GetStatsOverview200JSONResponse{
		ActiveCustomers: ov.ActiveCustomers,
		RefusedRequests: &ov.RefusedRequests,
	}
	resp.Totals.Logs = ov.Totals["logs"]
	resp.Totals.Traces = ov.Totals["traces"]
	resp.Totals.Metrics = ov.Totals["metrics"]
	resp.TopCustomers = make([]struct {
		CustomerId openapi_types.UUID `json:"customerId"`
		Items      int64              `json:"items"`
		Name       string             `json:"name"`
	}, 0, len(ov.TopCustomers))
	for _, tc := range ov.TopCustomers {
		resp.TopCustomers = append(resp.TopCustomers, struct {
			CustomerId openapi_types.UUID `json:"customerId"`
			Items      int64              `json:"items"`
			Name       string             `json:"name"`
		}{CustomerId: tc.CustomerID, Items: tc.Items, Name: tc.Name})
	}
	return resp, nil
}

// GetCostStats serves the per-customer ingest-volume breakdown. Bytes are the
// estimated in-memory row size accumulated by the ClickHouse MVs; rows
// written before the byte-accounting migration report 0 bytes.
func (s *Server) GetCostStats(ctx context.Context, request apigen.GetCostStatsRequestObject) (apigen.GetCostStatsResponseObject, error) {
	from, to := request.Params.From, request.Params.To
	if !to.After(from) {
		return costStatsErrResponse{errResp(http.StatusBadRequest, codeBadRequest, "'to' must be after 'from'")}, nil
	}
	costs, err := s.stats.GetCost(ctx, from, to)
	if errors.Is(err, stats.ErrUpstreamUnavailable) {
		return costStatsErrResponse{errResp(http.StatusServiceUnavailable, codeUpstream, "stats backend unavailable")}, nil
	}
	if err != nil {
		return nil, err
	}

	allowed, scoped := customerScope(ctx)
	resp := apigen.GetCostStats200JSONResponse{Customers: make([]apigen.CustomerCost, 0, len(costs))}
	for _, c := range costs {
		if scoped && !allowed[c.CustomerID] {
			continue
		}
		days := make([]struct {
			Bytes int64              `json:"bytes"`
			Date  openapi_types.Date `json:"date"`
			Items int64              `json:"items"`
		}, 0, len(c.Days))
		for _, d := range c.Days {
			days = append(days, struct {
				Bytes int64              `json:"bytes"`
				Date  openapi_types.Date `json:"date"`
				Items int64              `json:"items"`
			}{Bytes: d.Bytes, Date: openapi_types.Date{Time: d.Date}, Items: d.Items})
		}
		resp.Customers = append(resp.Customers, apigen.CustomerCost{
			CustomerId: c.CustomerID,
			Name:       c.Name,
			Items:      c.Items,
			Bytes:      c.Bytes,
			Days:       days,
		})
	}
	return resp, nil
}

func (s *Server) GetCustomerThroughput(ctx context.Context, request apigen.GetCustomerThroughputRequestObject) (apigen.GetCustomerThroughputResponseObject, error) {
	if err := requireCustomerAccess(ctx, &request.CustomerId); err != nil {
		return nil, err
	}
	from, to := request.Params.From, request.Params.To
	if !to.After(from) {
		return throughputErrResponse{errResp(http.StatusBadRequest, codeBadRequest, "'to' must be after 'from'")}, nil
	}
	step, err := stats.ParseStep(request.Params.Step)
	if err != nil {
		return throughputErrResponse{errResp(http.StatusBadRequest, codeBadRequest, err.Error())}, nil
	}
	var signal *string
	if request.Params.Signal != nil {
		v := string(*request.Params.Signal)
		signal = &v
	}

	series, err := s.stats.GetThroughput(ctx, request.CustomerId, signal, from, to, step)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apigen.GetCustomerThroughput404JSONResponse{NotFoundJSONResponse: apigen.NotFoundJSONResponse{Code: codeNotFound, Message: "customer not found"}}, nil
	case errors.Is(err, stats.ErrUpstreamUnavailable):
		return throughputErrResponse{errResp(http.StatusServiceUnavailable, codeUpstream, "stats backend unavailable")}, nil
	case err != nil:
		return nil, err
	}

	out := apigen.ThroughputResponse{Series: make([]apigen.ThroughputSeries, 0, len(series))}
	for _, sr := range series {
		points := make([]apigen.ThroughputPoint, 0, len(sr.Points))
		for _, p := range sr.Points {
			points = append(points, apigen.ThroughputPoint{Ts: p.Ts, Value: float32(p.Value)})
		}
		out.Series = append(out.Series, apigen.ThroughputSeries{Signal: apigen.Signal(sr.Signal), Points: points})
	}
	return apigen.GetCustomerThroughput200JSONResponse(out), nil
}

// quotaRetentionUpdate derives the tri-state quota/retention updates from the
// decoded PATCH body plus the raw-body key presence. Without a captured raw
// body (unreachable via the router) it falls back to "non-nil pointer = set",
// which cannot express clearing.
func quotaRetentionUpdate(ctx context.Context, body *apigen.UpdateCustomerJSONRequestBody) (rateLimit, retention store.OptionalInt) {
	raw, ok := rawBodyFrom(ctx)
	if !ok {
		if body.RateLimitItemsPerSec != nil {
			rateLimit = store.OptionalInt{Set: true, Value: body.RateLimitItemsPerSec}
		}
		if body.RetentionDays != nil {
			retention = store.OptionalInt{Set: true, Value: body.RetentionDays}
		}
		return rateLimit, retention
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return rateLimit, retention // strict decode already succeeded; defensive only
	}
	if _, present := keys["rateLimitItemsPerSec"]; present {
		rateLimit = store.OptionalInt{Set: true, Value: body.RateLimitItemsPerSec}
	}
	if _, present := keys["retentionDays"]; present {
		retention = store.OptionalInt{Set: true, Value: body.RetentionDays}
	}
	return rateLimit, retention
}

// badRequestError marks handler errors that must surface as 400 rather than
// 500; the router's ResponseErrorHandlerFunc unwraps it.
type badRequestError struct{ err error }

func (e badRequestError) Error() string { return e.err.Error() }
func (e badRequestError) Unwrap() error { return e.err }

// forbiddenError marks handler errors that must surface as 403 (tenant-scope
// violations); the router's ResponseErrorHandlerFunc unwraps it.
type forbiddenError struct{ err error }

func (e forbiddenError) Error() string { return e.err.Error() }
func (e forbiddenError) Unwrap() error { return e.err }
