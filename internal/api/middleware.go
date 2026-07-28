package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/jansagurna/otelfleet/internal/auth"
	"github.com/jansagurna/otelfleet/internal/authz"
	"github.com/jansagurna/otelfleet/internal/store"
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
	"/api/v1/settings/billing",
	"/api/v1/billing",
	"/api/v1/audit",
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
func Guard(sessions *auth.Sessions, users GuardStore) func(http.Handler) http.Handler {
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
					writeError(w, http.StatusUnauthorized, codeUnauthorized, "invalid API token")
					return
				}
				if isAdminOnlyPath(r.URL.Path) && !authz.AtLeast(role, authz.RoleAdmin) {
					writeError(w, http.StatusForbidden, codeForbidden, "requires admin role")
					return
				}
				if isMutating(r.Method) && r.URL.Path != logoutPath && !authz.CanMutate(role) {
					writeError(w, http.StatusForbidden, codeForbidden, "requires operator or admin role")
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
				writeError(w, http.StatusUnauthorized, codeUnauthorized, "authentication required")
				return
			}
			user, err := users.GetUser(ctx, userID)
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, codeUnauthorized, "unknown user")
				return
			}
			if err != nil {
				writeError(w, http.StatusInternalServerError, codeInternal, "internal server error")
				return
			}
			if user.DisabledAt != nil {
				writeError(w, http.StatusUnauthorized, codeUnauthorized, "account disabled")
				return
			}

			if isAdminOnlyPath(r.URL.Path) && !authz.AtLeast(user.Role, authz.RoleAdmin) {
				writeError(w, http.StatusForbidden, codeForbidden, "requires admin role")
				return
			}

			if isMutating(r.Method) {
				if !sessions.ValidCSRF(ctx, r.Header.Get("X-CSRF-Token")) {
					writeError(w, http.StatusForbidden, codeForbidden, "missing or invalid CSRF token")
					return
				}
				if r.URL.Path != logoutPath && !authz.CanMutate(user.Role) {
					writeError(w, http.StatusForbidden, codeForbidden, "requires operator or admin role")
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
