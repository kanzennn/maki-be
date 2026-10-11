-- +goose Up

-- The cashier's transactions. Owned by the order module. The device makes an
-- order's ID and receipt number, so a resend is the same order and numbers
-- never collide between devices, offline ones included. Transactions are
-- never deleted, and change only by being voided.
CREATE TABLE orders (
    order_id          VARCHAR(32) PRIMARY KEY,
    outlet_id         VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    device_id         VARCHAR(32) NOT NULL,
    -- KODEOUTLET-KODEDEVICE-YYYYMMDD-0001: the outlet's code, the device's,
    -- the day in the outlet's time zone, and the device's count that day.
    order_no          TEXT        NOT NULL
        CONSTRAINT orders_order_no_check CHECK (order_no ~ '^[A-Z0-9]{2,10}-K[0-9]{2,4}-[0-9]{8}-[0-9]{4,6}$'),
    type              TEXT        NOT NULL
        CONSTRAINT orders_type_check CHECK (type IN ('dine_in', 'takeaway')),
    -- A dine-in order's table; its foreign key comes with the table module.
    table_id          VARCHAR(32),
    cashier_id        VARCHAR(32) NOT NULL REFERENCES users (user_id),
    -- Rupiah. The subtotal is the items'; a voucher's discount comes off it,
    -- the service charge is on what is left, and the tax on that plus the
    -- service, each rounded to the nearest rupiah, half up (the CHECKs below,
    -- and api/test-vectors/order-totals.json). The rates are the ones used,
    -- kept with the order.
    subtotal          BIGINT      NOT NULL
        CONSTRAINT orders_subtotal_check CHECK (subtotal BETWEEN 0 AND 1000000000),
    -- The voucher's; its foreign key comes with the voucher module.
    voucher_id        VARCHAR(32),
    voucher_discount  BIGINT      NOT NULL DEFAULT 0,
    service_bps       INTEGER     NOT NULL
        CONSTRAINT orders_service_bps_check CHECK (service_bps BETWEEN 0 AND 10000),
    service           BIGINT      NOT NULL,
    tax_bps           INTEGER     NOT NULL
        CONSTRAINT orders_tax_bps_check CHECK (tax_bps BETWEEN 0 AND 10000),
    tax               BIGINT      NOT NULL,
    total             BIGINT      NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'paid'
        CONSTRAINT orders_status_check CHECK (status IN ('paid', 'void')),
    -- Made while the device was offline, and sent once it was back.
    is_offline        BOOLEAN     NOT NULL DEFAULT false,
    -- When the device made it; created_at is when the server took it.
    client_created_at TIMESTAMPTZ NOT NULL,
    -- SHA-256 of the order as the device sent it, which tells a resend from
    -- another order sent with the same ID.
    request_hash      TEXT        NOT NULL
        CONSTRAINT orders_request_hash_check CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    voided_at         TIMESTAMPTZ,
    voided_by         VARCHAR(32) REFERENCES users (user_id),
    void_reason       TEXT
        CONSTRAINT orders_void_reason_check CHECK (length(void_reason) BETWEEN 1 AND 200),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The device is of the order's outlet.
    CONSTRAINT orders_device_fkey FOREIGN KEY (device_id, outlet_id) REFERENCES devices (device_id, outlet_id),
    CONSTRAINT orders_outlet_order_no_key UNIQUE (outlet_id, order_no),
    -- For items, payments and stock movements to reference.
    CONSTRAINT orders_outlet_key UNIQUE (order_id, outlet_id),
    CONSTRAINT orders_voucher_discount_check CHECK (voucher_discount BETWEEN 0 AND subtotal),
    CONSTRAINT orders_service_check CHECK (service = ((subtotal - voucher_discount) * service_bps + 5000) / 10000),
    CONSTRAINT orders_tax_check CHECK (tax = ((subtotal - voucher_discount + service) * tax_bps + 5000) / 10000),
    CONSTRAINT orders_total_check CHECK (total = subtotal - voucher_discount + service + tax),
    -- A void has its time, who did it and why; nothing else has any of them.
    CONSTRAINT orders_void_check CHECK (
        (status = 'void') = (voided_at IS NOT NULL)
        AND (voided_at IS NULL) = (voided_by IS NULL)
        AND (voided_at IS NULL) = (void_reason IS NULL)
    )
);

-- An outlet's orders of a day, newest first; a device's orders of a day, for
-- the next number in its count.
CREATE INDEX orders_outlet_created_idx ON orders (outlet_id, client_created_at DESC);
CREATE INDEX orders_device_created_idx ON orders (device_id, client_created_at);

-- An order's lines, in the receipt's order. The menu's name and price are
-- copied at the sale, so a later change leaves the receipt as it was.
CREATE TABLE order_items (
    order_item_id VARCHAR(32) PRIMARY KEY,
    outlet_id     VARCHAR(32) NOT NULL,
    order_id      VARCHAR(32) NOT NULL,
    position      INTEGER     NOT NULL
        CONSTRAINT order_items_position_check CHECK (position >= 0),
    menu_id       VARCHAR(32) NOT NULL,
    menu_name     TEXT        NOT NULL
        CONSTRAINT order_items_menu_name_check CHECK (length(menu_name) BETWEEN 1 AND 100),
    price         BIGINT      NOT NULL
        CONSTRAINT order_items_price_check CHECK (price BETWEEN 0 AND 100000000),
    qty           INTEGER     NOT NULL
        CONSTRAINT order_items_qty_check CHECK (qty BETWEEN 1 AND 999),
    -- The customer's wish, such as "less sugar".
    note          TEXT
        CONSTRAINT order_items_note_check CHECK (length(note) BETWEEN 1 AND 100),
    line_total    BIGINT      NOT NULL,

    CONSTRAINT order_items_order_fkey FOREIGN KEY (order_id, outlet_id) REFERENCES orders (order_id, outlet_id),
    -- The menu is of the order's outlet.
    CONSTRAINT order_items_menu_fkey FOREIGN KEY (menu_id, outlet_id) REFERENCES menus (menu_id, outlet_id),
    CONSTRAINT order_items_order_position_key UNIQUE (order_id, position),
    CONSTRAINT order_items_line_total_check CHECK (line_total = price * qty)
);

-- How an order was paid: one payment each for now, in cash.
CREATE TABLE payments (
    payment_id VARCHAR(32) PRIMARY KEY,
    outlet_id  VARCHAR(32) NOT NULL,
    order_id   VARCHAR(32) NOT NULL
        CONSTRAINT payments_order_key UNIQUE,
    method     TEXT        NOT NULL
        CONSTRAINT payments_method_check CHECK (method IN ('cash', 'qris', 'debit')),
    amount     BIGINT      NOT NULL
        CONSTRAINT payments_amount_check CHECK (amount >= 0),
    -- Cash: what the customer handed over, and what they got back.
    received   BIGINT,
    change     BIGINT,
    -- A card's or QRIS's reference, when those methods arrive.
    reference  TEXT
        CONSTRAINT payments_reference_check CHECK (length(reference) BETWEEN 1 AND 64),
    status     TEXT        NOT NULL
        CONSTRAINT payments_status_check CHECK (status IN ('paid', 'void')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payments_order_fkey FOREIGN KEY (order_id, outlet_id) REFERENCES orders (order_id, outlet_id),
    -- Cash gives change from what was handed over, never less than the
    -- amount; the other methods have neither.
    CONSTRAINT payments_cash_check CHECK (
        CASE WHEN method = 'cash'
            THEN received IS NOT NULL AND change IS NOT NULL AND received >= amount AND change = received - amount
            ELSE received IS NULL AND change IS NULL
        END
    )
);

-- A sale or a void of stock names its order, and nothing else does. The
-- column came with the menu module; its foreign key comes now.
ALTER TABLE stock_movements
    ADD CONSTRAINT stock_movements_order_fkey FOREIGN KEY (order_id, outlet_id) REFERENCES orders (order_id, outlet_id),
    ADD CONSTRAINT stock_movements_order_check CHECK ((type IN ('sale', 'void')) = (order_id IS NOT NULL));

-- Transactions are kept as they were made: never deleted, their lines never
-- changed.
-- +goose StatementBegin
CREATE FUNCTION transactions_kept() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% on % is not allowed: transactions are kept as they were made', TG_OP, TG_TABLE_NAME
        USING ERRCODE = 'insufficient_privilege';
END;
$$;
-- +goose StatementEnd

-- An order or its payment changes only by being voided: from paid to void,
-- with nothing changed but the columns the trigger names.
-- +goose StatementBegin
CREATE FUNCTION transactions_void_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'paid' AND NEW.status = 'void'
        AND to_jsonb(NEW) - TG_ARGV = to_jsonb(OLD) - TG_ARGV THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'this UPDATE on % is not allowed: a transaction only changes by being voided', TG_TABLE_NAME
        USING ERRCODE = 'insufficient_privilege';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER orders_void_only
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION transactions_void_only('status', 'voided_at', 'voided_by', 'void_reason');
CREATE TRIGGER orders_no_delete
    BEFORE DELETE ON orders
    FOR EACH ROW EXECUTE FUNCTION transactions_kept();
CREATE TRIGGER orders_no_truncate
    BEFORE TRUNCATE ON orders
    FOR EACH STATEMENT EXECUTE FUNCTION transactions_kept();

CREATE TRIGGER order_items_no_update_or_delete
    BEFORE UPDATE OR DELETE ON order_items
    FOR EACH ROW EXECUTE FUNCTION transactions_kept();
CREATE TRIGGER order_items_no_truncate
    BEFORE TRUNCATE ON order_items
    FOR EACH STATEMENT EXECUTE FUNCTION transactions_kept();

CREATE TRIGGER payments_void_only
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION transactions_void_only('status');
CREATE TRIGGER payments_no_delete
    BEFORE DELETE ON payments
    FOR EACH ROW EXECUTE FUNCTION transactions_kept();
CREATE TRIGGER payments_no_truncate
    BEFORE TRUNCATE ON payments
    FOR EACH STATEMENT EXECUTE FUNCTION transactions_kept();

-- +goose Down

ALTER TABLE stock_movements
    DROP CONSTRAINT stock_movements_order_check,
    DROP CONSTRAINT stock_movements_order_fkey;
DROP TABLE payments;
DROP TABLE order_items;
DROP TABLE orders;
DROP FUNCTION transactions_void_only();
DROP FUNCTION transactions_kept();
