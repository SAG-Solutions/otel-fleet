package scim

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

type fakeStore struct {
	users map[uuid.UUID]*store.UserWithIdentities
}

func newFake() *fakeStore { return &fakeStore{users: map[uuid.UUID]*store.UserWithIdentities{}} }

func (f *fakeStore) ListUsers(context.Context) ([]store.UserWithIdentities, error) {
	out := make([]store.UserWithIdentities, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, *u)
	}
	return out, nil
}

func (f *fakeStore) GetUserWithIdentities(_ context.Context, id uuid.UUID) (store.UserWithIdentities, error) {
	if u, ok := f.users[id]; ok {
		return *u, nil
	}
	return store.UserWithIdentities{}, store.ErrNotFound
}

func (f *fakeStore) GetUserByEmail(_ context.Context, email string) (store.UserWithIdentities, error) {
	for _, u := range f.users {
		if strings.EqualFold(u.Email, email) {
			return *u, nil
		}
	}
	return store.UserWithIdentities{}, store.ErrNotFound
}

func (f *fakeStore) CreateSCIMUser(_ context.Context, id uuid.UUID, email, role string, displayName, externalID *string, _ []audit.Entry) (store.UserWithIdentities, error) {
	for _, u := range f.users {
		if strings.EqualFold(u.Email, email) {
			return store.UserWithIdentities{}, store.ErrEmailExists
		}
	}
	u := &store.UserWithIdentities{User: store.User{ID: id, Email: email, Role: role, DisplayName: displayName, ExternalID: externalID}}
	f.users[id] = u
	return *u, nil
}

func (f *fakeStore) UpdateSCIMUser(_ context.Context, id uuid.UUID, displayName, externalID *string, _ []audit.Entry) (store.UserWithIdentities, error) {
	u, ok := f.users[id]
	if !ok {
		return store.UserWithIdentities{}, store.ErrNotFound
	}
	u.DisplayName = displayName
	u.ExternalID = externalID
	return *u, nil
}

func (f *fakeStore) UpdateUserAdmin(_ context.Context, id uuid.UUID, upd store.UserUpdate, _ []audit.Entry) (store.UserWithIdentities, error) {
	u, ok := f.users[id]
	if !ok {
		return store.UserWithIdentities{}, store.ErrNotFound
	}
	if upd.Disabled != nil {
		if *upd.Disabled {
			now := time.Now()
			u.DisabledAt = &now
		} else {
			u.DisabledAt = nil
		}
	}
	return *u, nil
}

func adminAuth(context.Context, string) (string, bool) { return "admin", true }

func newServer() *Server { return New(newFake(), adminAuth, "viewer", nil) }

func do(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewBufferString(body)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer otm_pat_x")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

func TestSCIMCreateGetListDeactivate(t *testing.T) {
	srv := newServer()

	// Create
	rec := do(t, srv, http.MethodPost, "/Users",
		`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"alice@example.com","displayName":"Alice","externalId":"idp-1","active":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", rec.Code, rec.Body)
	}
	var created userResource
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.UserName != "alice@example.com" || created.ExternalID != "idp-1" || !created.Active {
		t.Fatalf("unexpected created resource: %+v", created)
	}
	if _, err := uuid.Parse(created.ID); err != nil {
		t.Fatalf("id is not a uuid: %s", created.ID)
	}

	// Duplicate create → 409
	if rec := do(t, srv, http.MethodPost, "/Users", `{"userName":"alice@example.com"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create status = %d", rec.Code)
	}

	// Get
	if rec := do(t, srv, http.MethodGet, "/Users/"+created.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}

	// List with userName filter
	rec = do(t, srv, http.MethodGet, "/Users?filter="+url.QueryEscape(`userName eq "alice@example.com"`), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list struct {
		TotalResults int            `json:"totalResults"`
		Resources    []userResource `json:"Resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.TotalResults != 1 || len(list.Resources) != 1 {
		t.Fatalf("filter list = %+v", list)
	}

	// Filter miss → empty
	rec = do(t, srv, http.MethodGet, "/Users?filter="+url.QueryEscape(`userName eq "nobody@example.com"`), "")
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if list.TotalResults != 0 {
		t.Fatalf("filter miss should be empty, got %d", list.TotalResults)
	}

	// PATCH active=false (deprovision)
	rec = do(t, srv, http.MethodPatch, "/Users/"+created.ID,
		`{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":false}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%s", rec.Code, rec.Body)
	}
	var patched userResource
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.Active {
		t.Fatal("user should be inactive after PATCH active=false")
	}

	// DELETE → 204 (deactivate)
	if rec := do(t, srv, http.MethodDelete, "/Users/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
}

func TestSCIMPatchPathlessAndDisplayName(t *testing.T) {
	srv := newServer()
	rec := do(t, srv, http.MethodPost, "/Users", `{"userName":"bob@example.com","displayName":"Bob"}`)
	var u userResource
	_ = json.Unmarshal(rec.Body.Bytes(), &u)

	// Pathless replace with a partial object.
	rec = do(t, srv, http.MethodPatch, "/Users/"+u.ID,
		`{"Operations":[{"op":"replace","value":{"active":false,"displayName":"Bobby"}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", rec.Code, rec.Body)
	}
	var patched userResource
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.Active {
		t.Error("active should be false")
	}
	if patched.DisplayName != "Bobby" {
		t.Errorf("displayName = %q, want Bobby", patched.DisplayName)
	}
}

func TestSCIMAuth(t *testing.T) {
	// Missing/invalid token → 401.
	srv := New(newFake(), func(context.Context, string) (string, bool) { return "", false }, "viewer", nil)
	req := httptest.NewRequest(http.MethodGet, "/Users", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", rec.Code)
	}

	// Non-admin token → 403.
	srv = New(newFake(), func(context.Context, string) (string, bool) { return "operator", true }, "viewer", nil)
	req = httptest.NewRequest(http.MethodGet, "/Users", nil)
	req.Header.Set("Authorization", "Bearer otm_pat_op")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator token status = %d, want 403", rec.Code)
	}
}

func TestSCIMDiscovery(t *testing.T) {
	srv := newServer()
	for _, path := range []string{"/ServiceProviderConfig", "/ResourceTypes", "/Schemas"} {
		if rec := do(t, srv, http.MethodGet, path, ""); rec.Code != http.StatusOK {
			t.Errorf("%s status = %d", path, rec.Code)
		}
	}
}
