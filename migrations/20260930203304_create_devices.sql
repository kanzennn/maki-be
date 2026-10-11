-- +goose Up

-- An outlet's cashier devices: a browser on the till's PC, and later the
-- Android app. Owned by the device module. A device sells only once one of
-- the outlet's Managers has activated it, and stops at once when revoked.
-- Never deleted: its code is in the receipt numbers it made.
CREATE TABLE devices (
    device_id    VARCHAR(32) PRIMARY KEY,
    outlet_id    VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    -- In every receipt number the device makes, after the outlet's code:
    -- K01, K02, … in the order the outlet's devices were activated. Never
    -- reused, so a receipt number always points at one device.
    code         TEXT        NOT NULL
        CONSTRAINT devices_code_check CHECK (code ~ '^K[0-9]{2,4}$'),
    name         TEXT        NOT NULL
        CONSTRAINT devices_name_check CHECK (length(name) BETWEEN 1 AND 50),
    platform     TEXT        NOT NULL
        CONSTRAINT devices_platform_check CHECK (platform IN ('web', 'android')),
    -- SHA-256 of the device's token, which only the device itself holds.
    token_hash   TEXT        NOT NULL
        CONSTRAINT devices_token_hash_key UNIQUE
        CONSTRAINT devices_token_hash_check CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    activated_by VARCHAR(32) NOT NULL REFERENCES users (user_id),
    -- When the device last sold or asked for its catalog.
    last_seen_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    revoked_by   VARCHAR(32) REFERENCES users (user_id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT devices_outlet_code_key UNIQUE (outlet_id, code),
    -- For orders to reference, so an order's device is of its outlet.
    CONSTRAINT devices_outlet_key UNIQUE (device_id, outlet_id),
    -- Revoked by someone, or not at all.
    CONSTRAINT devices_revoked_check CHECK ((revoked_at IS NULL) = (revoked_by IS NULL))
);

-- +goose Down

DROP TABLE devices;
