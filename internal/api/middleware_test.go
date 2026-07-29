package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/auth"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

type fakeUsers struct {
	users  map[uuid.UUID]store.User
	grants map[uuid.UUID][]uuid.UUID // userID -> granted customer IDs (tenant scope)
}

func (f *fakeUsers) GetUser(_ context.Context, id uuid.UUID) (store.User, error) {
	u, ok := f.users[id]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return u, nil
}

// ActiveAPITokensByPrefix: no API tokens in the middleware session tests.
func (f *fakeUsers) ActiveAPITokensByPrefix(_ context.Context, _ string) ([]store.APITokenAuth, error) {
	return nil, nil
}

// ListUserCustomerIDs returns the user's tenant-scope grants; a user with no
// entry is unscoped / all-customers.
func (f *fakeUsers) ListUserCustomerIDs(_ context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	return f.grants[id], nil
}

// guardEnv is a small HTTP app: test-only /login and /csrf plus a guarded
// catch-all that answers 200 "ok" for any /api/v1 path.
func guardEnv(t *testing.T) (*httptest.Server, *http.Client, map[string]uuid.UUID) {
	t.Helper()

	disabledAt := time.Now()
	ids := map[string]uuid.UUID{
		"viewer":          uuid.New(),
		"operator":        uuid.New(),
		"admin":           uuid.New(),
		"disabled":        uuid.New(),
		"portal":          uuid.New(), // operator scoped to a single customer
		"grantedCustomer": uuid.New(),
	}
	users := &fakeUsers{
		users: map[uuid.UUID]store.User{
			ids["viewer"]:   {ID: ids["viewer"], Email: "v@example.com", Role: "viewer"},
			ids["operator"]: {ID: ids["operator"], Email: "o@example.com", Role: "operator"},
			ids["admin"]:    {ID: ids["admin"], Email: "a@example.com", Role: "admin"},
			ids["disabled"]: {ID: ids["disabled"], Email: "d@example.com", Role: "admin", DisabledAt: &disabledAt},
			ids["portal"]:   {ID: ids["portal"], Email: "p@example.com", Role: "operator"},
		},
		grants: map[uuid.UUID][]uuid.UUID{
			ids["portal"]: {ids["grantedCustomer"]},
		},
	}

	sessions := auth.NewSessions(false)

	r := chi.NewRouter()
	r.Use(sessions.Manager.LoadAndSave)
	r.Post("/test/login/{id}", func(w http.ResponseWriter, req *http.Request) {
		id, err := uuid.Parse(chi.URLParam(req, "id"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := sessions.Login(req.Context(), id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/test/csrf", func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.WriteString(w, sessions.CSRFToken(req.Context()))
	})
	r.Group(func(g chi.Router) {
		g.Use(Guard(sessions, users, nil, nil))
		ok := func(w http.ResponseWriter, req *http.Request) { _, _ = io.WriteString(w, "ok") }
		// whoami echoes the resolved principal's tenant scope so tests can
		// assert how grants map onto the request principal.
		g.Get("/api/v1/whoami", func(w http.ResponseWriter, req *http.Request) {
			p, _ := auth.PrincipalFrom(req.Context())
			allowed := make([]string, 0, len(p.AllowedCustomers))
			for id := range p.AllowedCustomers {
				allowed = append(allowed, id.String())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"allCustomers": p.AllCustomers,
				"role":         p.User.Role,
				"allowed":      allowed,
			})
		})
		g.HandleFunc("/api/v1/*", ok)
		g.HandleFunc("/api/v1/customers", ok)
	})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	return srv, client, ids
}

func doReq(t *testing.T, client *http.Client, method, url, csrf string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func login(t *testing.T, client *http.Client, srv *httptest.Server, id uuid.UUID) string {
	t.Helper()
	resp := doReq(t, client, http.MethodPost, srv.URL+"/test/login/"+id.String(), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("test login failed: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/test/csrf", nil)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	tok, _ := io.ReadAll(res.Body)
	return string(tok)
}

func TestGuardRequiresSession(t *testing.T) {
	srv, client, _ := guardEnv(t)

	resp := doReq(t, client, http.MethodGet, srv.URL+"/api/v1/customers", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET = %d, want 401", resp.StatusCode)
	}

	// Public paths stay reachable without a session.
	for _, p := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/providers"},
		{http.MethodPost, "/api/v1/auth/dev-login"},
	} {
		resp := doReq(t, client, p.method, srv.URL+p.path, "")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("public %s %s = %d, want 200", p.method, p.path, resp.StatusCode)
		}
	}
}

func TestGuardRejectsUnknownAndDisabledUsers(t *testing.T) {
	srv, client, ids := guardEnv(t)

	login(t, client, srv, ids["disabled"])
	if resp := doReq(t, client, http.MethodGet, srv.URL+"/api/v1/customers", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("disabled user GET = %d, want 401", resp.StatusCode)
	}

	login(t, client, srv, uuid.New()) // session points at a user the store does not know
	if resp := doReq(t, client, http.MethodGet, srv.URL+"/api/v1/customers", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown user GET = %d, want 401", resp.StatusCode)
	}
}

func TestGuardCSRF(t *testing.T) {
	srv, client, ids := guardEnv(t)
	csrf := login(t, client, srv, ids["operator"])

	if resp := doReq(t, client, http.MethodPost, srv.URL+"/api/v1/customers", ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without CSRF = %d, want 403", resp.StatusCode)
	}
	if resp := doReq(t, client, http.MethodPost, srv.URL+"/api/v1/customers", "wrong-token"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST with wrong CSRF = %d, want 403", resp.StatusCode)
	}
	if resp := doReq(t, client, http.MethodPost, srv.URL+"/api/v1/customers", csrf); resp.StatusCode != http.StatusOK {
		t.Errorf("POST with valid CSRF = %d, want 200", resp.StatusCode)
	}
	// GET requests never need the token.
	if resp := doReq(t, client, http.MethodGet, srv.URL+"/api/v1/customers", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("GET without CSRF = %d, want 200", resp.StatusCode)
	}
}

func TestGuardRBACMatrix(t *testing.T) {
	srv, client, ids := guardEnv(t)

	cases := []struct {
		role       string
		method     string
		path       string
		wantStatus int
	}{
		{"viewer", http.MethodGet, "/api/v1/customers", http.StatusOK},
		{"viewer", http.MethodPost, "/api/v1/customers", http.StatusForbidden},
		{"viewer", http.MethodPatch, "/api/v1/customers", http.StatusForbidden},
		{"viewer", http.MethodDelete, "/api/v1/customers", http.StatusForbidden},
		{"viewer", http.MethodPost, "/api/v1/auth/logout", http.StatusOK}, // logout exempt from role check
		{"operator", http.MethodGet, "/api/v1/customers", http.StatusOK},
		{"operator", http.MethodPost, "/api/v1/customers", http.StatusOK},
		{"operator", http.MethodDelete, "/api/v1/customers", http.StatusOK},
		{"admin", http.MethodGet, "/api/v1/customers", http.StatusOK},
		{"admin", http.MethodPost, "/api/v1/customers", http.StatusOK},
		{"admin", http.MethodPatch, "/api/v1/customers", http.StatusOK},
		{"admin", http.MethodDelete, "/api/v1/customers", http.StatusOK},
	}
	for _, c := range cases {
		csrf := login(t, client, srv, ids[c.role])
		resp := doReq(t, client, c.method, srv.URL+c.path, csrf)
		if resp.StatusCode != c.wantStatus {
			t.Errorf("%s %s %s = %d, want %d", c.role, c.method, c.path, resp.StatusCode, c.wantStatus)
		}
	}
}

// TestGuardAdminOnlyPaths: user management, SSO settings and the audit log
// are admin-only for every method, including GET.
func TestGuardAdminOnlyPaths(t *testing.T) {
	srv, client, ids := guardEnv(t)

	cases := []struct {
		role       string
		method     string
		path       string
		wantStatus int
	}{
		{"viewer", http.MethodGet, "/api/v1/users", http.StatusForbidden},
		{"viewer", http.MethodGet, "/api/v1/audit", http.StatusForbidden},
		{"viewer", http.MethodGet, "/api/v1/settings/auth-providers", http.StatusForbidden},
		{"operator", http.MethodGet, "/api/v1/users", http.StatusForbidden},
		{"operator", http.MethodPost, "/api/v1/users", http.StatusForbidden},
		{"operator", http.MethodGet, "/api/v1/audit", http.StatusForbidden},
		{"operator", http.MethodPatch, "/api/v1/settings/auth-providers/xyz", http.StatusForbidden},
		{"operator", http.MethodPost, "/api/v1/settings/auth-providers/xyz/test", http.StatusForbidden},
		{"admin", http.MethodGet, "/api/v1/users", http.StatusOK},
		{"admin", http.MethodPost, "/api/v1/users", http.StatusOK},
		{"admin", http.MethodGet, "/api/v1/audit", http.StatusOK},
		{"admin", http.MethodGet, "/api/v1/settings/auth-providers", http.StatusOK},
		{"admin", http.MethodDelete, "/api/v1/settings/auth-providers/xyz", http.StatusOK},
		// Similar-looking non-admin paths stay operator-accessible.
		{"operator", http.MethodGet, "/api/v1/auditors", http.StatusOK},
	}
	for _, c := range cases {
		csrf := login(t, client, srv, ids[c.role])
		resp := doReq(t, client, c.method, srv.URL+c.path, csrf)
		if resp.StatusCode != c.wantStatus {
			t.Errorf("%s %s %s = %d, want %d", c.role, c.method, c.path, resp.StatusCode, c.wantStatus)
		}
	}
}

// TestGuardScopesPrincipalFromGrants covers the path that powers the tenant
// self-service portal: a non-admin user with per-customer grants is attached
// as a tenant-scoped principal (AllCustomers=false, AllowedCustomers set),
// while admins and non-admins with no grants stay unscoped.
func TestGuardScopesPrincipalFromGrants(t *testing.T) {
	srv, client, ids := guardEnv(t)

	whoami := func(role string) (bool, []string) {
		t.Helper()
		login(t, client, srv, ids[role])
		resp := doReq(t, client, http.MethodGet, srv.URL+"/api/v1/whoami", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s whoami = %d, want 200", role, resp.StatusCode)
		}
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/whoami", nil)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var body struct {
			AllCustomers bool     `json:"allCustomers"`
			Allowed      []string `json:"allowed"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("%s whoami body: %v", role, err)
		}
		return body.AllCustomers, body.Allowed
	}

	// Portal user: operator with a single grant -> scoped to exactly that customer.
	if all, allowed := whoami("portal"); all || len(allowed) != 1 || allowed[0] != ids["grantedCustomer"].String() {
		t.Errorf("portal principal = (all=%v, allowed=%v), want scoped to grantedCustomer", all, allowed)
	}
	// Admin: always unscoped.
	if all, allowed := whoami("admin"); !all || len(allowed) != 0 {
		t.Errorf("admin principal = (all=%v, allowed=%v), want unscoped", all, allowed)
	}
	// Non-admin with no grants: backward-compatible unscoped.
	if all, allowed := whoami("operator"); !all || len(allowed) != 0 {
		t.Errorf("ungranted operator principal = (all=%v, allowed=%v), want unscoped", all, allowed)
	}
}

func TestGuardErrorShape(t *testing.T) {
	srv, _, _ := guardEnv(t)
	resp, err := http.Get(srv.URL + "/api/v1/customers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("401 body is not JSON: %v", err)
	}
	if e.Code != "unauthorized" || e.Message == "" {
		t.Errorf("401 body = %+v, want code=unauthorized with message", e)
	}
}
