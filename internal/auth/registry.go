package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sag-solutions/otel-fleet/internal/config"
	"github.com/sag-solutions/otel-fleet/internal/crypto"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// Provider types (mirror the auth_providers.type check).
const (
	TypeGoogle    = "google"
	TypeMicrosoft = "microsoft"
	TypeGitHub    = "github"
	TypeOIDC      = "oidc"
	TypeSAML      = "saml"
)

// SAMLConfig is a SAML provider's (non-secret) IdP configuration, stored as
// JSON in auth_providers.saml_config.
type SAMLConfig struct {
	IDPEntityID    string `json:"idpEntityId"`    // IdP entity id / issuer
	IDPSSOURL      string `json:"idpSsoUrl"`      // IdP HTTP-Redirect SSO endpoint
	IDPCertificate string `json:"idpCertificate"` // IdP signing cert (PEM or base64 DER)
}

// Fixed issuers for the well-known provider types.
const (
	GoogleIssuer = "https://accounts.google.com"
	// MicrosoftIssuer is the multi-tenant Entra ID endpoint. Its discovery
	// document carries the literal "{tenantid}" issuer template and ID tokens
	// carry tenant-specific issuers, so issuer validation is relaxed for this
	// type (see OIDCHandler).
	MicrosoftIssuer = "https://login.microsoftonline.com/common/v2.0"
)

// KnownProviderType reports whether t is a supported provider type.
func KnownProviderType(t string) bool {
	switch t {
	case TypeGoogle, TypeMicrosoft, TypeGitHub, TypeOIDC, TypeSAML:
		return true
	}
	return false
}

// EffectiveIssuer resolves the issuer URL for a provider type (the stored
// issuer only matters for type oidc; github is not OIDC and has none).
func EffectiveIssuer(providerType, issuer string) string {
	switch providerType {
	case TypeGoogle:
		return GoogleIssuer
	case TypeMicrosoft:
		return MicrosoftIssuer
	case TypeGitHub:
		return ""
	default:
		return issuer
	}
}

// ProviderInfo is a fully resolved login provider (secret decrypted), the
// input for building a login flow.
type ProviderInfo struct {
	Type         string // google | microsoft | github | oidc | saml
	Name         string // URL slug: /auth/{name}/start
	DisplayName  string
	Issuer       string // effective issuer; "" for github/saml
	ClientID     string
	ClientSecret string
	SAML         *SAMLConfig // set for type saml
}

// IdentityKey is the user_identities.provider namespace for this provider:
// the bare type for the well-known IdPs (subjects are globally stable there)
// and "oidc:<name>" for custom OIDC providers (per-issuer subject spaces).
func (p ProviderInfo) IdentityKey() string {
	switch p.Type {
	case TypeOIDC:
		return "oidc:" + p.Name
	case TypeSAML:
		return "saml:" + p.Name
	}
	return p.Type
}

// LoginProvider is the public login-page view of a provider.
type LoginProvider struct {
	Name        string
	DisplayName string
}

// ProviderStore is the store subset the registry needs.
type ProviderStore interface {
	UserUpserter
	ListAuthProviders(ctx context.Context, enabledOnly bool) ([]store.AuthProvider, error)
	GetAuthProviderByName(ctx context.Context, name string) (store.AuthProvider, error)
}

// ErrUnknownProvider means no enabled provider answers to the requested name.
var ErrUnknownProvider = errors.New("unknown login provider")

// loginFlow is one provider's browser flow. Implemented by OIDCHandler and
// GitHubHandler.
type loginFlow interface {
	Start(w http.ResponseWriter, r *http.Request)
	Callback(w http.ResponseWriter, r *http.Request)
}

// Registry resolves login providers at request time: enabled database
// providers plus the OTEL_FLEET_OIDC_* environment provider as fallback under
// its configured name. Flow handlers are cached per provider version so OIDC
// discovery stays lazy and warm across requests.
type Registry struct {
	baseURL  string
	env      []config.OIDCProvider
	store    ProviderStore
	cipher   *crypto.Cipher
	sessions *Sessions
	isAdmin  func(email string) bool
	log      *slog.Logger

	mu    sync.Mutex
	flows map[string]cachedFlow
}

type cachedFlow struct {
	fingerprint string
	flow        loginFlow
}

// NewRegistry wires the provider registry.
func NewRegistry(cfg *config.Config, st ProviderStore, cipher *crypto.Cipher, sessions *Sessions, log *slog.Logger) *Registry {
	return &Registry{
		baseURL:  cfg.BaseURL,
		env:      cfg.OIDCProviders,
		store:    st,
		cipher:   cipher,
		sessions: sessions,
		isAdmin:  cfg.IsAdminEmail,
		log:      log,
		flows:    map[string]cachedFlow{},
	}
}

// RedirectURI is the callback URL to register at the identity provider.
func (reg *Registry) RedirectURI(name string) string {
	return reg.baseURL + "/auth/" + name + "/callback"
}

// ACSURL is the SAML assertion-consumer URL to register at the IdP.
func (reg *Registry) ACSURL(name string) string { return acsURL(reg.baseURL, name) }

// SPEntityID is the SAML SP entity id / audience to register at the IdP.
func (reg *Registry) SPEntityID(name string) string { return spEntityID(reg.baseURL, name) }

// LoginProviders lists what the login page offers: enabled database providers
// plus environment providers (database wins on name collision).
func (reg *Registry) LoginProviders(ctx context.Context) ([]LoginProvider, error) {
	dbProviders, err := reg.store.ListAuthProviders(ctx, true)
	if err != nil {
		return nil, err
	}
	out := []LoginProvider{}
	taken := map[string]bool{}
	for _, p := range dbProviders {
		out = append(out, LoginProvider{Name: p.Name, DisplayName: p.DisplayName})
		taken[p.Name] = true
	}
	for _, p := range reg.env {
		if !taken[p.Name] {
			out = append(out, LoginProvider{Name: p.Name, DisplayName: p.DisplayName})
		}
	}
	return out, nil
}

// Resolve finds the enabled provider behind a URL name and decrypts its
// client secret. Database providers shadow environment providers.
func (reg *Registry) Resolve(ctx context.Context, name string) (ProviderInfo, string, error) {
	p, err := reg.store.GetAuthProviderByName(ctx, name)
	switch {
	case err == nil:
		if !p.Enabled {
			return ProviderInfo{}, "", ErrUnknownProvider
		}
		issuer := ""
		if p.Issuer != nil {
			issuer = *p.Issuer
		}
		info := ProviderInfo{
			Type:        p.Type,
			Name:        p.Name,
			DisplayName: p.DisplayName,
			Issuer:      EffectiveIssuer(p.Type, issuer),
			ClientID:    p.ClientID,
		}
		if p.Type == TypeSAML {
			// SAML carries no client secret; its IdP config is non-secret JSON.
			var cfg SAMLConfig
			if err := json.Unmarshal(p.SAMLConfig, &cfg); err != nil {
				return ProviderInfo{}, "", fmt.Errorf("parse saml config of provider %q: %w", name, err)
			}
			info.SAML = &cfg
		} else {
			secret, err := reg.cipher.Decrypt(p.ClientSecretEnc)
			if err != nil {
				return ProviderInfo{}, "", fmt.Errorf("decrypt client secret of provider %q: %w", name, err)
			}
			info.ClientSecret = string(secret)
		}
		return info, fmt.Sprintf("db:%s:%d", p.ID, p.UpdatedAt.UnixNano()), nil
	case errors.Is(err, store.ErrNotFound):
		for _, e := range reg.env {
			if e.Name == name {
				info := ProviderInfo{
					Type:         TypeOIDC,
					Name:         e.Name,
					DisplayName:  e.DisplayName,
					Issuer:       e.Issuer,
					ClientID:     e.ClientID,
					ClientSecret: e.ClientSecret,
				}
				return info, "env:" + e.Name, nil
			}
		}
		return ProviderInfo{}, "", ErrUnknownProvider
	default:
		return ProviderInfo{}, "", err
	}
}

// flowFor returns the cached flow handler for a provider, rebuilding it when
// the provider changed (fingerprint mismatch).
func (reg *Registry) flowFor(info ProviderInfo, fingerprint string) loginFlow {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if c, ok := reg.flows[info.Name]; ok && c.fingerprint == fingerprint {
		return c.flow
	}
	finisher := loginFinisher{sessions: reg.sessions, store: reg.store, isAdmin: reg.isAdmin, log: reg.log}
	var flow loginFlow
	switch info.Type {
	case TypeGitHub:
		flow = NewGitHubHandler(info, reg.baseURL, finisher)
	case TypeSAML:
		flow = NewSAMLHandler(info, reg.baseURL, finisher)
	default:
		flow = NewOIDCHandler(info, reg.baseURL, finisher)
	}
	reg.flows[info.Name] = cachedFlow{fingerprint: fingerprint, flow: flow}
	return flow
}

// Start serves GET /auth/{name}/start.
func (reg *Registry) Start(w http.ResponseWriter, r *http.Request) {
	reg.dispatch(w, r, func(f loginFlow) { f.Start(w, r) })
}

// Callback serves GET /auth/{name}/callback.
func (reg *Registry) Callback(w http.ResponseWriter, r *http.Request) {
	reg.dispatch(w, r, func(f loginFlow) { f.Callback(w, r) })
}

// samlFlow is the extra surface a SAML provider exposes beyond loginFlow: the
// assertion-consumer POST endpoint and the SP metadata document.
type samlFlow interface {
	ACS(w http.ResponseWriter, r *http.Request)
	Metadata(w http.ResponseWriter, r *http.Request)
}

// ACS serves POST /auth/{name}/acs (SAML assertion consumer). Non-SAML
// providers 404.
func (reg *Registry) ACS(w http.ResponseWriter, r *http.Request) {
	reg.dispatch(w, r, func(f loginFlow) {
		if sf, ok := f.(samlFlow); ok {
			sf.ACS(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

// Metadata serves GET /auth/{name}/metadata (SP metadata XML). Non-SAML
// providers 404.
func (reg *Registry) Metadata(w http.ResponseWriter, r *http.Request) {
	reg.dispatch(w, r, func(f loginFlow) {
		if sf, ok := f.(samlFlow); ok {
			sf.Metadata(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func (reg *Registry) dispatch(w http.ResponseWriter, r *http.Request, serve func(loginFlow)) {
	name := providerNameFromPath(r.URL.Path)
	if name == "" {
		http.NotFound(w, r)
		return
	}
	info, fingerprint, err := reg.Resolve(r.Context(), name)
	if errors.Is(err, ErrUnknownProvider) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		reg.log.Error("resolve login provider failed", "provider", name, "err", err)
		http.Error(w, "login provider unavailable", http.StatusInternalServerError)
		return
	}
	serve(reg.flowFor(info, fingerprint))
}

// providerNameFromPath extracts {name} from /auth/{name}/(start|callback).
func providerNameFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "auth" {
		return ""
	}
	return parts[1]
}

// TestProviderConnectivity checks a provider's upstream without touching or
// leaking secrets: OIDC discovery for oidc/google/microsoft, API reachability
// for github. The bool is "usable", the message explains.
func TestProviderConnectivity(ctx context.Context, info ProviderInfo) (bool, string) {
	client := &http.Client{Timeout: 10 * time.Second}

	if info.Type == TypeSAML {
		if info.SAML == nil {
			return false, "SAML config missing"
		}
		if err := ValidateSAMLConfig(*info.SAML); err != nil {
			return false, "SAML config invalid: " + err.Error()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.SAML.IDPSSOURL, nil)
		if err != nil {
			return false, "build request: " + err.Error()
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, "IdP SSO URL unreachable: " + err.Error()
		}
		defer resp.Body.Close() //nolint:errcheck
		return true, fmt.Sprintf("config valid; IdP SSO URL reachable (HTTP %d)", resp.StatusCode)
	}

	if info.Type == TypeGitHub {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/", nil)
		if err != nil {
			return false, "build request: " + err.Error()
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, "GitHub API unreachable: " + err.Error()
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode >= 500 {
			return false, fmt.Sprintf("GitHub API returned HTTP %d", resp.StatusCode)
		}
		return true, "GitHub API reachable"
	}

	wellKnown := strings.TrimSuffix(info.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return false, "build request: " + err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "OIDC discovery failed: " + err.Error()
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("OIDC discovery returned HTTP %d for %s", resp.StatusCode, wellKnown)
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return false, "OIDC discovery document is not valid JSON: " + err.Error()
	}
	if doc.Issuer == strings.TrimSuffix(info.Issuer, "/") || doc.Issuer == info.Issuer {
		return true, "OIDC discovery OK, issuer matches"
	}
	if info.Type == TypeMicrosoft {
		// The multi-tenant endpoint advertises the tenanted issuer template;
		// per-tenant issuers are validated at login time instead.
		return true, fmt.Sprintf("OIDC discovery OK; Microsoft multi-tenant endpoint advertises the tenanted issuer template %q (expected, validated per tenant at login)", doc.Issuer)
	}
	return false, fmt.Sprintf("issuer mismatch: discovery document says %q, configured %q", doc.Issuer, info.Issuer)
}
