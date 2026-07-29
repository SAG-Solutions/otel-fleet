// Package auth implements browser authentication for the control plane:
// cookie sessions (scs), CSRF tokens, dev login support and generic OIDC.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Session keys.
const (
	sessUserID = "user_id"
	sessCSRF   = "csrf_token"
)

// Principal is the authenticated user attached to a request context.
type Principal struct {
	User      store.User
	CSRFToken string
	// Tenant-scoped RBAC. AllCustomers grants access to every customer (admins,
	// API tokens, and non-admins with no grants). Otherwise the user is limited
	// to AllowedCustomers.
	AllCustomers     bool
	AllowedCustomers map[uuid.UUID]bool
}

// CanAccessCustomer reports whether this principal may act on the given
// customer. A nil id (a resource with no customer, e.g. a gateway agent)
// requires unscoped access.
func (p Principal) CanAccessCustomer(id *uuid.UUID) bool {
	if p.AllCustomers {
		return true
	}
	return id != nil && p.AllowedCustomers[*id]
}

type ctxKey int

const principalKey ctxKey = iota

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom extracts the authenticated principal, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// Sessions wraps the scs session manager with otel-fleet-specific helpers.
type Sessions struct {
	Manager *scs.SessionManager
}

// NewSessions creates a session manager backed by an in-memory store.
// Production callers must call UsePostgres before serving traffic. The cookie
// is __Host-otel_fleet_session when secure, otel_fleet_session otherwise.
func NewSessions(secure bool) *Sessions {
	m := scs.New()
	m.Lifetime = 24 * time.Hour
	m.Cookie.Name = "otel_fleet_session"
	if secure {
		m.Cookie.Name = "__Host-otel_fleet_session"
	}
	m.Cookie.HttpOnly = true
	m.Cookie.SameSite = http.SameSiteLaxMode
	m.Cookie.Secure = secure
	m.Cookie.Path = "/"
	return &Sessions{Manager: m}
}

// UsePostgres switches session storage to the sessions table via pgxstore.
func (s *Sessions) UsePostgres(pool *pgxpool.Pool) {
	s.Manager.Store = pgxstore.New(pool)
}

// Login binds the session to userID: the token is renewed (session fixation)
// and a fresh CSRF token is minted.
func (s *Sessions) Login(ctx context.Context, userID uuid.UUID) error {
	if err := s.Manager.RenewToken(ctx); err != nil {
		return fmt.Errorf("renew session token: %w", err)
	}
	s.Manager.Put(ctx, sessUserID, userID.String())
	s.Manager.Put(ctx, sessCSRF, newToken())
	return nil
}

// Logout destroys the current session.
func (s *Sessions) Logout(ctx context.Context) error {
	return s.Manager.Destroy(ctx)
}

// UserID returns the authenticated user's ID, if the session has one.
func (s *Sessions) UserID(ctx context.Context) (uuid.UUID, bool) {
	raw := s.Manager.GetString(ctx, sessUserID)
	if raw == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// CSRFToken returns the session's CSRF token, minting one if absent.
func (s *Sessions) CSRFToken(ctx context.Context) string {
	tok := s.Manager.GetString(ctx, sessCSRF)
	if tok == "" {
		tok = newToken()
		s.Manager.Put(ctx, sessCSRF, tok)
	}
	return tok
}

// ValidCSRF reports whether presented matches the session's CSRF token.
func (s *Sessions) ValidCSRF(ctx context.Context, presented string) bool {
	tok := s.Manager.GetString(ctx, sessCSRF)
	if tok == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(presented)) == 1
}

func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b[:])
}
