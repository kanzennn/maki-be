-- name: InsertActionLog :exec
-- Records one action. action_logs is append-only, so there is no update or
-- delete query here, and the database refuses them anyway.
INSERT INTO action_logs (
    action_log_id, outlet_id, user_id, device_id, action, target_type, target_id,
    before_json, after_json, ip, occurred_at
) VALUES (
    @action_log_id, sqlc.narg(outlet_id), sqlc.narg(user_id), sqlc.narg(device_id), @action,
    sqlc.narg(target_type), sqlc.narg(target_id), sqlc.narg(before_json), sqlc.narg(after_json),
    sqlc.narg(ip), @occurred_at
);

-- name: CountActionLogs :one
SELECT count(*)
FROM action_logs
WHERE (sqlc.narg(from_at)::timestamptz IS NULL OR occurred_at >= sqlc.narg(from_at))
    AND (sqlc.narg(to_at)::timestamptz IS NULL OR occurred_at < sqlc.narg(to_at))
    AND (sqlc.narg(actions)::text[] IS NULL OR action = ANY (sqlc.narg(actions)))
    AND (sqlc.narg(outlet_ids)::text[] IS NULL OR outlet_id = ANY (sqlc.narg(outlet_ids)))
    AND (sqlc.narg(user_id)::text IS NULL OR user_id = sqlc.narg(user_id));

-- name: ListActionLogs :many
-- Newest first. The ID breaks ties, so a page never repeats an entry.
SELECT action_log_id, outlet_id, user_id, device_id, action, target_type, target_id,
    before_json, after_json, ip, occurred_at, created_at
FROM action_logs
WHERE (sqlc.narg(from_at)::timestamptz IS NULL OR occurred_at >= sqlc.narg(from_at))
    AND (sqlc.narg(to_at)::timestamptz IS NULL OR occurred_at < sqlc.narg(to_at))
    AND (sqlc.narg(actions)::text[] IS NULL OR action = ANY (sqlc.narg(actions)))
    AND (sqlc.narg(outlet_ids)::text[] IS NULL OR outlet_id = ANY (sqlc.narg(outlet_ids)))
    AND (sqlc.narg(user_id)::text IS NULL OR user_id = sqlc.narg(user_id))
ORDER BY occurred_at DESC, action_log_id DESC
LIMIT @page_size OFFSET @page_offset;
