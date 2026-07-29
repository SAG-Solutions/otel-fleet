package auth

import (
	"log/slog"
	"net/http"

	"github.com/sag-solutions/otel-fleet/internal/authz"
)

// loginFinisher is the provider-independent tail of every browser login:
// upsert the user by identity, refuse disabled accounts, bind the session.
type loginFinisher struct {
	sessions *Sessions
	store    UserUpserter
	isAdmin  func(email string) bool
	log      *slog.Logger
}

// finish completes a verified external login and redirects to /.
func (f loginFinisher) finish(w http.ResponseWriter, r *http.Request, identityKey, subject, email string, displayName *string) {
	ctx := r.Context()
	role := authz.RoleViewer
	if f.isAdmin(email) {
		role = authz.RoleAdmin
	}
	user, err := f.store.UpsertUserByIdentity(ctx, identityKey, subject, email, displayName, role)
	if err != nil {
		f.log.Error("login: user upsert failed", "provider", identityKey, "err", err)
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	if user.DisabledAt != nil {
		http.Error(w, "account disabled", http.StatusForbidden)
		return
	}
	if err := f.sessions.Login(ctx, user.ID); err != nil {
		f.log.Error("login: session login failed", "err", err)
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}
