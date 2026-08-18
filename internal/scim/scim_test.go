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
	users     map[uuid.UUID]*store.UserWithIdentities
	groups    map[uuid.UUID]*store.SCIMGroup
	customers map[string]uuid.UUID // slug -> id
}

func newFake() *fakeStore {
	return &fakeStore{
		users:     map[uuid.UUID]*store.UserWithIdentities{},
		groups:    map[uuid.UUID]*store.SCIMGroup{},
		customers: map[string]uuid.UUID{},
	}
}

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

var testMapping = store.SCIMMapping{RolePrefix: "role:", CustomerPrefix: "customer:", DefaultRole: "viewer"}

func newServer() *Server { return New(newFake(), adminAuth, testMapping, nil) }

func newServerWithFake() (*Server, *fakeStore) {
	f := newFake()
	return New(f, adminAuth, testMapping, nil), f
}

// --- fake SCIM group + recompute implementation (mirrors the PG semantics) ---

func (f *fakeStore) CreateSCIMGroup(_ context.Context, id uuid.UUID, dn string, ext *string, members []uuid.UUID, _ []audit.Entry) (store.SCIMGroup, error) {
	g := &store.SCIMGroup{ID: id, DisplayName: dn, ExternalID: ext, Members: append([]uuid.UUID{}, members...)}
	f.groups[id] = g
	return *g, nil
}

func (f *fakeStore) GetSCIMGroup(_ context.Context, id uuid.UUID) (store.SCIMGroup, error) {
	if g, ok := f.groups[id]; ok {
		return *g, nil
	}
	return store.SCIMGroup{}, store.ErrNotFound
}

func (f *fakeStore) ListSCIMGroups(context.Context) ([]store.SCIMGroup, error) {
	out := make([]store.SCIMGroup, 0, len(f.groups))
	for _, g := range f.groups {
		out = append(out, *g)
	}
	return out, nil
}

func (f *fakeStore) UpdateSCIMGroup(_ context.Context, id uuid.UUID, dn, ext *string, members *[]uuid.UUID, _ []audit.Entry) (store.SCIMGroup, []uuid.UUID, error) {
	g, ok := f.groups[id]
	if !ok {
		return store.SCIMGroup{}, nil, store.ErrNotFound
	}
	affected := map[uuid.UUID]bool{}
	nameChanged := dn != nil && *dn != g.DisplayName
	if dn != nil {
		g.DisplayName = *dn
	}
	if ext != nil {
		g.ExternalID = ext
	}
	if members != nil {
		for _, u := range g.Members {
			affected[u] = true
		}
		g.Members = append([]uuid.UUID{}, (*members)...)
		for _, u := range g.Members {
			affected[u] = true
		}
	} else if nameChanged {
		for _, u := range g.Members {
			affected[u] = true
		}
	}
	return *g, keysOf(affected), nil
}

func (f *fakeStore) ModifySCIMGroupMembers(_ context.Context, id uuid.UUID, add, remove []uuid.UUID, _ []audit.Entry) (store.SCIMGroup, []uuid.UUID, error) {
	g, ok := f.groups[id]
	if !ok {
		return store.SCIMGroup{}, nil, store.ErrNotFound
	}
	has := map[uuid.UUID]bool{}
	for _, u := range g.Members {
		has[u] = true
	}
	for _, u := range add {
		has[u] = true
	}
	for _, u := range remove {
		delete(has, u)
	}
	g.Members = keysOf(has)
	affected := map[uuid.UUID]bool{}
	for _, u := range add {
		affected[u] = true
	}
	for _, u := range remove {
		affected[u] = true
	}
	return *g, keysOf(affected), nil
}

func (f *fakeStore) DeleteSCIMGroup(_ context.Context, id uuid.UUID, _ []audit.Entry) ([]uuid.UUID, error) {
	g, ok := f.groups[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	former := append([]uuid.UUID{}, g.Members...)
	delete(f.groups, id)
	return former, nil
}

func (f *fakeStore) RecomputeSCIMUserAccess(_ context.Context, userID uuid.UUID, m store.SCIMMapping, _ *uuid.UUID, _ []audit.Entry) error {
	u, ok := f.users[userID]
	if !ok {
		return store.ErrNotFound
	}
	bestRole, best := "", 0
	rank := map[string]int{"viewer": 1, "operator": 2, "admin": 3}
	hasMapped := false
	var grants []uuid.UUID
	for _, g := range f.groups {
		member := false
		for _, mem := range g.Members {
			if mem == userID {
				member = true
				break
			}
		}
		if !member {
			continue
		}
		if strings.HasPrefix(g.DisplayName, m.RolePrefix) {
			hasMapped = true
			role := strings.TrimPrefix(g.DisplayName, m.RolePrefix)
			if rank[role] > best {
				best, bestRole = rank[role], role
			}
		} else if strings.HasPrefix(g.DisplayName, m.CustomerPrefix) {
			hasMapped = true
			if cid, ok := f.customers[strings.TrimPrefix(g.DisplayName, m.CustomerPrefix)]; ok {
				grants = append(grants, cid)
			}
		}
	}
	if !u.ScimManaged && !hasMapped {
		return nil
	}
	if bestRole == "" {
		bestRole = m.DefaultRole
	}
	u.Role = bestRole
	u.CustomerIDs = grants
	u.ScimManaged = true
	return nil
}

func keysOf(set map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}

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
	srv := New(newFake(), func(context.Context, string) (string, bool) { return "", false }, testMapping, nil)
	req := httptest.NewRequest(http.MethodGet, "/Users", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", rec.Code)
	}

	// Non-admin token → 403.
	srv = New(newFake(), func(context.Context, string) (string, bool) { return "operator", true }, testMapping, nil)
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

func createSCIMUser(t *testing.T, srv *Server, email string) string {
	t.Helper()
	rec := do(t, srv, http.MethodPost, "/Users", `{"userName":"`+email+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user %s: %d %s", email, rec.Code, rec.Body)
	}
	var u userResource
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	return u.ID
}

func createSCIMGroup(t *testing.T, srv *Server, displayName string, memberIDs ...string) string {
	t.Helper()
	members := make([]string, 0, len(memberIDs))
	for _, id := range memberIDs {
		members = append(members, `{"value":"`+id+`"}`)
	}
	body := `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],"displayName":"` + displayName +
		`","members":[` + strings.Join(members, ",") + `]}`
	rec := do(t, srv, http.MethodPost, "/Groups", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group %s: %d %s", displayName, rec.Code, rec.Body)
	}
	var g groupResource
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	return g.ID
}

func TestSCIMGroupsMapRoleAndTenant(t *testing.T) {
	srv, f := newServerWithFake()
	acme := uuid.New()
	f.customers["acme"] = acme

	aliceID := createSCIMUser(t, srv, "alice@example.com")
	alice := uuid.MustParse(aliceID)

	// role:operator + customer:acme → operator scoped to ACME.
	roleGroup := createSCIMGroup(t, srv, "role:operator", aliceID)
	custGroup := createSCIMGroup(t, srv, "customer:acme", aliceID)

	if u := f.users[alice]; u.Role != "operator" || !u.ScimManaged || len(u.CustomerIDs) != 1 || u.CustomerIDs[0] != acme {
		t.Fatalf("after mapping: role=%q managed=%v grants=%v", u.Role, u.ScimManaged, u.CustomerIDs)
	}

	// Remove alice from customer:acme (SCIM remove-filter form). She stays in
	// role:operator → still managed, still operator, but now NO customer access.
	rec := do(t, srv, http.MethodPatch, "/Groups/"+custGroup,
		`{"Operations":[{"op":"remove","path":"members[value eq \"`+aliceID+`\"]"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch remove member: %d %s", rec.Code, rec.Body)
	}
	if u := f.users[alice]; u.Role != "operator" || !u.ScimManaged || len(u.CustomerIDs) != 0 {
		t.Fatalf("after removing customer group: role=%q managed=%v grants=%v", u.Role, u.ScimManaged, u.CustomerIDs)
	}

	// Delete the role group → alice is in no mapped group but stays managed
	// (sticky): role falls back to the default, grants empty (no access).
	if rec := do(t, srv, http.MethodDelete, "/Groups/"+roleGroup, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete group: %d %s", rec.Code, rec.Body)
	}
	if u := f.users[alice]; u.Role != "viewer" || !u.ScimManaged || len(u.CustomerIDs) != 0 {
		t.Fatalf("after deleting role group: role=%q managed=%v grants=%v", u.Role, u.ScimManaged, u.CustomerIDs)
	}
}

func TestSCIMGroupsAddPatchAndList(t *testing.T) {
	srv, f := newServerWithFake()
	f.customers["acme"] = uuid.New()
	bobID := createSCIMUser(t, srv, "bob@example.com")
	bob := uuid.MustParse(bobID)

	gid := createSCIMGroup(t, srv, "role:admin") // no members yet

	// PATCH add member (Okta form: op add, path members, value list).
	rec := do(t, srv, http.MethodPatch, "/Groups/"+gid,
		`{"Operations":[{"op":"add","path":"members","value":[{"value":"`+bobID+`"}]}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch add member: %d %s", rec.Code, rec.Body)
	}
	if u := f.users[bob]; u.Role != "admin" || !u.ScimManaged {
		t.Fatalf("bob should be admin via group: role=%q managed=%v", u.Role, u.ScimManaged)
	}

	// GET the group reflects membership.
	rec = do(t, srv, http.MethodGet, "/Groups/"+gid, "")
	var g groupResource
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	if len(g.Members) != 1 || g.Members[0].Value != bobID {
		t.Fatalf("group members = %+v", g.Members)
	}

	// List + displayName filter.
	rec = do(t, srv, http.MethodGet, "/Groups?filter="+url.QueryEscape(`displayName eq "role:admin"`), "")
	var list struct {
		TotalResults int             `json:"totalResults"`
		Resources    []groupResource `json:"Resources"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if list.TotalResults != 1 || len(list.Resources) != 1 {
		t.Fatalf("group filter list = %+v", list)
	}

	// Unknown group → 404.
	if rec := do(t, srv, http.MethodGet, "/Groups/"+uuid.New().String(), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown group status = %d", rec.Code)
	}
}
