package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/auth"
	"github.com/sag-solutions/otel-fleet/internal/authz"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Paths reachable without a session.
var publicPaths = map[string]struct{}{
	"/api/v1/auth/providers": {},
	"/api/v1/auth/dev-login": {},
}

// logoutPath is authenticated but exempt from the operator-role requirement:
// every role may end its own session.
const logoutPath = "/api/v1/auth/logout"

// adminPathPrefixes are admin-only for every method, including GET: user
// management, SSO provider settings and the audit log.
var adminPathPrefixes = []string{
	"/api/v1/users",
	"/api/v1/settings/auth-providers",
	"/api/v1/settings/api-tokens",
	"/api/v1/settings/alert-rules",
	"/api/v1/settings/maintenance-windows",
	"/api/v1/settings/reencrypt-secrets",
	"/api/v1/settings/billing",
	"/api/v1/billing",
	"/api/v1/audit",
	"/api/v1/metrics",
}

func isAdminOnlyPath(path string) bool {
	for _, p := range adminPathPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// GuardStore is the store subset the Guard middleware needs: the session user
// load, per-customer grants, plus management-API token validation.
type GuardStore interface {
	GetUser(ctx context.Context, id uuid.UUID) (store.User, error)
	ListUserCustomerIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	tokenStore
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// Guard enforces, in order: session authentication (401), admin-only areas
// (403, all methods), CSRF on mutating requests (403), and RBAC — mutations
// require operator or admin (403). The resolved principal is attached to the
// request context. The per-request user load doubles as the disabled check:
// a disabled user's next request fails even if a session row survived.
func Guard(sessions *auth.Sessions, users GuardStore, log *slog.Logger, metrics *securityMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := publicPaths[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			ctx := r.Context()

			// Management-API token (otm_pat_) auth: for programmatic clients
			// (CLI/CI). Not cookie-based, so it is exempt from CSRF; the token's
			// own role drives RBAC. Falls through to session auth otherwise.
			if looksLikeAPIToken(r.Header.Get("Authorization")) {
				role, createdBy, ok := authenticateAPIToken(ctx, users, r.Header.Get("Authorization"))
				if !ok {
					denyRequest(w, r, log, metrics, http.StatusUnauthorized, codeUnauthorized, "invalid API token", reasonInvalidToken, "")
					return
				}
				if isAdminOnlyPath(r.URL.Path) && !authz.AtLeast(role, authz.RoleAdmin) {
					denyRequest(w, r, log, metrics, http.StatusForbidden, codeForbidden, "requires admin role", reasonRequiresAdmin, "api-token")
					return
				}
				if isMutating(r.Method) && r.URL.Path != logoutPath && !authz.CanMutate(role) {
					denyRequest(w, r, log, metrics, http.StatusForbidden, codeForbidden, "requires operator or admin role", reasonInsufficient, "api-token")
					return
				}
				tokenUser := store.User{Role: role, Email: "api-token"}
				if createdBy != nil {
					tokenUser.ID = *createdBy // audit attributes to the token's creator
				}
				// Management-API tokens are unscoped (automation acts fleet-wide).
				ctx = auth.WithPrincipal(ctx, auth.Principal{User: tokenUser, AllCustomers: true})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			userID, ok := sessions.UserID(ctx)
			if !ok {
				denyRequest(w, r, log, metrics, http.StatusUnauthorized, codeUnauthorized, "authentication required", reasonUnauthenticated, "")
				return
			}
			user, err := users.GetUser(ctx, userID)
			if errors.Is(err, store.ErrNotFound) {
				denyRequest(w, r, log, metrics, http.StatusUnauthorized, codeUnauthorized, "unknown user", reasonUnknownUser, "")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, codeInternal, "internal server error")
				return
			}
			if user.DisabledAt != nil {
				denyRequest(w, r, log, metrics, http.StatusUnauthorized, codeUnauthorized, "account disabled", reasonAccountDisabled, user.Email)
				return
			}

			if isAdminOnlyPath(r.URL.Path) && !authz.AtLeast(user.Role, authz.RoleAdmin) {
				denyRequest(w, r, log, metrics, http.StatusForbidden, codeForbidden, "requires admin role", reasonRequiresAdmin, user.Email)
				return
			}

			if isMutating(r.Method) {
				if !sessions.ValidCSRF(ctx, r.Header.Get("X-CSRF-Token")) {
					denyRequest(w, r, log, metrics, http.StatusForbidden, codeForbidden, "missing or invalid CSRF token", reasonCSRF, user.Email)
					return
				}
				if r.URL.Path != logoutPath && !authz.CanMutate(user.Role) {
					denyRequest(w, r, log, metrics, http.StatusForbidden, codeForbidden, "requires operator or admin role", reasonInsufficient, user.Email)
					return
				}
			}

			p := auth.Principal{
				User:      user,
				CSRFToken: sessions.CSRFToken(ctx),
			}
			// Tenant scoping: admins are always unscoped. A non-admin is limited
			// to their granted customers; a non-admin with no grants stays
			// unscoped (backward compatible).
			if authz.AtLeast(user.Role, authz.RoleAdmin) {
				p.AllCustomers = true
			} else {
				grants, err := users.ListUserCustomerIDs(ctx, user.ID)
				if err != nil {
					writeError(w, http.StatusInternalServerError, codeInternal, "internal server error")
					return
				}
				if len(grants) == 0 {
					p.AllCustomers = true
				} else {
					p.AllowedCustomers = make(map[uuid.UUID]bool, len(grants))
					for _, id := range grants {
						p.AllowedCustomers[id] = true
					}
				}
			}

			ctx = auth.WithPrincipal(ctx, p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
