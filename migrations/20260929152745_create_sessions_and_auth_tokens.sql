-- +goose Up

-- Refresh tokens, one row per token. Every sign-in starts a family, whose ID
-- is the session ID in access tokens; each refresh replaces the family's
-- current token with a new one. The session is alive while the family has a
-- current token (not replaced, not revoked, not expired). Presenting a token
-- that was already replaced means it was stolen or copied, and revokes the
-- whole family.
CREATE TABLE refresh_tokens (
    refresh_token_id VARCHAR(32) PRIMARY KEY,
    user_id          VARCHAR(32) NOT NULL REFERENCES users (user_id),
    family_id        VARCHAR(32) NOT NULL,
    -- SHA-256 of the token, hex. The token itself is never stored.
    token_hash       TEXT        NOT NULL
        CONSTRAINT refresh_tokens_token_hash_key UNIQUE,
    expires_at       TIMESTAMPTZ NOT NULL,
    -- The token that replaced this one at its refresh.
    replaced_by      VARCHAR(32) REFERENCES refresh_tokens (refresh_token_id),
    replaced_at      TIMESTAMPTZ,
    -- Set on every token of the family when the session ends: sign-out,
    -- password change or reset, a replayed token, the user deactivated.
    revoked_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT refresh_tokens_replaced_check CHECK ((replaced_by IS NULL) = (replaced_at IS NULL))
);

-- Is this session alive? Asked on every authenticated request.
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);
-- Revoke every session of a user.
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id) WHERE revoked_at IS NULL;

-- Single-use tokens sent by email: password reset (30 minutes) and email
-- verification. Stored hashed; the plain token only ever exists in the
-- email's link.
CREATE TABLE auth_tokens (
    auth_token_id VARCHAR(32) PRIMARY KEY,
    user_id       VARCHAR(32) NOT NULL REFERENCES users (user_id),
    purpose       TEXT        NOT NULL
        CONSTRAINT auth_tokens_purpose_check CHECK (purpose IN ('password_reset', 'email_verify')),
    -- The address being verified, for email_verify. The user's current
    -- email stays in place until this one is confirmed.
    email         TEXT,
    token_hash    TEXT        NOT NULL
        CONSTRAINT auth_tokens_token_hash_key UNIQUE,
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT auth_tokens_email_check CHECK ((purpose = 'email_verify') = (email IS NOT NULL))
);

CREATE INDEX auth_tokens_user_id_idx ON auth_tokens (user_id, purpose) WHERE used_at IS NULL;

-- +goose Down

DROP TABLE auth_tokens;
DROP TABLE refresh_tokens;
