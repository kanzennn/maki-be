-- +goose Up

-- The Owner's discount vouchers, for every outlet or some. Owned by the
-- voucher module. Soft-deleted, and only while never used: a used voucher
-- stays with the orders that name it.
CREATE TABLE vouchers (
    voucher_id    VARCHAR(32) PRIMARY KEY,
    -- Typed at the cashier: capital letters and digits.
    code          TEXT        NOT NULL
        CONSTRAINT vouchers_code_check CHECK (code ~ '^[A-Z0-9]{3,20}$'),
    name          TEXT
        CONSTRAINT vouchers_name_check CHECK (length(name) BETWEEN 1 AND 100),
    -- percent: value is basis points (1000 is 10%) of the subtotal, rounded
    -- half up, at most max_discount; amount: value is rupiah. Either way the
    -- discount is never past the subtotal (api/test-vectors/voucher-discounts.json).
    type          TEXT        NOT NULL
        CONSTRAINT vouchers_type_check CHECK (type IN ('percent', 'amount')),
    value         BIGINT      NOT NULL
        CONSTRAINT vouchers_value_check CHECK (value > 0 AND (type = 'amount' OR value <= 10000)),
    max_discount  BIGINT
        CONSTRAINT vouchers_max_discount_check CHECK (max_discount IS NULL OR (max_discount > 0 AND type = 'percent')),
    -- The subtotal it needs, in rupiah; NULL needs none.
    min_spend     BIGINT
        CONSTRAINT vouchers_min_spend_check CHECK (min_spend > 0),
    -- Uses in every outlet together, and how many are taken: a sale takes
    -- one, and its void gives it back.
    quota         INTEGER     NOT NULL
        CONSTRAINT vouchers_quota_check CHECK (quota BETWEEN 1 AND 1000000),
    used_count    INTEGER     NOT NULL DEFAULT 0,
    -- NULL starts at once.
    start_at      TIMESTAMPTZ,
    expired_at    TIMESTAMPTZ NOT NULL,
    -- Every outlet, those still to come included; otherwise the outlets in
    -- voucher_outlets.
    all_outlets   BOOLEAN     NOT NULL,
    is_active     BOOLEAN     NOT NULL DEFAULT true,
    -- The first sale that used it, voided since or not: from then on its code
    -- and type stay, its quota only grows, and it is never deleted.
    first_used_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,

    CONSTRAINT vouchers_used_count_check CHECK (used_count BETWEEN 0 AND quota),
    CONSTRAINT vouchers_period_check CHECK (start_at IS NULL OR expired_at > start_at),
    CONSTRAINT vouchers_first_used_check CHECK (used_count = 0 OR first_used_at IS NOT NULL),
    CONSTRAINT vouchers_deleted_check CHECK (deleted_at IS NULL OR first_used_at IS NULL)
);

-- Unique among the vouchers not deleted: a deleted one was never used, so
-- its code is free again.
CREATE UNIQUE INDEX vouchers_code_key ON vouchers (code) WHERE deleted_at IS NULL;

-- A voucher's status at a moment, the one definition every query uses, in
-- this order: inactive (turned off), expired, used_up (every use taken),
-- scheduled (not started yet), or active.
-- +goose StatementBegin
CREATE FUNCTION voucher_status(
    is_active BOOLEAN, start_at TIMESTAMPTZ, expired_at TIMESTAMPTZ, used_count INTEGER, quota INTEGER,
    at TIMESTAMPTZ
) RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN NOT is_active THEN 'inactive'
        WHEN expired_at <= at THEN 'expired'
        WHEN used_count >= quota THEN 'used_up'
        WHEN start_at > at THEN 'scheduled'
        ELSE 'active'
    END
$$;
-- +goose StatementEnd

-- The outlets a voucher is for, when it isn't for all of them.
CREATE TABLE voucher_outlets (
    voucher_id VARCHAR(32) NOT NULL REFERENCES vouchers (voucher_id),
    outlet_id  VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    PRIMARY KEY (voucher_id, outlet_id)
);

-- An outlet's vouchers, for its Manager's list.
CREATE INDEX voucher_outlets_outlet_id_idx ON voucher_outlets (outlet_id);

-- An order's voucher, which the order module's table kept a place for: its
-- foreign key now, and its code copied at the sale, as a line's menu name
-- is, so the receipt shows the code it was sold with. A discount needs a
-- voucher; a voucher may give none, rounded to zero on a small subtotal.
ALTER TABLE orders
    ADD COLUMN voucher_code TEXT,
    ADD CONSTRAINT orders_voucher_fkey FOREIGN KEY (voucher_id) REFERENCES vouchers (voucher_id),
    ADD CONSTRAINT orders_voucher_check CHECK (
        (voucher_id IS NULL) = (voucher_code IS NULL)
        AND (voucher_discount = 0 OR voucher_id IS NOT NULL)
    );

-- A voucher's uses, newest first.
CREATE INDEX orders_voucher_id_idx ON orders (voucher_id, created_at DESC) WHERE voucher_id IS NOT NULL;

-- +goose Down
DROP INDEX orders_voucher_id_idx;
ALTER TABLE orders
    DROP CONSTRAINT orders_voucher_check,
    DROP CONSTRAINT orders_voucher_fkey,
    DROP COLUMN voucher_code;
DROP TABLE voucher_outlets;
DROP FUNCTION voucher_status(BOOLEAN, TIMESTAMPTZ, TIMESTAMPTZ, INTEGER, INTEGER, TIMESTAMPTZ);
DROP TABLE vouchers;
