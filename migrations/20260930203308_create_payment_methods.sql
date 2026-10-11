-- +goose Up

-- Which payment methods each outlet's cashier offers: Payment Control. Owned
-- by the payment module. The methods themselves are fixed in code; each
-- outlet has a row per method saying whether it is on.
CREATE TABLE outlet_payment_methods (
    outlet_id  VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    method     TEXT        NOT NULL
        CONSTRAINT outlet_payment_methods_method_check CHECK (method IN ('cash', 'qris', 'debit')),
    is_enabled BOOLEAN     NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (outlet_id, method)
);

-- The outlets that exist already start as a new one does: with cash, the
-- one method that works offline, and the others off.
INSERT INTO outlet_payment_methods (outlet_id, method, is_enabled)
SELECT o.outlet_id, m.method, m.method = 'cash'
FROM outlets o
CROSS JOIN (VALUES ('cash'), ('qris'), ('debit')) AS m (method);

-- +goose Down

DROP TABLE outlet_payment_methods;
