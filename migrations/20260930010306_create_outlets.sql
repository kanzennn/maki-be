-- +goose Up

-- The business's outlets. Owned by the outlet module; only the Owner
-- creates and changes them.
CREATE TABLE outlets (
    outlet_id   VARCHAR(32) PRIMARY KEY,
    -- Printed in receipt numbers, so never reused, even after the outlet is
    -- deleted: an old receipt always points at one outlet.
    code        TEXT        NOT NULL
        CONSTRAINT outlets_code_key UNIQUE
        CONSTRAINT outlets_code_check CHECK (code ~ '^[A-Z0-9]{2,10}$'),
    name        TEXT        NOT NULL
        CONSTRAINT outlets_name_check CHECK (length(name) BETWEEN 1 AND 100),
    address     TEXT
        CONSTRAINT outlets_address_check CHECK (length(address) <= 500),
    phone       TEXT
        CONSTRAINT outlets_phone_check CHECK (length(phone) <= 32),
    -- An IANA name such as Asia/Jakarta, checked by the application: every
    -- time the outlet shows is in this zone, whatever the device's is.
    timezone    TEXT        NOT NULL,
    -- The default language: receipts, and users who haven't chosen one.
    -- Checked against the catalogs in the application, like users.locale.
    locale      TEXT        NOT NULL,
    -- Opening hours; closing before opening means past midnight.
    open_time   TIME,
    close_time  TIME,
    -- Tax and service charge in basis points: 1100 is 11%. Integers, so no
    -- float ever enters an amount of money.
    tax_bps     INTEGER     NOT NULL DEFAULT 0
        CONSTRAINT outlets_tax_check CHECK (tax_bps BETWEEN 0 AND 10000),
    service_bps INTEGER     NOT NULL DEFAULT 0
        CONSTRAINT outlets_service_check CHECK (service_bps BETWEEN 0 AND 10000),
    status      TEXT        NOT NULL DEFAULT 'active'
        CONSTRAINT outlets_status_check CHECK (status IN ('active', 'inactive')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,

    -- Both hours or neither, and never the same time twice.
    CONSTRAINT outlets_hours_check CHECK (
        (open_time IS NULL AND close_time IS NULL)
        OR (open_time IS NOT NULL AND close_time IS NOT NULL AND open_time <> close_time)
    ),
    -- Only an outlet that was deactivated first can be deleted.
    CONSTRAINT outlets_deleted_check CHECK (deleted_at IS NULL OR status = 'inactive')
);

ALTER TABLE users
    ADD CONSTRAINT users_outlet_id_fkey FOREIGN KEY (outlet_id) REFERENCES outlets (outlet_id);

ALTER TABLE action_logs
    ADD CONSTRAINT action_logs_outlet_id_fkey FOREIGN KEY (outlet_id) REFERENCES outlets (outlet_id);

-- +goose Down

ALTER TABLE action_logs DROP CONSTRAINT action_logs_outlet_id_fkey;
ALTER TABLE users DROP CONSTRAINT users_outlet_id_fkey;
DROP TABLE outlets;
