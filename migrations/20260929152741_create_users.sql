-- +goose Up

-- Everyone who signs in: the one Owner, outlet Managers, and staff with a
-- custom role. Owned by the auth module; nobody signs in with anything but a
-- username and password.
CREATE TABLE users (
    user_id              VARCHAR(32) PRIMARY KEY,
    -- The outlet a Manager or staff member belongs to; NULL for the Owner.
    -- Its foreign key to outlets arrives with the outlet module.
    outlet_id            VARCHAR(32),
    name                 TEXT        NOT NULL
        CONSTRAINT users_name_check CHECK (length(name) BETWEEN 1 AND 100),
    -- Stored lowercase, so uniqueness and sign-in ignore case. Never reused,
    -- even after the user is deleted, so a username in old logs always means
    -- the same person.
    username             TEXT        NOT NULL
        CONSTRAINT users_username_key UNIQUE
        CONSTRAINT users_username_check CHECK (username ~ '^[a-z0-9][a-z0-9._-]{2,31}$'),
    -- Stored lowercase. Optional, except for the Owner, whose password reset
    -- goes to it.
    email                TEXT
        CONSTRAINT users_email_check CHECK (email = lower(email) AND email LIKE '%_@_%'),
    email_verified_at    TIMESTAMPTZ,
    phone                TEXT,
    photo_key            TEXT,
    -- The user's own language; NULL follows their outlet's. Checked against
    -- the catalogs in the application, so adding a language needs no
    -- migration.
    locale               TEXT,
    -- argon2id, in PHC string format with its parameters.
    password_hash        TEXT        NOT NULL,
    -- Set when someone else chose the password (a new account, a reset by a
    -- Manager or the Owner); the user must replace it before anything else.
    must_change_password BOOLEAN     NOT NULL DEFAULT false,
    is_owner             BOOLEAN     NOT NULL DEFAULT false,
    is_manager           BOOLEAN     NOT NULL DEFAULT false,
    status               TEXT        NOT NULL DEFAULT 'active'
        CONSTRAINT users_status_check CHECK (status IN ('active', 'inactive')),
    last_login_at        TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at           TIMESTAMPTZ,

    -- The Owner belongs to no outlet; everyone else to exactly one.
    CONSTRAINT users_outlet_check CHECK (is_owner = (outlet_id IS NULL)),
    CONSTRAINT users_owner_manager_check CHECK (NOT (is_owner AND is_manager)),
    CONSTRAINT users_owner_email_check CHECK (NOT is_owner OR email IS NOT NULL),
    -- The Owner can be neither deactivated nor deleted.
    CONSTRAINT users_owner_active_check CHECK (NOT is_owner OR (status = 'active' AND deleted_at IS NULL))
);

-- There is exactly one Owner: a second `api create-owner` fails here even if
-- two run at the same moment.
CREATE UNIQUE INDEX users_single_owner_key ON users (is_owner) WHERE is_owner;

CREATE INDEX users_outlet_id_idx ON users (outlet_id) WHERE deleted_at IS NULL;

-- +goose Down

DROP TABLE users;
