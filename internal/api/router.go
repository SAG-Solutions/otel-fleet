package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/jansagurna/otelfleet/internal/api/apigen"
	"github.com/jansagurna/otelfleet/internal/auth"
	"github.com/jansagurna/otelfleet/internal/config"
	"github.com/jansagurna/otelfleet/internal/scim"
	"github.com/jansagurna/otelfleet/internal/store"
)

// RouterDeps carries everything the HTTP router needs.
type RouterDeps struct {
	Config   *config.Config
	Store    store.Store
	Sessions *auth.Sessions
	Server   *Server
	Auth     *auth.Registry
	Log      *slog.Logger
}

// NewRouter assembles the public HTTP surface: the REST API under /api/v1
// (session-guarded), the SSO browser flows under /auth/{name}/... (providers
// resolved at request time from the database + environment), and the SPA
// fallback.
func NewRouter(d RouterDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(requestLogger(d.Log))
	r.Use(chimw.Recoverer)
	// Per-IP rate limiting across the whole surface; a stricter bucket is
	// layered onto the SSO endpoints below. Must sit behind RealIP.
	var authLimit func(http.Handler) http.Handler
	if d.Config.RateLimitEnabled {
		r.Use(newIPRateLimiter(d.Config.RateLimitRPS, d.Config.RateLimitBurst).middleware())
		authLimit = newIPRateLimiter(d.Config.AuthRateLimitRPS, d.Config.AuthRateLimitBurst).middleware()
	}
	r.Use(d.Sessions.Manager.LoadAndSave)

	// SSO browser flows: an extra, stricter per-IP limiter to blunt abuse of
	// the login/callback/assertion endpoints.
	r.Group(func(g chi.Router) {
		if authLimit != nil {
			g.Use(authLimit)
		}
		g.Get("/auth/{provider}/start", d.Auth.Start)
		g.Get("/auth/{provider}/callback", d.Auth.Callback)
		// SAML SP endpoints: the IdP POSTs the assertion to /acs and consumes /metadata.
		g.Post("/auth/{provider}/acs", d.Auth.ACS)
		g.Get("/auth/{provider}/metadata", d.Auth.Metadata)
	})

	// SCIM 2.0 user provisioning for identity providers. Authenticated with an
	// admin management-API token (not a session), so it sits outside the Guard.
	scimSrv := scim.New(d.Store, func(ctx context.Context, authorization string) (string, bool) {
		role, _, ok := authenticateAPIToken(ctx, d.Store, authorization)
		return role, ok
	}, d.Config.SCIMDefaultRole, d.Log)
	r.Mount("/scim/v2", scimSrv.Router())

	strict := apigen.NewStrictHandlerWithOptions(d.Server, nil, apigen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			var bad badRequestError
			if errors.As(err, &bad) {
				writeError(w, http.StatusBadRequest, codeBadRequest, bad.Error())
				return
			}
			var forbidden forbiddenError
			if errors.As(err, &forbidden) {
				writeError(w, http.StatusForbidden, codeForbidden, forbidden.Error())
				return
			}
			d.Log.Error("handler failed", "method", r.Method, "path", r.URL.Path, "err", err)
			writeError(w, http.StatusInternalServerError, codeInternal, "internal server error")
		},
	})
	r.Group(func(g chi.Router) {
		if d.Config.MaxRequestBodyBytes > 0 {
			g.Use(maxBodyBytes(d.Config.MaxRequestBodyBytes))
		}
		g.Use(Guard(d.Sessions, d.Store))
		g.Use(captureRawBody)
		apigen.HandlerWithOptions(strict, apigen.ChiServerOptions{
			BaseRouter: g,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
			},
		})
	})

	r.NotFound(spaHandler(d.Config.WebDir))
	return r
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
