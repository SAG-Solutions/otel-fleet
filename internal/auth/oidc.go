package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Session keys used during the OAuth/OIDC dance (shared by all providers; a
// session runs one login flow at a time).
const (
	sessOIDCState    = "oidc_state"
	sessOIDCVerifier = "oidc_verifier"
)

// UserUpserter is the subset of the store the login handlers need.
type UserUpserter interface {
	UpsertUserByIdentity(ctx context.Context, provider, subject, email string, displayName *string, roleIfNew string) (store.User, error)
}

// microsoftIssuerPattern matches the tenant-specific issuers Entra ID puts in
// ID tokens obtained via the multi-tenant (common) endpoint.
var microsoftIssuerPattern = regexp.MustCompile(`^https://login\.microsoftonline\.com/[0-9a-fA-F-]+/v2\.0$`)

// OIDCHandler serves /auth/{name}/start and /auth/{name}/callback for one
// resolved OIDC provider (types oidc, google, microsoft). The upstream
// provider metadata is discovered lazily so the control plane starts even
// when the IdP is briefly unreachable.
//
// Microsoft note: the multi-tenant endpoint's discovery document advertises
// the literal "{tenantid}" issuer template and ID tokens carry per-tenant
// issuers, so go-oidc's strict issuer checks are relaxed for TypeMicrosoft
// (InsecureIssuerURLContext + SkipIssuerCheck) and the token issuer is
// verified against microsoftIssuerPattern instead.
type OIDCHandler struct {
	info    ProviderInfo
	baseURL string
	finish  loginFinisher
	log     *slog.Logger

	mu       sync.Mutex
	provider *oidc.Provider
}

// NewOIDCHandler wires an OIDC login flow for the given resolved provider.
func NewOIDCHandler(info ProviderInfo, baseURL string, finisher loginFinisher) *OIDCHandler {
	return &OIDCHandler{info: info, baseURL: baseURL, finish: finisher, log: finisher.log}
}

// Name returns the provider's URL-safe name.
func (h *OIDCHandler) Name() string { return h.info.Name }

func (h *OIDCHandler) getProvider(ctx context.Context) (*oidc.Provider, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.provider != nil {
		return h.provider, nil
	}
	if h.info.Type == TypeMicrosoft {
		// The discovery document's issuer is the "{tenantid}" template, which
		// never equals the configured URL; skip that check.
		ctx = oidc.InsecureIssuerURLContext(ctx, h.info.Issuer)
	}
	p, err := oidc.NewProvider(ctx, h.info.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", h.info.Issuer, err)
	}
	h.provider = p
	return p, nil
}

func (h *OIDCHandler) oauthConfig(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     h.info.ClientID,
		ClientSecret: h.info.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  h.baseURL + "/auth/" + h.info.Name + "/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
}

// Start redirects the browser to the IdP's authorization endpoint (PKCE S256,
// state bound to the session).
func (h *OIDCHandler) Start(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.getProvider(ctx)
	if err != nil {
		h.log.Error("oidc start: discovery failed", "provider", h.info.Name, "err", err)
		http.Error(w, "identity provider unavailable", http.StatusBadGateway)
		return
	}
	state := newToken()
	verifier := oauth2.GenerateVerifier()
	h.finish.sessions.Manager.Put(ctx, sessOIDCState, state)
	h.finish.sessions.Manager.Put(ctx, sessOIDCVerifier, verifier)

	url := h.oauthConfig(p).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback finishes the flow: state check, code exchange (PKCE), ID token
// verification, user upsert and session login. On success it redirects to /.
func (h *OIDCHandler) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	wantState := h.finish.sessions.Manager.PopString(ctx, sessOIDCState)
	verifier := h.finish.sessions.Manager.PopString(ctx, sessOIDCVerifier)
	if wantState == "" || r.URL.Query().Get("state") != wantState {
		http.Error(w, "invalid OIDC state", http.StatusBadRequest)
		return
	}
	if errCode := r.URL.Query().Get("error"); errCode != "" {
		h.log.Warn("oidc callback: provider returned error", "provider", h.info.Name, "error", errCode)
		http.Error(w, "login failed: "+errCode, http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	p, err := h.getProvider(ctx)
	if err != nil {
		h.log.Error("oidc callback: discovery failed", "provider", h.info.Name, "err", err)
		http.Error(w, "identity provider unavailable", http.StatusBadGateway)
		return
	}
	token, err := h.oauthConfig(p).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		h.log.Warn("oidc callback: code exchange failed", "provider", h.info.Name, "err", err)
		http.Error(w, "code exchange failed", http.StatusBadGateway)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		http.Error(w, "no id_token in token response", http.StatusBadGateway)
		return
	}
	verifierCfg := &oidc.Config{ClientID: h.info.ClientID}
	if h.info.Type == TypeMicrosoft {
		verifierCfg.SkipIssuerCheck = true // tenant-specific issuer, checked below
	}
	idToken, err := p.Verifier(verifierCfg).Verify(ctx, rawIDToken)
	if err != nil {
		h.log.Warn("oidc callback: id token verification failed", "provider", h.info.Name, "err", err)
		http.Error(w, "invalid id_token", http.StatusBadRequest)
		return
	}
	if h.info.Type == TypeMicrosoft && !microsoftIssuerPattern.MatchString(idToken.Issuer) {
		h.log.Warn("oidc callback: unexpected microsoft issuer", "provider", h.info.Name, "issuer", idToken.Issuer)
		http.Error(w, "invalid id_token issuer", http.StatusBadRequest)
		return
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "cannot parse id_token claims", http.StatusBadRequest)
		return
	}
	if claims.Email == "" {
		http.Error(w, "id_token contains no email", http.StatusBadRequest)
		return
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		http.Error(w, "email not verified at identity provider", http.StatusForbidden)
		return
	}

	var displayName *string
	if claims.Name != "" {
		displayName = &claims.Name
	}
	h.finish.finish(w, r, h.info.IdentityKey(), idToken.Subject, claims.Email, displayName)
}
