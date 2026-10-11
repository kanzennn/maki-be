-- +goose Up

-- An outlet's dining areas (Indoor, Outdoor, Lantai 2), each with a canvas
-- of its own, in sort_order. Owned by the table module. Soft-deleted, and
-- only once no table is left in it.
CREATE TABLE table_areas (
    area_id        VARCHAR(32) PRIMARY KEY,
    outlet_id      VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    name           TEXT        NOT NULL
        CONSTRAINT table_areas_name_check CHECK (length(name) BETWEEN 1 AND 50),
    sort_order     INTEGER     NOT NULL DEFAULT 0
        CONSTRAINT table_areas_sort_order_check CHECK (sort_order >= 0),
    -- What the canvas marks besides tables (walls, the cashier, doors), as
    -- [{"kind", "x", "y", "w", "h", "rotation"}], checked by the module.
    decorations    JSONB       NOT NULL DEFAULT '[]'
        CONSTRAINT table_areas_decorations_check CHECK (jsonb_typeof(decorations) = 'array'),
    -- Goes up with each change to the layout or to a table in the area, so a
    -- layout saved from an editor opened before is refused.
    layout_version INTEGER     NOT NULL DEFAULT 0
        CONSTRAINT table_areas_layout_version_check CHECK (layout_version >= 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,

    -- For tables to reference, so a table's area is always of its outlet.
    CONSTRAINT table_areas_outlet_key UNIQUE (area_id, outlet_id)
);

-- Unique per outlet, ignoring case, among areas that aren't deleted.
CREATE UNIQUE INDEX table_areas_outlet_name_key ON table_areas (outlet_id, lower(name))
    WHERE deleted_at IS NULL;

-- An outlet's tables. Owned by the table module. Soft-deleted, because an
-- order still points at its table.
--
-- A table is a box on its area's grid of 48 × 32 cells: x and y are its top
-- left cell, w and h its cells across and down. Its rotation turns its
-- drawing inside the box; a quarter turn swaps w and h. A table without a
-- box isn't placed on the layout yet.
CREATE TABLE tables (
    table_id    VARCHAR(32) PRIMARY KEY,
    outlet_id   VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    area_id     VARCHAR(32) NOT NULL,
    name        TEXT        NOT NULL
        CONSTRAINT tables_name_check CHECK (length(name) BETWEEN 1 AND 30),
    -- Seats; NULL when not given.
    capacity    INTEGER
        CONSTRAINT tables_capacity_check CHECK (capacity BETWEEN 1 AND 99),
    shape       TEXT        NOT NULL DEFAULT 'square'
        CONSTRAINT tables_shape_check CHECK (shape IN ('square', 'round', 'rectangle')),
    x           INTEGER,
    y           INTEGER,
    w           INTEGER,
    h           INTEGER,
    rotation    INTEGER     NOT NULL DEFAULT 0
        CONSTRAINT tables_rotation_check CHECK (rotation IN (0, 90, 180, 270)),
    -- An inactive table stays on the layout, and can't be picked at the
    -- cashier once nobody is seated at it.
    is_active   BOOLEAN     NOT NULL DEFAULT true,
    -- When the cashier last released it: only the sales after it seat anyone
    -- there. Set with clock_timestamp() under the table's lock, as a sale's
    -- created_at is, so the two always compare in the order they happened.
    released_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ,

    CONSTRAINT tables_area_fkey FOREIGN KEY (area_id, outlet_id) REFERENCES table_areas (area_id, outlet_id),
    -- For orders and schedules to reference, so their tables are always of
    -- their outlet.
    CONSTRAINT tables_outlet_key UNIQUE (table_id, outlet_id),
    -- A whole box or none; on the grid, 2 to 16 cells a side.
    CONSTRAINT tables_box_check CHECK (
        (x IS NULL AND y IS NULL AND w IS NULL AND h IS NULL)
        OR (
            x IS NOT NULL AND y IS NOT NULL AND w IS NOT NULL AND h IS NOT NULL
            AND x >= 0 AND y >= 0 AND w BETWEEN 2 AND 16 AND h BETWEEN 2 AND 16
            AND x + w <= 48 AND y + h <= 32
        )
    )
);

-- Unique per outlet, ignoring case, among tables that aren't deleted.
CREATE UNIQUE INDEX tables_outlet_name_key ON tables (outlet_id, lower(name))
    WHERE deleted_at IS NULL;

-- An area's tables.
CREATE INDEX tables_area_id_idx ON tables (area_id) WHERE deleted_at IS NULL;

-- Times a Manager takes tables out of service: an event, a renovation, the
-- terrace in the rainy season. Owned by the table module. A schedule is
-- cancelled, never deleted, and kept once over.
CREATE TABLE table_schedules (
    schedule_id  VARCHAR(32) PRIMARY KEY,
    outlet_id    VARCHAR(32) NOT NULL REFERENCES outlets (outlet_id),
    title        TEXT        NOT NULL
        CONSTRAINT table_schedules_title_check CHECK (length(title) BETWEEN 1 AND 100),
    start_at     TIMESTAMPTZ NOT NULL,
    end_at       TIMESTAMPTZ NOT NULL,
    created_by   VARCHAR(32) NOT NULL REFERENCES users (user_id),
    cancelled_at TIMESTAMPTZ,
    cancelled_by VARCHAR(32) REFERENCES users (user_id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT table_schedules_period_check CHECK (end_at > start_at),
    CONSTRAINT table_schedules_cancelled_check CHECK ((cancelled_at IS NULL) = (cancelled_by IS NULL)),
    -- For its tables to reference, so they are always of its outlet.
    CONSTRAINT table_schedules_outlet_key UNIQUE (schedule_id, outlet_id)
);

-- An outlet's schedules, latest first.
CREATE INDEX table_schedules_outlet_id_idx ON table_schedules (outlet_id, start_at DESC);

-- A schedule's status at a moment, the one definition every query uses, in
-- this order: cancelled, finished (at or past its end), ongoing (started),
-- or upcoming.
-- +goose StatementBegin
CREATE FUNCTION table_schedule_status(
    cancelled_at TIMESTAMPTZ, start_at TIMESTAMPTZ, end_at TIMESTAMPTZ, at TIMESTAMPTZ
) RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN cancelled_at IS NOT NULL THEN 'cancelled'
        WHEN end_at <= at THEN 'finished'
        WHEN start_at <= at THEN 'ongoing'
        ELSE 'upcoming'
    END
$$;
-- +goose StatementEnd

-- The tables a schedule takes out of service.
CREATE TABLE table_schedule_items (
    schedule_id VARCHAR(32) NOT NULL,
    table_id    VARCHAR(32) NOT NULL,
    outlet_id   VARCHAR(32) NOT NULL,
    PRIMARY KEY (schedule_id, table_id),
    CONSTRAINT table_schedule_items_schedule_fkey FOREIGN KEY (schedule_id, outlet_id)
        REFERENCES table_schedules (schedule_id, outlet_id),
    CONSTRAINT table_schedule_items_table_fkey FOREIGN KEY (table_id, outlet_id) REFERENCES tables (table_id, outlet_id)
);

-- A table's schedules, for its status.
CREATE INDEX table_schedule_items_table_id_idx ON table_schedule_items (table_id);

-- An order's table, which the order module's table kept a place for: its
-- foreign key now, to a table of the same outlet, and its name copied at the
-- sale, as a voucher's code is, so the receipt shows the table it was sold
-- at. Only a dine-in order has one.
ALTER TABLE orders
    ADD COLUMN table_name TEXT,
    ADD CONSTRAINT orders_table_fkey FOREIGN KEY (table_id, outlet_id) REFERENCES tables (table_id, outlet_id),
    ADD CONSTRAINT orders_table_check CHECK (
        (table_id IS NULL) = (table_name IS NULL)
        AND (table_id IS NULL OR type = 'dine_in')
    );

-- A sale's time is when its row is written, after it has locked its table,
-- not when its transaction began: compared with the table's released_at,
-- a sale that waited for a release counts after it.
ALTER TABLE orders ALTER COLUMN created_at SET DEFAULT clock_timestamp();

-- An outlet's sales at its tables since a moment, for their status.
CREATE INDEX orders_outlet_table_idx ON orders (outlet_id, created_at) WHERE table_id IS NOT NULL;

-- +goose Down
DROP INDEX orders_outlet_table_idx;
ALTER TABLE orders ALTER COLUMN created_at SET DEFAULT now();
ALTER TABLE orders
    DROP CONSTRAINT orders_table_check,
    DROP CONSTRAINT orders_table_fkey,
    DROP COLUMN table_name;
DROP TABLE table_schedule_items;
DROP FUNCTION table_schedule_status(TIMESTAMPTZ, TIMESTAMPTZ, TIMESTAMPTZ, TIMESTAMPTZ);
DROP TABLE table_schedules;
DROP TABLE tables;
DROP TABLE table_areas;
