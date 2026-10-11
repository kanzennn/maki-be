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
