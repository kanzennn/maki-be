-- +goose Up

-- The Log Action: every security-relevant or business action, written in the
-- same transaction as the action. Append-only — the triggers below refuse
-- UPDATE, DELETE and TRUNCATE for everyone, the application included.
CREATE TABLE action_logs (
    action_log_id VARCHAR(32) PRIMARY KEY,
    -- NULL for actions outside any outlet, such as the Owner's own.
    outlet_id     VARCHAR(32),
    -- NULL when nobody is known, such as a failed sign-in with an unknown
    -- username. The username typed is never stored: people type passwords
    -- into the username field by mistake.
    user_id       VARCHAR(32) REFERENCES users (user_id),
    device_id     VARCHAR(32),
    -- <module>.<what happened>, e.g. auth.login, menu.update.
    action        TEXT        NOT NULL,
    target_type   TEXT,
    target_id     VARCHAR(32),
    -- Stored exactly as they were, never translated.
    before_json   JSONB,
    after_json    JSONB,
    ip            INET,
    -- When it happened: the device's clock for actions made offline.
    occurred_at   TIMESTAMPTZ NOT NULL,
    -- When the server recorded it.
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX action_logs_occurred_at_idx ON action_logs (occurred_at DESC);
CREATE INDEX action_logs_outlet_id_idx ON action_logs (outlet_id, occurred_at DESC);
CREATE INDEX action_logs_user_id_idx ON action_logs (user_id, occurred_at DESC);

-- +goose StatementBegin
CREATE FUNCTION action_logs_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'action_logs is append-only: % is not allowed', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER action_logs_no_update_or_delete
    BEFORE UPDATE OR DELETE ON action_logs
    FOR EACH ROW EXECUTE FUNCTION action_logs_append_only();

CREATE TRIGGER action_logs_no_truncate
    BEFORE TRUNCATE ON action_logs
    FOR EACH STATEMENT EXECUTE FUNCTION action_logs_append_only();

-- +goose Down

DROP TABLE action_logs;
DROP FUNCTION action_logs_append_only();
