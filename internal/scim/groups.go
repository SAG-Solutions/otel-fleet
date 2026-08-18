package scim

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/store"
)

// --- SCIM Group resource shapes ---

type groupMember struct {
	Value string `json:"value"`          // user id
	Ref   string `json:"$ref,omitempty"` // ignored on input
	Type  string `json:"type,omitempty"`
}

type groupResource struct {
	Schemas     []string      `json:"schemas"`
	ID          string        `json:"id"`
	ExternalID  string        `json:"externalId,omitempty"`
	DisplayName string        `json:"displayName"`
	Members     []groupMember `json:"members"`
	Meta        meta          `json:"meta"`
}

type groupPayload struct {
	DisplayName string        `json:"displayName"`
	ExternalID  *string       `json:"externalId"`
	Members     []groupMember `json:"members"`
}

func (s *Server) toGroupResource(g store.SCIMGroup) groupResource {
	members := make([]groupMember, 0, len(g.Members))
	for _, m := range g.Members {
		members = append(members, groupMember{Value: m.String(), Type: "User"})
	}
	res := groupResource{
		Schemas:     []string{schemaGroup},
		ID:          g.ID.String(),
		DisplayName: g.DisplayName,
		Members:     members,
		Meta: meta{
			ResourceType: "Group",
			Created:      g.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			LastModified: g.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			Location:     "/scim/v2/Groups/" + g.ID.String(),
		},
	}
	if g.ExternalID != nil {
		res.ExternalID = *g.ExternalID
	}
	return res
}

// memberIDs parses SCIM member entries into user UUIDs, skipping malformed ones.
func memberIDs(members []groupMember) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(members))
	for _, m := range members {
		if id, err := uuid.Parse(strings.TrimSpace(m.Value)); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// --- handlers ---

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListSCIMGroups(r.Context())
	if err != nil {
		s.internalError(w, "scim list groups", err)
		return
	}
	// displayName eq "x" filter (the only one IdPs use to reconcile groups).
	filter := parseDisplayNameFilter(r.URL.Query().Get("filter"))
	resources := make([]groupResource, 0, len(groups))
	for _, g := range groups {
		if filter != "" && g.DisplayName != filter {
			continue
		}
		resources = append(resources, s.toGroupResource(g))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"schemas":      []string{schemaListResponse},
		"totalResults": len(resources),
		"startIndex":   1,
		"itemsPerPage": len(resources),
		"Resources":    resources,
	})
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	var p groupPayload
	if !s.decode(w, r, &p) {
		return
	}
	if strings.TrimSpace(p.DisplayName) == "" {
		s.writeError(w, http.StatusBadRequest, "displayName is required")
		return
	}
	id := uuid.New()
	g, err := s.store.CreateSCIMGroup(r.Context(), id, p.DisplayName, p.ExternalID, memberIDs(p.Members), []audit.Entry{{
		Action: "scim.group.create", EntityType: "scim_group", EntityID: id.String(),
		Payload: map[string]any{"displayName": p.DisplayName, "members": len(p.Members)},
	}})
	if errors.Is(err, store.ErrConflict) {
		s.writeError(w, http.StatusConflict, "a group with this externalId already exists")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusBadRequest, "one or more members are not known users")
		return
	}
	if err != nil {
		s.internalError(w, "scim create group", err)
		return
	}
	s.recompute(r, g.Members)
	s.writeJSON(w, http.StatusCreated, s.toGroupResource(g))
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) {
	g, ok := s.loadGroup(w, r)
	if !ok {
		return
	}
	s.writeJSON(w, http.StatusOK, s.toGroupResource(g))
}

func (s *Server) putGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.groupID(w, r)
	if !ok {
		return
	}
	var p groupPayload
	if !s.decode(w, r, &p) {
		return
	}
	members := memberIDs(p.Members)
	dn := p.DisplayName
	g, affected, err := s.store.UpdateSCIMGroup(r.Context(), id, &dn, p.ExternalID, &members, []audit.Entry{{
		Action: "scim.group.update", EntityType: "scim_group", EntityID: id.String(),
		Payload: map[string]any{"displayName": p.DisplayName, "members": len(members)},
	}})
	if !s.handleGroupWriteErr(w, err) {
		return
	}
	s.recompute(r, affected)
	s.writeJSON(w, http.StatusOK, s.toGroupResource(g))
}

var memberFilterRe = regexp.MustCompile(`(?i)members\[\s*value\s+eq\s+"([^"]+)"\s*\]`)

type groupPatchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

// patchGroup applies a SCIM PATCH: displayName replace and member add/remove/
// replace (both the `members` + value list form and the
// `members[value eq "id"]` remove-filter form used by Okta/Entra).
func (s *Server) patchGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.groupID(w, r)
	if !ok {
		return
	}
	var body struct {
		Operations []groupPatchOp `json:"Operations"`
	}
	if !s.decode(w, r, &body) {
		return
	}

	var newDisplayName *string
	var replaceMembers *[]uuid.UUID
	var add, remove []uuid.UUID

	for _, op := range body.Operations {
		verb := strings.ToLower(strings.TrimSpace(op.Op))
		path := strings.TrimSpace(op.Path)
		lpath := strings.ToLower(path)

		switch {
		case lpath == "displayname":
			var v string
			if json.Unmarshal(op.Value, &v) == nil {
				dn := v
				newDisplayName = &dn
			}
		case verb == "remove" && memberFilterRe.MatchString(path):
			if m := memberFilterRe.FindStringSubmatch(path); m != nil {
				if uid, err := uuid.Parse(m[1]); err == nil {
					remove = append(remove, uid)
				}
			}
		case lpath == "members":
			ids := memberIDs(decodeMembers(op.Value))
			switch verb {
			case "add":
				add = append(add, ids...)
			case "remove":
				remove = append(remove, ids...)
			case "replace":
				cp := ids
				replaceMembers = &cp
			}
		case path == "":
			// Pathless op: value is a partial group object.
			var obj struct {
				DisplayName *string       `json:"displayName"`
				Members     []groupMember `json:"members"`
			}
			if json.Unmarshal(op.Value, &obj) == nil {
				if obj.DisplayName != nil {
					newDisplayName = obj.DisplayName
				}
				if obj.Members != nil {
					cp := memberIDs(obj.Members)
					replaceMembers = &cp
				}
			}
		}
	}

	ctx := r.Context()
	affected := map[uuid.UUID]bool{}
	var g store.SCIMGroup
	var err error

	if replaceMembers != nil {
		var aff []uuid.UUID
		g, aff, err = s.store.UpdateSCIMGroup(ctx, id, newDisplayName, nil, replaceMembers, s.groupAudit(id, "scim.group.patch"))
		if !s.handleGroupWriteErr(w, err) {
			return
		}
		for _, u := range aff {
			affected[u] = true
		}
	} else {
		if newDisplayName != nil {
			var aff []uuid.UUID
			g, aff, err = s.store.UpdateSCIMGroup(ctx, id, newDisplayName, nil, nil, s.groupAudit(id, "scim.group.patch"))
			if !s.handleGroupWriteErr(w, err) {
				return
			}
			for _, u := range aff {
				affected[u] = true
			}
		}
		if len(add) > 0 || len(remove) > 0 {
			var aff []uuid.UUID
			g, aff, err = s.store.ModifySCIMGroupMembers(ctx, id, add, remove, s.groupAudit(id, "scim.group.patch"))
			if !s.handleGroupWriteErr(w, err) {
				return
			}
			for _, u := range aff {
				affected[u] = true
			}
		}
	}

	// If no mutating op matched, still return the current group.
	if g.ID == uuid.Nil {
		g, ok = s.loadGroup(w, r)
		if !ok {
			return
		}
	}
	s.recompute(r, setKeys(affected))
	s.writeJSON(w, http.StatusOK, s.toGroupResource(g))
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := s.groupID(w, r)
	if !ok {
		return
	}
	formerMembers, err := s.store.DeleteSCIMGroup(r.Context(), id, s.groupAudit(id, "scim.group.delete"))
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if err != nil {
		s.internalError(w, "scim delete group", err)
		return
	}
	s.recompute(r, formerMembers)
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

// recompute re-derives each affected user's role + tenant grants from their
// current SCIM group membership. Errors are logged, not surfaced — the group
// mutation already succeeded.
func (s *Server) recompute(r *http.Request, users []uuid.UUID) {
	for _, uid := range users {
		err := s.store.RecomputeSCIMUserAccess(r.Context(), uid, s.mapping, nil, []audit.Entry{{
			Action: "scim.access.recompute", EntityType: "user", EntityID: uid.String(),
		}})
		if err != nil && !errors.Is(err, store.ErrNotFound) && s.log != nil {
			s.log.Error("scim: recompute user access failed", "user", uid, "err", err)
		}
	}
}

func (s *Server) groupAudit(id uuid.UUID, action string) []audit.Entry {
	return []audit.Entry{{Action: action, EntityType: "scim_group", EntityID: id.String()}}
}

func (s *Server) groupID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.writeError(w, http.StatusNotFound, "group not found")
		return uuid.Nil, false
	}
	return id, true
}

func (s *Server) loadGroup(w http.ResponseWriter, r *http.Request) (store.SCIMGroup, bool) {
	id, ok := s.groupID(w, r)
	if !ok {
		return store.SCIMGroup{}, false
	}
	g, err := s.store.GetSCIMGroup(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "group not found")
		return store.SCIMGroup{}, false
	}
	if err != nil {
		s.internalError(w, "scim get group", err)
		return store.SCIMGroup{}, false
	}
	return g, true
}

func (s *Server) handleGroupWriteErr(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, http.StatusNotFound, "group not found or member is not a known user")
		return false
	case errors.Is(err, store.ErrConflict):
		s.writeError(w, http.StatusConflict, "a group with this externalId already exists")
		return false
	default:
		s.internalError(w, "scim group write", err)
		return false
	}
}

func (s *Server) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "could not read request body")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// decodeMembers parses a PATCH op value that is a list of member objects.
func decodeMembers(raw json.RawMessage) []groupMember {
	var members []groupMember
	_ = json.Unmarshal(raw, &members)
	return members
}

func parseDisplayNameFilter(filter string) string {
	f := strings.TrimSpace(filter)
	if !strings.HasPrefix(strings.ToLower(f), "displayname eq ") {
		return ""
	}
	return strings.Trim(strings.TrimSpace(f[len("displayName eq "):]), `"`)
}

func setKeys(set map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}
