-- +goose Up

-- How an outlet seats its dine-in customers: at the tables of its layout,
-- which the cashier picks, or wherever they set down the number stand the
-- cashier gave them, where a waiter brings the order. Owned by the table
-- module. An outlet without a row seats them at tables.
CREATE TABLE dine_in_settings (
    outlet_id  VARCHAR(32) PRIMARY KEY REFERENCES outlets (outlet_id),
    mode       TEXT        NOT NULL
        CONSTRAINT dine_in_settings_mode_check CHECK (mode IN ('table', 'number')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by VARCHAR(32) NOT NULL REFERENCES users (user_id)
);

-- The number on the stand a dine-in customer took, as the cashier typed it:
-- 1 to 10 capitals, digits and hyphens, a hyphen only between them. An
-- order is at a table or at a stand, never both. Owned by the order module,
-- as the order's table is.
ALTER TABLE orders
    ADD COLUMN stand_number TEXT,
    ADD CONSTRAINT orders_stand_number_check CHECK (
        stand_number IS NULL
        OR (
            stand_number ~ '^[A-Z0-9]([A-Z0-9-]{0,8}[A-Z0-9])?$'
            AND type = 'dine_in'
            AND table_id IS NULL
        )
    );

-- +goose Down
ALTER TABLE orders
    DROP CONSTRAINT orders_stand_number_check,
    DROP COLUMN stand_number;
DROP TABLE dine_in_settings;
