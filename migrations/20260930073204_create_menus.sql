-- +goose Up

-- An outlet's menu categories, shown as tiles at the cashier in sort_order.
-- Owned by the menu module. Soft-deleted, because a deleted menu still
-- points at its category.
CREATE TABLE menu_categories (
    category_id VARCHAR(32) PRIMARY KEY,
    outlet_id   VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    name        TEXT        NOT NULL
        CONSTRAINT menu_categories_name_check CHECK (length(name) BETWEEN 1 AND 50),
    -- An emoji, on the category's tile and on its menus until they have a
    -- photo. A few code points: some emoji join several (🧑‍🍳 is three).
    icon        TEXT
        CONSTRAINT menu_categories_icon_check CHECK (length(icon) BETWEEN 1 AND 8),
    sort_order  INTEGER     NOT NULL DEFAULT 0
        CONSTRAINT menu_categories_sort_order_check CHECK (sort_order >= 0),
    -- An inactive category hides itself and its menus at the cashier.
    is_active   BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,

    -- For menus to reference, so a menu's category is always of its outlet.
    CONSTRAINT menu_categories_outlet_key UNIQUE (category_id, outlet_id)
);

-- Unique per outlet, ignoring case, among categories that aren't deleted.
CREATE UNIQUE INDEX menu_categories_outlet_name_key ON menu_categories (outlet_id, lower(name))
    WHERE deleted_at IS NULL;

-- An outlet's menus. Owned by the menu module.
CREATE TABLE menus (
    menu_id     VARCHAR(32) PRIMARY KEY,
    outlet_id   VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    category_id VARCHAR(32) NOT NULL,
    name        TEXT        NOT NULL
        CONSTRAINT menus_name_check CHECK (length(name) BETWEEN 1 AND 100),
    description TEXT
        CONSTRAINT menus_description_check CHECK (length(description) <= 500),
    sku         TEXT
        CONSTRAINT menus_sku_check CHECK (sku ~ '^[A-Za-z0-9._-]{1,32}$'),
    -- Rupiah, an integer like every amount of money.
    price       BIGINT      NOT NULL
        CONSTRAINT menus_price_check CHECK (price BETWEEN 0 AND 100000000),
    -- The photo's object key, which the upload milestone's worker sets.
    image_key   TEXT,
    -- Tracked stock has a count; untracked stock (a mixed drink, say) is
    -- always available.
    track_stock BOOLEAN     NOT NULL DEFAULT false,
    -- The count after the menu's last stock movement, written in the same
    -- transaction. Adjustments never take it below zero; only a sale made
    -- offline may (the Sync module), so there's no CHECK here.
    stock_qty   INTEGER     NOT NULL DEFAULT 0,
    -- Stock at or below it is low; NULL or 0 turns the alert off.
    stock_alert INTEGER
        CONSTRAINT menus_stock_alert_check CHECK (stock_alert >= 0),
    is_active   BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,

    CONSTRAINT menus_category_fkey FOREIGN KEY (category_id, outlet_id)
        REFERENCES menu_categories (category_id, outlet_id),
    -- For stock_movements to reference, like menu_categories_outlet_key.
    CONSTRAINT menus_outlet_key UNIQUE (menu_id, outlet_id)
);

-- A menu's stock condition, the one definition every query uses: untracked
-- (always available), out (none left), low (at or below its alert), or ok.
-- An alert of 0 or NULL is off, so tracked stock above zero is ok.
-- +goose StatementBegin
CREATE FUNCTION menu_stock_status(track_stock BOOLEAN, stock_qty INTEGER, stock_alert INTEGER) RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN NOT track_stock THEN 'untracked'
        WHEN stock_qty <= 0 THEN 'out'
        WHEN stock_qty <= COALESCE(stock_alert, 0) THEN 'low'
        ELSE 'ok'
    END
$$;
-- +goose StatementEnd

-- A SKU names one menu per outlet, ignoring case, among menus that aren't
-- deleted.
CREATE UNIQUE INDEX menus_outlet_sku_key ON menus (outlet_id, lower(sku))
    WHERE deleted_at IS NULL AND sku IS NOT NULL;
CREATE INDEX menus_outlet_category_idx ON menus (outlet_id, category_id) WHERE deleted_at IS NULL;

-- Every change to a tracked menu's stock. Append-only, like the Log Action:
-- menus.stock_qty is only ever the last row's stock_after.
CREATE TABLE stock_movements (
    stock_movement_id VARCHAR(32) PRIMARY KEY,
    outlet_id         VARCHAR(32) NOT NULL,
    menu_id           VARCHAR(32) NOT NULL,
    -- initial: the count a menu starts tracking with; in, out, set: an
    -- adjustment (set is a stock count); sale, void: the cashier's.
    type              TEXT        NOT NULL
        CONSTRAINT stock_movements_type_check CHECK (type IN ('initial', 'in', 'out', 'set', 'sale', 'void')),
    -- The change to the count: new minus old.
    qty_change        INTEGER     NOT NULL,
    stock_after       INTEGER     NOT NULL,
    reason            TEXT,
    note              TEXT
        CONSTRAINT stock_movements_note_check CHECK (length(note) <= 200),
    -- Who made it: the adjuster, or the cashier of the sale.
    user_id           VARCHAR(32) NOT NULL REFERENCES users (user_id),
    -- The sale or void's order; its foreign key comes with the order module.
    order_id          VARCHAR(32),
    -- When the row is written, not when its transaction began (now()):
    -- changes to one menu's stock wait for its lock in turn, so one that
    -- began first can land second. The history then reads in the order the
    -- count changed in.
    created_at        TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),

    CONSTRAINT stock_movements_menu_fkey FOREIGN KEY (menu_id, outlet_id)
        REFERENCES menus (menu_id, outlet_id),
    -- A starting count says so; an adjustment gives one of four reasons; a
    -- sale or void has its order instead. COALESCE, because a CHECK passes
    -- when its expression is NULL, and `NULL IN (…)` is. An unknown type is
    -- stock_movements_type_check's to refuse.
    CONSTRAINT stock_movements_reason_check CHECK (
        CASE
            WHEN type = 'initial' THEN COALESCE(reason, '') = 'initial'
            WHEN type IN ('in', 'out', 'set') THEN COALESCE(reason, '') IN ('restock', 'damaged', 'correction', 'other')
            WHEN type IN ('sale', 'void') THEN reason IS NULL
            ELSE true
        END
    ),
    -- Stock comes in, or goes out, in the direction its type says.
    CONSTRAINT stock_movements_direction_check CHECK (
        CASE
            WHEN type IN ('in', 'void') THEN qty_change > 0
            WHEN type IN ('out', 'sale') THEN qty_change < 0
            ELSE true
        END
    ),
    -- Only a sale may leave the count below zero: one made offline, sold
    -- before the device knew the stock was gone.
    CONSTRAINT stock_movements_stock_after_check CHECK (stock_after >= 0 OR type = 'sale')
);

CREATE INDEX stock_movements_menu_idx ON stock_movements (menu_id, created_at DESC);

-- +goose StatementBegin
CREATE FUNCTION stock_movements_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'stock_movements is append-only: % is not allowed', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER stock_movements_no_update_or_delete
    BEFORE UPDATE OR DELETE ON stock_movements
    FOR EACH ROW EXECUTE FUNCTION stock_movements_append_only();

CREATE TRIGGER stock_movements_no_truncate
    BEFORE TRUNCATE ON stock_movements
    FOR EACH STATEMENT EXECUTE FUNCTION stock_movements_append_only();

-- +goose Down

DROP TABLE stock_movements;
DROP FUNCTION stock_movements_append_only();
DROP TABLE menus;
DROP FUNCTION menu_stock_status(BOOLEAN, INTEGER, INTEGER);
DROP TABLE menu_categories;
