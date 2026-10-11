-- name: GetVoucher :one
SELECT v.*, voucher_status(v.is_active, v.start_at, v.expired_at, v.used_count, v.quota, @at)::text AS status
FROM vouchers v
WHERE v.voucher_id = @voucher_id
  AND v.deleted_at IS NULL;

-- name: LockVoucher :one
-- Locks the voucher for a change, so two edits, or an edit and a sale,
-- can't interleave.
SELECT v.*, voucher_status(v.is_active, v.start_at, v.expired_at, v.used_count, v.quota, @at)::text AS status
FROM vouchers v
WHERE v.voucher_id = @voucher_id
  AND v.deleted_at IS NULL
FOR UPDATE;

-- name: FindVoucherByCode :one
SELECT v.*, voucher_status(v.is_active, v.start_at, v.expired_at, v.used_count, v.quota, @at)::text AS status
FROM vouchers v
WHERE v.code = @code
  AND v.deleted_at IS NULL;

-- name: LockVoucherByCode :one
-- Locks the voucher a sale uses, last of the sale's locks: two outlets
-- taking its last use at once take it one after the other.
SELECT v.*, voucher_status(v.is_active, v.start_at, v.expired_at, v.used_count, v.quota, @at)::text AS status
FROM vouchers v
WHERE v.code = @code
  AND v.deleted_at IS NULL
FOR UPDATE;

-- name: VoucherIsForOutlet :one
SELECT EXISTS (SELECT 1 FROM voucher_outlets WHERE voucher_id = @voucher_id AND outlet_id = @outlet_id);

-- name: ListVoucherOutlets :many
-- The outlets of the vouchers that aren't for all of them.
SELECT voucher_id, outlet_id
FROM voucher_outlets
WHERE voucher_id = ANY (@voucher_ids::text[])
ORDER BY voucher_id, outlet_id;

-- name: ListVouchers :many
-- outlet_id narrows them to the ones the outlet's cashier can take: for
-- every outlet, or for it among others. search matches the code or the
-- name, and is already escaped for LIKE. The ones that run out last come
-- first.
SELECT v.*, voucher_status(v.is_active, v.start_at, v.expired_at, v.used_count, v.quota, @at)::text AS status
FROM vouchers v
WHERE v.deleted_at IS NULL
  AND (sqlc.narg(outlet_id)::text IS NULL
    OR v.all_outlets
    OR EXISTS (SELECT 1
               FROM voucher_outlets o
               WHERE o.voucher_id = v.voucher_id
                 AND o.outlet_id = sqlc.narg(outlet_id)::text))
  AND (sqlc.narg(status)::text IS NULL
    OR voucher_status(v.is_active, v.start_at, v.expired_at, v.used_count, v.quota, @at) = sqlc.narg(status)::text)
  AND (sqlc.narg(search)::text IS NULL
    OR v.code ILIKE '%' || sqlc.narg(search)::text || '%'
    OR v.name ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY v.expired_at DESC, v.code
LIMIT @page_size OFFSET @page_offset;

-- name: CountVouchers :one
SELECT count(*)
FROM vouchers v
WHERE v.deleted_at IS NULL
  AND (sqlc.narg(outlet_id)::text IS NULL
    OR v.all_outlets
    OR EXISTS (SELECT 1
               FROM voucher_outlets o
               WHERE o.voucher_id = v.voucher_id
                 AND o.outlet_id = sqlc.narg(outlet_id)::text))
  AND (sqlc.narg(status)::text IS NULL
    OR voucher_status(v.is_active, v.start_at, v.expired_at, v.used_count, v.quota, @at) = sqlc.narg(status)::text)
  AND (sqlc.narg(search)::text IS NULL
    OR v.code ILIKE '%' || sqlc.narg(search)::text || '%'
    OR v.name ILIKE '%' || sqlc.narg(search)::text || '%');

-- name: InsertVoucher :exec
INSERT INTO vouchers (voucher_id, code, name, type, value, max_discount, min_spend, quota, start_at, expired_at,
                      all_outlets, is_active)
VALUES (@voucher_id, @code, sqlc.narg(name), @type, @value, sqlc.narg(max_discount), sqlc.narg(min_spend), @quota,
        sqlc.narg(start_at), @expired_at, @all_outlets, @is_active);

-- name: UpdateVoucher :exec
UPDATE vouchers
SET code         = @code,
    name         = sqlc.narg(name),
    type         = @type,
    value        = @value,
    max_discount = sqlc.narg(max_discount),
    min_spend    = sqlc.narg(min_spend),
    quota        = @quota,
    start_at     = sqlc.narg(start_at),
    expired_at   = @expired_at,
    all_outlets  = @all_outlets,
    updated_at   = now()
WHERE voucher_id = @voucher_id;

-- name: DeleteVoucherOutlets :exec
DELETE FROM voucher_outlets
WHERE voucher_id = @voucher_id;

-- name: InsertVoucherOutlets :exec
INSERT INTO voucher_outlets (voucher_id, outlet_id)
SELECT @voucher_id, unnest(@outlet_ids::text[]);

-- name: SetVoucherActive :exec
UPDATE vouchers
SET is_active  = @is_active,
    updated_at = now()
WHERE voucher_id = @voucher_id;

-- name: DeleteVoucher :exec
UPDATE vouchers
SET deleted_at = now(),
    updated_at = now()
WHERE voucher_id = @voucher_id;

-- name: TakeVoucherUse :exec
-- A sale takes one use; the first one marks the voucher used for good.
UPDATE vouchers
SET used_count    = used_count + 1,
    first_used_at = COALESCE(first_used_at, @at)
WHERE voucher_id = @voucher_id;

-- name: ReturnVoucherUse :exec
-- A void gives its sale's use back. The voucher stays used.
UPDATE vouchers
SET used_count = used_count - 1
WHERE voucher_id = @voucher_id;
