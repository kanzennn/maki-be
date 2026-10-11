-- name: GetOutlet :one
SELECT outlet_id, code, name, timezone, locale, status
FROM outlets
WHERE outlet_id = @outlet_id
  AND deleted_at IS NULL;

-- name: OutletExists :one
SELECT EXISTS (SELECT 1 FROM outlets WHERE outlet_id = @outlet_id AND deleted_at IS NULL);

-- name: GetOutletHours :one
-- When the outlet opens and closes, for the table module.
SELECT timezone, open_time, close_time FROM outlets
WHERE outlet_id = @outlet_id AND deleted_at IS NULL;

-- name: GetOutletDetail :one
SELECT *
FROM outlets
WHERE outlet_id = @outlet_id
  AND deleted_at IS NULL;

-- name: GetOutletForUpdate :one
-- Locks the outlet for a change, so two edits can't interleave.
SELECT *
FROM outlets
WHERE outlet_id = @outlet_id
  AND deleted_at IS NULL
FOR UPDATE;

-- name: HoldOutletForSale :one
-- Holds the outlet (FOR SHARE) while a sale is taken: its code, time zone and
-- rates can't change until the sale commits, and a change under way
-- finishes first.
SELECT *
FROM outlets
WHERE outlet_id = @outlet_id
  AND deleted_at IS NULL
FOR SHARE;

-- name: ListOutletRefs :many
-- Names outlets for other modules, deleted ones too: a voucher's history
-- keeps the outlets it was used in.
SELECT outlet_id, code, name, (deleted_at IS NOT NULL)::boolean AS deleted
FROM outlets
WHERE outlet_id = ANY (@outlet_ids::text[]);

-- name: ListOutlets :many
-- search matches the name or the code, and is already escaped for LIKE.
SELECT *
FROM outlets
WHERE deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(search)::text IS NULL
    OR name ILIKE '%' || sqlc.narg(search)::text || '%'
    OR code ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY lower(name), code
LIMIT @page_size OFFSET @page_offset;

-- name: CountOutlets :one
SELECT count(*)
FROM outlets
WHERE deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(search)::text IS NULL
    OR name ILIKE '%' || sqlc.narg(search)::text || '%'
    OR code ILIKE '%' || sqlc.narg(search)::text || '%');

-- name: InsertOutlet :exec
INSERT INTO outlets (outlet_id, code, name, address, phone, timezone, locale, open_time, close_time, tax_bps, service_bps)
VALUES (@outlet_id, @code, @name, sqlc.narg(address), sqlc.narg(phone), @timezone, @locale,
        sqlc.narg(open_time), sqlc.narg(close_time), @tax_bps, @service_bps);

-- name: UpdateOutlet :exec
UPDATE outlets
SET code        = @code,
    name        = @name,
    address     = sqlc.narg(address),
    phone       = sqlc.narg(phone),
    timezone    = @timezone,
    locale      = @locale,
    open_time   = sqlc.narg(open_time),
    close_time  = sqlc.narg(close_time),
    tax_bps     = @tax_bps,
    service_bps = @service_bps,
    updated_at  = now()
WHERE outlet_id = @outlet_id;

-- name: SetOutletStatus :exec
UPDATE outlets
SET status     = @status,
    updated_at = now()
WHERE outlet_id = @outlet_id;

-- name: DeleteOutlet :exec
UPDATE outlets
SET deleted_at = now(),
    updated_at = now()
WHERE outlet_id = @outlet_id;
