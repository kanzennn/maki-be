-- name: LockOutletDevices :exec
-- One activation at a time in an outlet, so two can't take the same code.
SELECT pg_advisory_xact_lock(hashtextextended('devices:' || @outlet_id::text, 0));

-- name: NextDeviceNumber :one
-- The number in the next code: one past the outlet's highest, revoked
-- devices included, since a code is never used again.
SELECT (COALESCE(max(substring(code FROM 2)::int), 0) + 1)::int AS next
FROM devices
WHERE outlet_id = @outlet_id;

-- name: InsertDevice :exec
INSERT INTO devices (device_id, outlet_id, code, name, platform, token_hash, activated_by, last_seen_at)
VALUES (@device_id, @outlet_id, @code, @name, @platform, @token_hash, @activated_by, now());

-- name: CountDevices :one
SELECT count(*)
FROM devices
WHERE outlet_id = @outlet_id;

-- name: ListDevices :many
-- Those in use first, then the revoked; each by code.
SELECT device_id, code, name, platform, activated_by, last_seen_at, revoked_at, revoked_by, created_at
FROM devices
WHERE outlet_id = @outlet_id
ORDER BY revoked_at IS NOT NULL, code
LIMIT @page_size OFFSET @page_offset;

-- name: GetDevice :one
SELECT device_id, code, name, platform, activated_by, last_seen_at, revoked_at, revoked_by, created_at
FROM devices
WHERE device_id = @device_id AND outlet_id = @outlet_id;

-- name: LockDevice :one
-- For a revocation, which waits for the device's sale in progress.
SELECT device_id, code, name, platform, revoked_at
FROM devices
WHERE device_id = @device_id AND outlet_id = @outlet_id
FOR UPDATE;

-- name: RevokeDevice :exec
UPDATE devices SET revoked_at = now(), revoked_by = @revoked_by
WHERE device_id = @device_id;

-- name: DeviceByToken :one
SELECT device_id, outlet_id, code, name, platform, revoked_at
FROM devices
WHERE token_hash = @token_hash;

-- name: HoldDeviceByToken :one
-- For a sale: held until it commits, so the device can't be revoked
-- halfway through, and its sales go one at a time.
SELECT device_id, outlet_id, code, name, platform, revoked_at
FROM devices
WHERE token_hash = @token_hash
FOR UPDATE;

-- name: TouchDevice :exec
UPDATE devices SET last_seen_at = now()
WHERE device_id = @device_id;
