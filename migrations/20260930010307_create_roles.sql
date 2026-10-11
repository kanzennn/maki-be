-- +goose Up

-- An outlet's custom roles: named sets of permissions that Managers give
-- their staff. Owned by the user module. The Owner and Manager roles are
-- fixed in code rather than rows here, so nobody can change or delete them.
CREATE TABLE roles (
    role_id     VARCHAR(32) PRIMARY KEY,
    outlet_id   VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    name        TEXT        NOT NULL
        CONSTRAINT roles_name_check CHECK (length(name) BETWEEN 1 AND 50),
    description TEXT
        CONSTRAINT roles_description_check CHECK (length(description) <= 200),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,

    -- For user_roles to reference, so a role can only go to a user of the
    -- same outlet.
    CONSTRAINT roles_outlet_key UNIQUE (role_id, outlet_id)
);

-- Unique per outlet, ignoring case, among roles that aren't deleted.
CREATE UNIQUE INDEX roles_outlet_name_key ON roles (outlet_id, lower(name)) WHERE deleted_at IS NULL;

CREATE TABLE role_permissions (
    role_id         VARCHAR(32) NOT NULL REFERENCES roles (role_id),
    -- A code such as menu.manage, checked against the catalog in the
    -- application, so adding a permission needs no migration.
    permission_code TEXT        NOT NULL
        CONSTRAINT role_permissions_code_check CHECK (permission_code ~ '^[a-z]+\.[a-z]+$'),
    PRIMARY KEY (role_id, permission_code)
);

-- For user_roles to reference, like roles_outlet_key.
ALTER TABLE users ADD CONSTRAINT users_outlet_key UNIQUE (user_id, outlet_id);

-- A staff member's custom role. One per user for now (user_roles_user_key);
-- the table allows more later without moving any data. The two composite
-- keys keep the role in the user's own outlet, and the Owner, who has no
-- outlet, out entirely.
CREATE TABLE user_roles (
    user_id   VARCHAR(32) NOT NULL,
    role_id   VARCHAR(32) NOT NULL,
    outlet_id VARCHAR(32) NOT NULL,
    PRIMARY KEY (user_id, role_id),
    CONSTRAINT user_roles_user_key UNIQUE (user_id),
    CONSTRAINT user_roles_user_fkey FOREIGN KEY (user_id, outlet_id) REFERENCES users (user_id, outlet_id),
    CONSTRAINT user_roles_role_fkey FOREIGN KEY (role_id, outlet_id) REFERENCES roles (role_id, outlet_id)
);

CREATE INDEX user_roles_role_id_idx ON user_roles (role_id);

-- +goose Down

DROP TABLE user_roles;
ALTER TABLE users DROP CONSTRAINT users_outlet_key;
DROP TABLE role_permissions;
DROP TABLE roles;
