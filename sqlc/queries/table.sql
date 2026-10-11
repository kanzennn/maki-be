-- The table module's queries: table_areas, tables, table_schedules,
-- table_schedule_items and dine_in_settings. Every query names the outlet,
-- or a row found through it, so nothing reads across outlets.

-- Areas

-- name: CountAreas :one
SELECT count(*) FROM table_areas
WHERE outlet_id = @outlet_id AND deleted_at IS NULL;

-- name: ListAreas :many
-- In their order, with how many tables each holds.
SELECT a.area_id, a.name, a.sort_order, a.created_at, a.updated_at,
    (SELECT count(*) FROM tables t WHERE t.area_id = a.area_id AND t.deleted_at IS NULL) AS table_count
FROM table_areas a
WHERE a.outlet_id = @outlet_id AND a.deleted_at IS NULL
ORDER BY a.sort_order, lower(a.name), a.area_id
LIMIT @page_size OFFSET @page_offset;

-- name: GetArea :one
SELECT a.area_id, a.name, a.sort_order, a.created_at, a.updated_at,
    (SELECT count(*) FROM tables t WHERE t.area_id = a.area_id AND t.deleted_at IS NULL) AS table_count
FROM table_areas a
WHERE a.area_id = @area_id AND a.outlet_id = @outlet_id AND a.deleted_at IS NULL;

-- name: GetAreaLayout :one
-- The area's canvas: its decorations, and the version an editor saves on.
SELECT area_id, name, decorations, layout_version FROM table_areas
WHERE area_id = @area_id AND outlet_id = @outlet_id AND deleted_at IS NULL;

-- name: LockArea :one
-- For a change to the area, to its layout or to one of its tables, and for
-- a delete: each waits for the others, so no table slips into an area
-- being deleted, and every change to the area moves its layout's version.
SELECT area_id, name, decorations, layout_version FROM table_areas
WHERE area_id = @area_id AND outlet_id = @outlet_id AND deleted_at IS NULL
FOR UPDATE;

-- name: NextAreaOrder :one
-- A new area goes last.
SELECT COALESCE(max(sort_order) + 1, 0)::integer FROM table_areas
WHERE outlet_id = @outlet_id AND deleted_at IS NULL;

-- name: InsertArea :exec
INSERT INTO table_areas (area_id, outlet_id, name, sort_order)
VALUES (@area_id, @outlet_id, @name, @sort_order);

-- name: UpdateArea :exec
UPDATE table_areas SET name = @name, updated_at = now()
WHERE area_id = @area_id;

-- name: CountAreaTables :one
SELECT count(*) FROM tables
WHERE area_id = @area_id AND deleted_at IS NULL;

-- name: DeleteArea :exec
UPDATE table_areas SET deleted_at = now(), updated_at = now()
WHERE area_id = @area_id;

-- name: LockAreaIDs :many
-- Every area of the outlet, locked while they are put in a new order.
SELECT area_id FROM table_areas
WHERE outlet_id = @outlet_id AND deleted_at IS NULL
ORDER BY sort_order, lower(name), area_id
FOR UPDATE;

-- name: SetAreaOrder :exec
-- Puts the outlet's areas in the order of area_ids, from 0.
UPDATE table_areas a SET sort_order = o.pos - 1, updated_at = now()
FROM unnest(@area_ids::text[]) WITH ORDINALITY AS o(area_id, pos)
WHERE a.area_id = o.area_id AND a.outlet_id = @outlet_id AND a.sort_order <> o.pos - 1;

-- name: BumpLayoutVersion :exec
-- One of the area's tables changed: a layout an editor opened before is
-- refused.
UPDATE table_areas SET layout_version = layout_version + 1
WHERE area_id = @area_id;

-- name: SaveDecorations :exec
UPDATE table_areas SET decorations = @decorations, layout_version = layout_version + 1, updated_at = now()
WHERE area_id = @area_id;

-- Tables

-- name: ListTables :many
-- The outlet's tables, or one area's, with their areas; scheduled is
-- whether a schedule has a table out of service at the moment at. The
-- module sorts them by name as people read numbers.
SELECT t.table_id, t.area_id, a.name AS area_name, a.sort_order AS area_sort_order, t.name, t.capacity,
    t.shape, t.x, t.y, t.w, t.h, t.rotation, t.is_active, t.released_at, t.created_at, t.updated_at,
    EXISTS (
        SELECT 1 FROM table_schedule_items i JOIN table_schedules s ON s.schedule_id = i.schedule_id
        WHERE i.table_id = t.table_id
            AND table_schedule_status(s.cancelled_at, s.start_at, s.end_at, @at) = 'ongoing'
    ) AS scheduled
FROM tables t JOIN table_areas a ON a.area_id = t.area_id
WHERE t.outlet_id = @outlet_id AND t.deleted_at IS NULL
    AND (sqlc.narg(area_id)::text IS NULL OR t.area_id = sqlc.narg(area_id));

-- name: GetTable :one
SELECT t.table_id, t.area_id, a.name AS area_name, a.sort_order AS area_sort_order, t.name, t.capacity,
    t.shape, t.x, t.y, t.w, t.h, t.rotation, t.is_active, t.released_at, t.created_at, t.updated_at,
    EXISTS (
        SELECT 1 FROM table_schedule_items i JOIN table_schedules s ON s.schedule_id = i.schedule_id
        WHERE i.table_id = t.table_id
            AND table_schedule_status(s.cancelled_at, s.start_at, s.end_at, @at) = 'ongoing'
    ) AS scheduled
FROM tables t JOIN table_areas a ON a.area_id = t.area_id
WHERE t.table_id = @table_id AND t.outlet_id = @outlet_id AND t.deleted_at IS NULL;

-- name: GetTableArea :one
-- The table's area, read before its lock: a change to a table locks its
-- area first, then the table, the order a layout save takes them in.
SELECT area_id FROM tables
WHERE table_id = @table_id AND outlet_id = @outlet_id AND deleted_at IS NULL;

-- name: LockTable :one
-- For a change, a release or a delete of the table. A sale at it takes it
-- FOR SHARE, so it waits for these and they wait for the sale.
SELECT table_id, area_id, name, capacity, shape, is_active, released_at FROM tables
WHERE table_id = @table_id AND outlet_id = @outlet_id AND deleted_at IS NULL
FOR UPDATE;

-- name: InsertTable :exec
-- A new table isn't placed on its area's layout yet.
INSERT INTO tables (table_id, outlet_id, area_id, name, capacity, shape, is_active)
VALUES (@table_id, @outlet_id, @area_id, @name, @capacity, @shape, @is_active);

-- name: UpdateTable :exec
-- A table that moves to another area leaves its place in the old one's
-- layout.
UPDATE tables SET
    name = @name, capacity = @capacity, shape = @shape,
    x = CASE WHEN area_id = @area_id THEN x END,
    y = CASE WHEN area_id = @area_id THEN y END,
    w = CASE WHEN area_id = @area_id THEN w END,
    h = CASE WHEN area_id = @area_id THEN h END,
    area_id = @area_id, updated_at = now()
WHERE table_id = @table_id;

-- name: SetTableActive :exec
UPDATE tables SET is_active = @is_active, updated_at = now()
WHERE table_id = @table_id;

-- name: DeleteTable :exec
UPDATE tables SET deleted_at = now(), updated_at = now()
WHERE table_id = @table_id;

-- name: LockAreaTables :many
-- Every table of the area, locked in the order of their IDs while its
-- layout is saved.
SELECT table_id, name, shape, x, y, w, h, rotation FROM tables
WHERE area_id = @area_id AND deleted_at IS NULL
ORDER BY table_id
FOR UPDATE;

-- name: PlaceTables :exec
-- Puts each of the tables in its box, with its turn and shape; a table
-- whose place didn't change isn't touched.
-- The arrays are all as long, and zipped.
UPDATE tables t SET x = p.x, y = p.y, w = p.w, h = p.h, rotation = p.rotation, shape = p.shape, updated_at = now()
FROM (
    SELECT unnest(@table_ids::text[]) AS table_id, unnest(@xs::integer[]) AS x, unnest(@ys::integer[]) AS y,
        unnest(@ws::integer[]) AS w, unnest(@hs::integer[]) AS h, unnest(@rotations::integer[]) AS rotation,
        unnest(@shapes::text[]) AS shape
) AS p
WHERE t.table_id = p.table_id AND t.area_id = @area_id
    AND (t.x, t.y, t.w, t.h, t.rotation, t.shape) IS DISTINCT FROM (p.x, p.y, p.w, p.h, p.rotation, p.shape);

-- name: UnplaceTables :exec
-- Takes the area's tables not in placed off its layout.
UPDATE tables SET x = NULL, y = NULL, w = NULL, h = NULL, updated_at = now()
WHERE area_id = @area_id AND deleted_at IS NULL AND x IS NOT NULL AND NOT (table_id = ANY (@placed::text[]));

-- Schedules

-- name: CountSchedules :one
SELECT count(*) FROM table_schedules
WHERE outlet_id = @outlet_id
    AND (sqlc.narg(status)::text IS NULL
        OR table_schedule_status(cancelled_at, start_at, end_at, @at) = sqlc.narg(status));

-- name: ListSchedules :many
-- The latest first, with their statuses at the moment at.
SELECT schedule_id, title, start_at, end_at, created_by, cancelled_at, cancelled_by, created_at, updated_at,
    table_schedule_status(cancelled_at, start_at, end_at, @at)::text AS status
FROM table_schedules
WHERE outlet_id = @outlet_id
    AND (sqlc.narg(status)::text IS NULL
        OR table_schedule_status(cancelled_at, start_at, end_at, @at) = sqlc.narg(status))
ORDER BY start_at DESC, schedule_id
LIMIT @page_size OFFSET @page_offset;

-- name: GetSchedule :one
SELECT schedule_id, title, start_at, end_at, created_by, cancelled_at, cancelled_by, created_at, updated_at,
    table_schedule_status(cancelled_at, start_at, end_at, @at)::text AS status
FROM table_schedules
WHERE schedule_id = @schedule_id AND outlet_id = @outlet_id;

-- name: LockSchedule :one
-- For a change or a cancel; its status at the moment at says whether it
-- may.
SELECT schedule_id, title, start_at, end_at,
    table_schedule_status(cancelled_at, start_at, end_at, @at)::text AS status
FROM table_schedules
WHERE schedule_id = @schedule_id AND outlet_id = @outlet_id
FOR UPDATE;

-- name: ListScheduleTables :many
-- The tables of the schedules, deleted ones too, with their areas.
SELECT i.schedule_id, t.table_id, t.name, (t.deleted_at IS NOT NULL)::boolean AS deleted, a.area_id, a.name AS area_name,
    a.sort_order AS area_sort_order
FROM table_schedule_items i
JOIN tables t ON t.table_id = i.table_id
JOIN table_areas a ON a.area_id = t.area_id
WHERE i.schedule_id = ANY (@schedule_ids::text[]);

-- name: ListLiveTables :many
-- The tables among table_ids that are the outlet's and not deleted.
SELECT table_id, name FROM tables
WHERE outlet_id = @outlet_id AND deleted_at IS NULL AND table_id = ANY (@table_ids::text[]);

-- name: ListOverlappingSchedules :many
-- The outlet's schedules but schedule_id, neither cancelled nor over at the
-- moment at, whose time crosses start_at to end_at at any of table_ids;
-- with those tables' names.
SELECT s.schedule_id, s.title, s.start_at, s.end_at, array_agg(t.name)::text[] AS table_names
FROM table_schedules s
JOIN table_schedule_items i ON i.schedule_id = s.schedule_id
JOIN tables t ON t.table_id = i.table_id
WHERE s.outlet_id = @outlet_id AND s.schedule_id <> @schedule_id AND s.cancelled_at IS NULL AND s.end_at > @at
    AND s.start_at < @end_at AND s.end_at > @start_at AND i.table_id = ANY (@table_ids::text[])
GROUP BY s.schedule_id, s.title, s.start_at, s.end_at
ORDER BY s.start_at, s.schedule_id;

-- name: InsertSchedule :exec
INSERT INTO table_schedules (schedule_id, outlet_id, title, start_at, end_at, created_by)
VALUES (@schedule_id, @outlet_id, @title, @start_at, @end_at, @created_by);

-- name: UpdateSchedule :exec
UPDATE table_schedules SET title = @title, start_at = @start_at, end_at = @end_at, updated_at = now()
WHERE schedule_id = @schedule_id;

-- name: InsertScheduleItems :exec
-- Puts the tables on the schedule; those on it already stay.
INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id)
SELECT @schedule_id, unnest(@table_ids::text[]), @outlet_id
ON CONFLICT DO NOTHING;

-- name: DeleteScheduleItems :exec
-- Takes the tables not in table_ids off the schedule.
DELETE FROM table_schedule_items
WHERE schedule_id = @schedule_id AND NOT (table_id = ANY (@table_ids::text[]));

-- name: CancelSchedule :exec
UPDATE table_schedules SET cancelled_at = now(), cancelled_by = @cancelled_by, updated_at = now()
WHERE schedule_id = @schedule_id;

-- The cashier

-- name: ListFloorAreas :many
-- The outlet's areas, in order, with their decorations.
SELECT area_id, name, decorations FROM table_areas
WHERE outlet_id = @outlet_id AND deleted_at IS NULL
ORDER BY sort_order, lower(name), area_id;

-- name: HoldTable :one
-- A table for a sale: shared with the other sales at it until the sale
-- commits, while a change, a release or a delete of it waits. scheduled is
-- whether a schedule has it out of service at the moment at.
SELECT t.table_id, t.name, t.is_active, t.released_at,
    EXISTS (
        SELECT 1 FROM table_schedule_items i JOIN table_schedules s ON s.schedule_id = i.schedule_id
        WHERE i.table_id = t.table_id
            AND table_schedule_status(s.cancelled_at, s.start_at, s.end_at, @at) = 'ongoing'
    ) AS scheduled
FROM tables t
WHERE t.table_id = @table_id AND t.outlet_id = @outlet_id AND t.deleted_at IS NULL
FOR SHARE OF t;

-- name: ReleaseTable :exec
-- Timed by clock_timestamp() under the table's lock, as a sale is, so the
-- two compare in the order they happened.
UPDATE tables SET released_at = clock_timestamp(), updated_at = now()
WHERE table_id = @table_id;

-- Dine-in mode

-- name: GetDineInMode :one
-- The outlet's dine-in mode: tables, without a row.
SELECT COALESCE((SELECT mode FROM dine_in_settings WHERE outlet_id = @outlet_id), 'table')::text AS mode;

-- name: LockDineInMode :exec
-- One change of the outlet's dine-in mode at a time, once the sales under
-- way, which hold it shared, are done. An advisory lock of its own: an
-- outlet that never chose a mode has no row to lock. Keyed by the schema
-- too, as the rows it guards are, since a lock is the database's. Read the
-- mode after it, in a statement of its own, which sees what a change before
-- it wrote.
SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.dine_in_mode:' || @outlet_id::text, 0));

-- name: HoldDineInMode :exec
-- The outlet's dine-in mode, held for a sale until it commits, shared with
-- the other sales: a change waits for them, and they for a change under
-- way.
SELECT pg_advisory_xact_lock_shared(hashtextextended(current_schema() || '.dine_in_mode:' || @outlet_id::text, 0));

-- name: SetDineInMode :exec
INSERT INTO dine_in_settings (outlet_id, mode, updated_by)
VALUES (@outlet_id, @mode, @updated_by)
ON CONFLICT (outlet_id) DO UPDATE SET mode = EXCLUDED.mode, updated_at = now(), updated_by = EXCLUDED.updated_by;
