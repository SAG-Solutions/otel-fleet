package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sag-solutions/otel-fleet/internal/audit"
	"github.com/sag-solutions/otel-fleet/internal/authz"
)

const scimGroupCols = `id, external_id, display_name, created_at, updated_at`

func scanSCIMGroup(row pgx.Row) (SCIMGroup, error) {
	var g SCIMGroup
	err := row.Scan(&g.ID, &g.ExternalID, &g.DisplayName, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

// GetCustomerBySlug resolves a customer by its (unique) slug — used to map a
// `customer:<slug>` SCIM group to a customer. Excludes soft-deleted customers.
func (s *PG) GetCustomerBySlug(ctx context.Context, slug string) (Customer, error) {
	c, err := scanCustomer(s.pool.QueryRow(ctx, `
		SELECT `+customerCols+` FROM customers WHERE slug = $1 AND status <> 'deleted'`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	return c, err
}

func loadGroupMembers(ctx context.Context, q pgx.Tx, id uuid.UUID) ([]uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT user_id FROM scim_group_members WHERE group_id = $1 ORDER BY user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var uid uuid.UUID
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		out = append(out, uid)
	}
	return out, rows.Err()
}

func insertGroupMembers(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, members []uuid.UUID) error {
	for _, uid := range members {
		if _, err := tx.Exec(ctx, `
			INSERT INTO scim_group_members (group_id, user_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, groupID, uid); err != nil {
			if isForeignKeyViolation(err, "scim_group_members_user_id_fkey") {
				return fmt.Errorf("%w: member %s", ErrNotFound, uid)
			}
			return err
		}
	}
	return nil
}

// CreateSCIMGroup inserts a group with its initial members. Returns the created
// group (with Members) and audits it. The returned affected set for recompute is
// the member list itself (handled by the caller).
func (s *PG) CreateSCIMGroup(ctx context.Context, id uuid.UUID, displayName string, externalID *string, members []uuid.UUID, entries []audit.Entry) (SCIMGroup, error) {
	var g SCIMGroup
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		g, err = scanSCIMGroup(tx.QueryRow(ctx, `
			INSERT INTO scim_groups (id, external_id, display_name)
			VALUES ($1, $2, $3)
			RETURNING `+scimGroupCols, id, externalID, displayName))
		if err != nil {
			if isUniqueViolation(err, "scim_groups_external_id_key") {
				return ErrConflict
			}
			return fmt.Errorf("insert scim group: %w", err)
		}
		if err := insertGroupMembers(ctx, tx, id, members); err != nil {
			return err
		}
		g.Members, err = loadGroupMembers(ctx, tx, id)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return SCIMGroup{}, err
	}
	return g, nil
}

// GetSCIMGroup returns a group with its members, or ErrNotFound.
func (s *PG) GetSCIMGroup(ctx context.Context, id uuid.UUID) (SCIMGroup, error) {
	var g SCIMGroup
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		g, err = scanSCIMGroup(tx.QueryRow(ctx, `SELECT `+scimGroupCols+` FROM scim_groups WHERE id = $1`, id))
		if err != nil {
			return err
		}
		g.Members, err = loadGroupMembers(ctx, tx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SCIMGroup{}, ErrNotFound
	}
	return g, err
}

// ListSCIMGroups returns all groups with their members.
func (s *PG) ListSCIMGroups(ctx context.Context) ([]SCIMGroup, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+scimGroupCols+` FROM scim_groups ORDER BY display_name`)
	if err != nil {
		return nil, fmt.Errorf("list scim groups: %w", err)
	}
	groups := map[uuid.UUID]*SCIMGroup{}
	var order []uuid.UUID
	for rows.Next() {
		g, err := scanSCIMGroup(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		gg := g
		groups[g.ID] = &gg
		order = append(order, g.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// One pass over the membership join to attach members.
	mrows, err := s.pool.Query(ctx, `SELECT group_id, user_id FROM scim_group_members ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var gid, uid uuid.UUID
		if err := mrows.Scan(&gid, &uid); err != nil {
			return nil, err
		}
		if g := groups[gid]; g != nil {
			g.Members = append(g.Members, uid)
		}
	}
	if err := mrows.Err(); err != nil {
		return nil, err
	}
	out := make([]SCIMGroup, 0, len(order))
	for _, id := range order {
		out = append(out, *groups[id])
	}
	return out, nil
}

// UpdateSCIMGroup replaces the display name / external id and, when members is
// non-nil, the full member set (PUT semantics). It returns the updated group and
// the set of affected user ids (added ∪ removed ∪ — when the name changed — all
// current members, since the name drives the mapping).
func (s *PG) UpdateSCIMGroup(ctx context.Context, id uuid.UUID, displayName, externalID *string, members *[]uuid.UUID, entries []audit.Entry) (SCIMGroup, []uuid.UUID, error) {
	var g SCIMGroup
	var affected []uuid.UUID
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		before, err := scanSCIMGroup(tx.QueryRow(ctx, `SELECT `+scimGroupCols+` FROM scim_groups WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		oldMembers, err := loadGroupMembers(ctx, tx, id)
		if err != nil {
			return err
		}
		g, err = scanSCIMGroup(tx.QueryRow(ctx, `
			UPDATE scim_groups
			SET display_name = COALESCE($2, display_name),
			    external_id  = COALESCE($3, external_id),
			    updated_at   = now()
			WHERE id = $1
			RETURNING `+scimGroupCols, id, displayName, externalID))
		if err != nil {
			if isUniqueViolation(err, "scim_groups_external_id_key") {
				return ErrConflict
			}
			return err
		}
		affectedSet := map[uuid.UUID]bool{}
		nameChanged := displayName != nil && *displayName != before.DisplayName
		if members != nil {
			if _, err := tx.Exec(ctx, `DELETE FROM scim_group_members WHERE group_id = $1`, id); err != nil {
				return err
			}
			if err := insertGroupMembers(ctx, tx, id, *members); err != nil {
				return err
			}
			for _, u := range oldMembers {
				affectedSet[u] = true
			}
			for _, u := range *members {
				affectedSet[u] = true
			}
		} else if nameChanged {
			// Name (mapping) changed but membership didn't → recompute all members.
			for _, u := range oldMembers {
				affectedSet[u] = true
			}
		}
		g.Members, err = loadGroupMembers(ctx, tx, id)
		if err != nil {
			return err
		}
		affected = setToSlice(affectedSet)
		return audit.Write(ctx, tx, entries...)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SCIMGroup{}, nil, ErrNotFound
	}
	if err != nil {
		return SCIMGroup{}, nil, err
	}
	return g, affected, nil
}

// ModifySCIMGroupMembers applies a PATCH member delta (add/remove) and returns
// the updated group plus the affected user ids (add ∪ remove).
func (s *PG) ModifySCIMGroupMembers(ctx context.Context, id uuid.UUID, add, remove []uuid.UUID, entries []audit.Entry) (SCIMGroup, []uuid.UUID, error) {
	var g SCIMGroup
	var affected []uuid.UUID
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := scanSCIMGroup(tx.QueryRow(ctx, `SELECT `+scimGroupCols+` FROM scim_groups WHERE id = $1 FOR UPDATE`, id)); err != nil {
			return err
		}
		if err := insertGroupMembers(ctx, tx, id, add); err != nil {
			return err
		}
		for _, uid := range remove {
			if _, err := tx.Exec(ctx, `DELETE FROM scim_group_members WHERE group_id = $1 AND user_id = $2`, id, uid); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE scim_groups SET updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		var err error
		g, err = scanSCIMGroup(tx.QueryRow(ctx, `SELECT `+scimGroupCols+` FROM scim_groups WHERE id = $1`, id))
		if err != nil {
			return err
		}
		g.Members, err = loadGroupMembers(ctx, tx, id)
		if err != nil {
			return err
		}
		affectedSet := map[uuid.UUID]bool{}
		for _, u := range add {
			affectedSet[u] = true
		}
		for _, u := range remove {
			affectedSet[u] = true
		}
		affected = setToSlice(affectedSet)
		return audit.Write(ctx, tx, entries...)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SCIMGroup{}, nil, ErrNotFound
	}
	if err != nil {
		return SCIMGroup{}, nil, err
	}
	return g, affected, nil
}

// DeleteSCIMGroup removes the group (members cascade) and returns the former
// member ids so the caller can recompute their access.
func (s *PG) DeleteSCIMGroup(ctx context.Context, id uuid.UUID, entries []audit.Entry) ([]uuid.UUID, error) {
	var formerMembers []uuid.UUID
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		formerMembers, err = loadGroupMembers(ctx, tx, id)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM scim_groups WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return audit.Write(ctx, tx, entries...)
	})
	if err != nil {
		return nil, err
	}
	return formerMembers, nil
}

// RecomputeSCIMUserAccess re-derives one user's role + tenant grants from their
// current SCIM group membership and applies them authoritatively, all in one
// transaction. Rules (see SCIMMapping):
//   - role = the highest role among the user's `<rolePrefix><role>` groups, or
//     the default role if the user is managed but has no role group;
//   - grants = the union of customers named by `<customerPrefix><slug>` groups
//     (unknown slugs are skipped);
//   - a user managed by SCIM keeps scim_managed=true even with no groups left,
//     so an empty grant set means NO customer access (never all-access).
//
// A user who has never been managed and is in no mapped group is left untouched.
// The last-enabled-admin invariant is preserved: a demotion that would remove
// the final admin is skipped (the role stays admin).
func (s *PG) RecomputeSCIMUserAccess(ctx context.Context, userID uuid.UUID, m SCIMMapping, actor *uuid.UUID, entries []audit.Entry) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var currentRole string
		var wasManaged bool
		if err := tx.QueryRow(ctx, `SELECT role, scim_managed FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&currentRole, &wasManaged); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT g.display_name FROM scim_group_members mem
			JOIN scim_groups g ON g.id = mem.group_id
			WHERE mem.user_id = $1`, userID)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var dn string
			if err := rows.Scan(&dn); err != nil {
				rows.Close()
				return err
			}
			names = append(names, dn)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		bestRole := ""
		hasMapped := false
		var slugs []string
		for _, dn := range names {
			if m.RolePrefix != "" && strings.HasPrefix(dn, m.RolePrefix) {
				hasMapped = true
				role := strings.TrimPrefix(dn, m.RolePrefix)
				if authz.Known(role) && authz.Rank(role) > authz.Rank(bestRole) {
					bestRole = role
				}
				continue
			}
			if m.CustomerPrefix != "" && strings.HasPrefix(dn, m.CustomerPrefix) {
				hasMapped = true
				slugs = append(slugs, strings.TrimPrefix(dn, m.CustomerPrefix))
			}
		}

		managed := wasManaged || hasMapped
		if !managed {
			return nil // never managed, in no mapped group → leave manual
		}

		newRole := bestRole
		if newRole == "" {
			newRole = m.DefaultRole
		}
		// Preserve the last-admin invariant: never let SCIM demote the final
		// enabled admin.
		if currentRole == authz.RoleAdmin && newRole != authz.RoleAdmin {
			var admins int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL`).Scan(&admins); err != nil {
				return err
			}
			if admins <= 1 {
				newRole = authz.RoleAdmin
			}
		}

		// Resolve customer slugs → ids (skip unknown).
		grantSet := map[uuid.UUID]bool{}
		for _, slug := range slugs {
			var cid uuid.UUID
			err := tx.QueryRow(ctx, `SELECT id FROM customers WHERE slug = $1 AND status <> 'deleted'`, slug).Scan(&cid)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			grantSet[cid] = true
		}

		if _, err := tx.Exec(ctx, `UPDATE users SET role = $2, scim_managed = true WHERE id = $1`, userID, newRole); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_customer_grants WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for cid := range grantSet {
			if _, err := tx.Exec(ctx, `INSERT INTO user_customer_grants (user_id, customer_id) VALUES ($1, $2)`, userID, cid); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, entries...)
	})
}

func setToSlice(set map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}
