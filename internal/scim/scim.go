// Package scim implements a minimal SCIM 2.0 (RFC 7643/7644) Users endpoint so
// identity providers (Okta, Entra ID, …) can provision, update and deprovision
// otel-fleet console users. It maps a SCIM User onto the users table: userName
// = email, active = enabled, displayName/externalId are stored. Roles and
// tenant-scope grants are NOT set by SCIM — provisioned users get the
// configured default role (least privilege) and an admin adjusts them in the
// UI. Deprovisioning (DELETE or active=false) disables the account (and its
// sessions) rather than hard-deleting, preserving audit history.
//
// The endpoint is mounted at /scim/v2 and authenticated with an admin
// management-API token (Authorization: Bearer otm_pat_…).
package scim

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/authz"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

const (
	schemaUser         = "urn:ietf:params:scim:schemas:core:2.0:User"
	schemaListResponse = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	schemaError        = "urn:ietf:params:scim:api:messages:2.0:Error"
	schemaPatchOp      = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	contentType        = "application/scim+json"
)

// Store is the persistence subset SCIM needs.
type Store interface {
	ListUsers(ctx context.Context) ([]store.UserWithIdentities, error)
	GetUserWithIdentities(ctx context.Context, id uuid.UUID) (store.UserWithIdentities, error)
	GetUserByEmail(ctx context.Context, email string) (store.UserWithIdentities, error)
	CreateSCIMUser(ctx context.Context, id uuid.UUID, email, role string, displayName, externalID *string, entries []audit.Entry) (store.UserWithIdentities, error) //nolint:lll
	UpdateSCIMUser(ctx context.Context, id uuid.UUID, displayName, externalID *string, entries []audit.Entry) (store.UserWithIdentities, error)
	UpdateUserAdmin(ctx context.Context, id uuid.UUID, upd store.UserUpdate, entries []audit.Entry) (store.UserWithIdentities, error)
}

// AuthFunc validates the request Authorization header and reports the caller's
// role; ok=false rejects the request. SCIM requires an admin token.
type AuthFunc func(ctx context.Context, authorization string) (role string, ok bool)

// Server serves the SCIM 2.0 Users resource.
type Server struct {
	store       Store
	auth        AuthFunc
	defaultRole string
	log         Logger
}

// Logger is the minimal logging surface (satisfied by *slog.Logger).
type Logger interface {
	Error(msg string, args ...any)
}

// New builds a SCIM server. defaultRole is the role assigned to provisioned
// users (falls back to viewer if unknown).
func New(st Store, auth AuthFunc, defaultRole string, log Logger) *Server {
	if !authz.Known(defaultRole) {
		defaultRole = authz.RoleViewer
	}
	return &Server{store: st, auth: auth, defaultRole: defaultRole, log: log}
}

// Router returns the /scim/v2 sub-router (mount it with the base path stripped).
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(s.authMiddleware)
	r.Get("/ServiceProviderConfig", s.serviceProviderConfig)
	r.Get("/ResourceTypes", s.resourceTypes)
	r.Get("/Schemas", s.schemas)
	r.Get("/Users", s.listUsers)
	r.Post("/Users", s.createUser)
	r.Get("/Users/{id}", s.getUser)
	r.Put("/Users/{id}", s.putUser)
	r.Patch("/Users/{id}", s.patchUser)
	r.Delete("/Users/{id}", s.deleteUser)
	return r
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := s.auth(r.Context(), r.Header.Get("Authorization"))
		if !ok {
			s.writeError(w, http.StatusUnauthorized, "invalid or missing token")
			return
		}
		if !authz.AtLeast(role, authz.RoleAdmin) {
			s.writeError(w, http.StatusForbidden, "SCIM requires an admin token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- SCIM resource shapes ---

type email struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary,omitempty"`
}

type name struct {
	Formatted string `json:"formatted,omitempty"`
}

type meta struct {
	ResourceType string `json:"resourceType"`
	Created      string `json:"created,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	Location     string `json:"location"`
}

type userResource struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id"`
	ExternalID  string   `json:"externalId,omitempty"`
	UserName    string   `json:"userName"`
	Name        *name    `json:"name,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Emails      []email  `json:"emails,omitempty"`
	Active      bool     `json:"active"`
	Meta        meta     `json:"meta"`
}

func toResource(u store.UserWithIdentities) userResource {
	res := userResource{
		Schemas:  []string{schemaUser},
		ID:       u.ID.String(),
		UserName: u.Email,
		Emails:   []email{{Value: u.Email, Primary: true}},
		Active:   u.DisabledAt == nil,
		Meta:     meta{ResourceType: "User", Location: "/scim/v2/Users/" + u.ID.String(), Created: u.CreatedAt.UTC().Format(time.RFC3339)},
	}
	if u.ExternalID != nil {
		res.ExternalID = *u.ExternalID
	}
	if u.DisplayName != nil && *u.DisplayName != "" {
		res.DisplayName = *u.DisplayName
		res.Name = &name{Formatted: *u.DisplayName}
	}
	return res
}

// userPayload is the incoming SCIM User (create / replace).
type userPayload struct {
	UserName    string  `json:"userName"`
	ExternalID  string  `json:"externalId"`
	DisplayName string  `json:"displayName"`
	Name        *name   `json:"name"`
	Emails      []email `json:"emails"`
	Active      *bool   `json:"active"`
}

// primaryEmail resolves the account email: userName, else the primary/first
// email value.
func (p userPayload) primaryEmail() string {
	if e := strings.TrimSpace(p.UserName); e != "" {
		return e
	}
	for _, em := range p.Emails {
		if em.Primary && em.Value != "" {
			return em.Value
		}
	}
	if len(p.Emails) > 0 {
		return p.Emails[0].Value
	}
	return ""
}

func (p userPayload) display() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	if p.Name != nil {
		return p.Name.Formatted
	}
	return ""
}

// --- handlers ---

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var p userPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid SCIM payload")
		return
	}
	e := strings.ToLower(strings.TrimSpace(p.primaryEmail()))
	if e == "" || !strings.Contains(e, "@") {
		s.writeError(w, http.StatusBadRequest, "userName (email) is required")
		return
	}

	var displayName, externalID *string
	if d := p.display(); d != "" {
		displayName = &d
	}
	if p.ExternalID != "" {
		externalID = &p.ExternalID
	}

	u, err := s.store.CreateSCIMUser(r.Context(), uuid.New(), e, s.defaultRole, displayName, externalID, []audit.Entry{{
		Action: "scim.user.create", EntityType: "user",
		Payload: map[string]any{"email": e, "role": s.defaultRole, "via": "scim"},
	}})
	switch {
	case errors.Is(err, store.ErrEmailExists), errors.Is(err, store.ErrConflict):
		s.writeError(w, http.StatusConflict, "a user with this userName or externalId already exists")
		return
	case err != nil:
		s.internalError(w, "scim create user", err)
		return
	}
	// Fix up the audit entity id now that the row exists (best-effort; the row
	// is the source of truth regardless).
	s.writeResource(w, http.StatusCreated, toResource(u))
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	// IdPs reconcile with `filter=userName eq "x"` before creating; resolve it
	// with a direct lookup rather than scanning all users.
	if f := parseUserNameFilter(r.URL.Query().Get("filter")); f != "" {
		var resources []userResource
		u, err := s.store.GetUserByEmail(r.Context(), f)
		switch {
		case err == nil:
			resources = []userResource{toResource(u)}
		case errors.Is(err, store.ErrNotFound):
			resources = []userResource{}
		default:
			s.internalError(w, "scim filter users", err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{
			"schemas":      []string{schemaListResponse},
			"totalResults": len(resources),
			"startIndex":   1,
			"itemsPerPage": len(resources),
			"Resources":    resources,
		})
		return
	}

	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, "scim list users", err)
		return
	}

	start := atoiDefault(r.URL.Query().Get("startIndex"), 1) // 1-based
	if start < 1 {
		start = 1
	}
	count := atoiDefault(r.URL.Query().Get("count"), len(users))
	if count < 0 {
		count = 0
	}
	total := len(users)
	lo := start - 1
	if lo > total {
		lo = total
	}
	hi := lo + count
	if hi > total {
		hi = total
	}
	page := users[lo:hi]

	resources := make([]userResource, 0, len(page))
	for _, u := range page {
		resources = append(resources, toResource(u))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"schemas":      []string{schemaListResponse},
		"totalResults": total,
		"startIndex":   start,
		"itemsPerPage": len(resources),
		"Resources":    resources,
	})
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.load(w, r)
	if !ok {
		return
	}
	s.writeResource(w, http.StatusOK, toResource(u))
}

func (s *Server) putUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.load(w, r)
	if !ok {
		return
	}
	var p userPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid SCIM payload")
		return
	}
	// userName (email) is the account identity and is not changed via SCIM;
	// displayName, externalId and active are.
	var displayName, externalID *string
	if d := p.display(); d != "" {
		displayName = &d
	}
	if p.ExternalID != "" {
		externalID = &p.ExternalID
	}
	updated, err := s.store.UpdateSCIMUser(r.Context(), u.ID, displayName, externalID, []audit.Entry{{
		Action: "scim.user.update", EntityType: "user", EntityID: u.ID.String(),
	}})
	if err != nil {
		s.internalError(w, "scim put user", err)
		return
	}
	if p.Active != nil {
		updated, ok = s.setActive(w, u.ID, *p.Active)
		if !ok {
			return
		}
	}
	s.writeResource(w, http.StatusOK, toResource(updated))
}

// patchOp is one RFC 7644 PatchOp operation. value is free-form so we can read
// either a scalar (path-addressed) or an object (pathless replace).
type patchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

func (s *Server) patchUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.load(w, r)
	if !ok {
		return
	}
	var body struct {
		Operations []patchOp `json:"Operations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid SCIM PatchOp")
		return
	}

	var (
		setActive    *bool
		setDisplay   *string
		displayDirty bool
	)
	for _, op := range body.Operations {
		if !strings.EqualFold(op.Op, "replace") && !strings.EqualFold(op.Op, "add") {
			continue // remove/unsupported paths are ignored
		}
		path := strings.ToLower(strings.TrimPrefix(op.Path, "urn:ietf:params:scim:schemas:core:2.0:User:"))
		switch path {
		case "active":
			if b, err := parseBool(op.Value); err == nil {
				setActive = &b
			}
		case "displayname":
			var str string
			if json.Unmarshal(op.Value, &str) == nil {
				setDisplay, displayDirty = &str, true
			}
		case "": // pathless replace: value is a partial User object
			var obj struct {
				Active      *bool   `json:"active"`
				DisplayName *string `json:"displayName"`
			}
			if json.Unmarshal(op.Value, &obj) == nil {
				if obj.Active != nil {
					setActive = obj.Active
				}
				if obj.DisplayName != nil {
					setDisplay, displayDirty = obj.DisplayName, true
				}
			}
		}
	}

	updated := u
	if displayDirty {
		var err error
		updated, err = s.store.UpdateSCIMUser(r.Context(), u.ID, setDisplay, u.ExternalID, []audit.Entry{{
			Action: "scim.user.update", EntityType: "user", EntityID: u.ID.String(),
		}})
		if err != nil {
			s.internalError(w, "scim patch user", err)
			return
		}
	}
	if setActive != nil {
		var ok2 bool
		updated, ok2 = s.setActive(w, u.ID, *setActive)
		if !ok2 {
			return
		}
	}
	s.writeResource(w, http.StatusOK, toResource(updated))
}

// deleteUser deprovisions by disabling the account (and its sessions) rather
// than hard-deleting, preserving audit history and honoring the last-admin
// guard.
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.load(w, r)
	if !ok {
		return
	}
	if _, ok := s.setActive(w, u.ID, false); !ok {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setActive toggles the disabled flag via the admin path (last-admin guarded)
// and writes the response error itself on failure, returning ok=false.
func (s *Server) setActive(w http.ResponseWriter, id uuid.UUID, active bool) (store.UserWithIdentities, bool) {
	disabled := !active
	action := "scim.user.deactivate"
	if active {
		action = "scim.user.activate"
	}
	u, err := s.store.UpdateUserAdmin(context.Background(), id, store.UserUpdate{Disabled: &disabled}, []audit.Entry{{
		Action: action, EntityType: "user", EntityID: id.String(),
	}})
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "user not found")
		return store.UserWithIdentities{}, false
	case errors.Is(err, store.ErrLastAdmin):
		s.writeError(w, http.StatusConflict, "cannot deactivate the last enabled admin")
		return store.UserWithIdentities{}, false
	case err != nil:
		s.internalError(w, "scim set active", err)
		return store.UserWithIdentities{}, false
	}
	return u, true
}

// load resolves the {id} path parameter to a user, writing a 404 when absent.
func (s *Server) load(w http.ResponseWriter, r *http.Request) (store.UserWithIdentities, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, "user not found")
		return store.UserWithIdentities{}, false
	}
	u, err := s.store.GetUserWithIdentities(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "user not found")
		return store.UserWithIdentities{}, false
	}
	if err != nil {
		s.internalError(w, "scim load user", err)
		return store.UserWithIdentities{}, false
	}
	return u, true
}

// --- discovery endpoints ---

func (s *Server) serviceProviderConfig(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]any{
		"schemas":               []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"patch":                 map[string]any{"supported": true},
		"bulk":                  map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":                map[string]any{"supported": true, "maxResults": 200},
		"changePassword":        map[string]any{"supported": false},
		"sort":                  map[string]any{"supported": false},
		"etag":                  map[string]any{"supported": false},
		"authenticationSchemes": []map[string]any{{"type": "oauthbearertoken", "name": "Bearer Token", "description": "Admin management-API token (otm_pat_)"}},
	})
}

func (s *Server) resourceTypes(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, []map[string]any{{
		"schemas":  []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"},
		"id":       "User",
		"name":     "User",
		"endpoint": "/Users",
		"schema":   schemaUser,
		"meta":     map[string]any{"resourceType": "ResourceType", "location": "/scim/v2/ResourceTypes/User"},
	}})
}

func (s *Server) schemas(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, []map[string]any{{
		"id":   schemaUser,
		"name": "User",
		"attributes": []map[string]any{
			{"name": "userName", "type": "string", "required": true, "uniqueness": "server"},
			{"name": "displayName", "type": "string"},
			{"name": "active", "type": "boolean"},
			{"name": "emails", "type": "complex", "multiValued": true},
		},
		"meta": map[string]any{"resourceType": "Schema", "location": "/scim/v2/Schemas/" + schemaUser},
	}})
}

// --- helpers ---

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) writeResource(w http.ResponseWriter, status int, res userResource) {
	s.writeJSON(w, status, res)
}

func (s *Server) writeError(w http.ResponseWriter, status int, detail string) {
	s.writeJSON(w, status, map[string]any{
		"schemas": []string{schemaError},
		"detail":  detail,
		"status":  strconv.Itoa(status),
	})
}

func (s *Server) internalError(w http.ResponseWriter, ctx string, err error) {
	if s.log != nil {
		s.log.Error("scim: "+ctx+" failed", "err", err)
	}
	s.writeError(w, http.StatusInternalServerError, "internal error")
}

// parseUserNameFilter extracts x from `userName eq "x"` (the only filter IdPs
// need for reconciliation); returns "" for anything else.
func parseUserNameFilter(filter string) string {
	f := strings.TrimSpace(filter)
	lower := strings.ToLower(f)
	if !strings.HasPrefix(lower, "username eq ") {
		return ""
	}
	rest := strings.TrimSpace(f[len("userName eq "):])
	return strings.Trim(rest, `"`)
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseBool(raw json.RawMessage) (bool, error) {
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b, nil
	}
	// Some IdPs send booleans as strings ("true"/"false").
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return strconv.ParseBool(str)
	}
	return false, errors.New("not a boolean")
}
