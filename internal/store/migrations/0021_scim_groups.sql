-- +goose Up

-- SCIM Groups: an IdP (Okta/Entra/…) pushes groups + membership over SCIM, and
-- membership drives each member's role and per-customer tenant grants (by naming
-- convention — see the scim package). Groups are keyed by the id we mint at
-- create time; external_id is the IdP's own id.
CREATE TABLE scim_groups (
    id           UUID PRIMARY KEY,
    external_id  TEXT,
    display_name TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX scim_groups_external_id_key ON scim_groups (external_id) WHERE external_id IS NOT NULL;

CREATE TABLE scim_group_members (
    group_id UUID NOT NULL REFERENCES scim_groups(id) ON DELETE CASCADE,
    user_id  UUID NOT NULL REFERENCES users(id)       ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX scim_group_members_user_id_idx ON scim_group_members (user_id);

-- Marks a user whose role + tenant grants are governed by SCIM group membership
-- ("authoritative"). While true, the Guard scopes the user to EXACTLY their
-- grants (an empty grant set means NO customer access, not all-access — the
-- opposite of the manual default). A user who has never been in a mapped group
-- stays false and is managed manually as before.
ALTER TABLE users ADD COLUMN scim_managed BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN scim_managed;
DROP TABLE scim_group_members;
DROP TABLE scim_groups;
