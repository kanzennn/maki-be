-- name: GetUserByUsername :one
-- Deleted users can't sign in, but their username stays taken.
SELECT *
FROM users
WHERE username = @username
  AND deleted_at IS NULL;

-- name: GetUserByID :one
SELECT *
FROM users
WHERE user_id = @user_id
  AND deleted_at IS NULL;

-- name: ListUserNames :many
-- For records that name who made them, such as the stock history. A deleted
-- account keeps its name there.
SELECT user_id, name
FROM users
WHERE user_id = ANY (@user_ids::text[]);

-- name: GetOwnerByLogin :one
-- The Owner, by username or by verified email, for a password reset.
SELECT *
FROM users
WHERE is_owner
  AND (username = @login OR (email = @login AND email_verified_at IS NOT NULL));

-- name: OwnerExists :one
SELECT EXISTS (SELECT 1 FROM users WHERE is_owner);

-- name: InsertOwner :exec
INSERT INTO users (user_id, name, username, email, password_hash, is_owner)
VALUES (@user_id, @name, @username, @email, @password_hash, true);

-- name: RecordLogin :exec
UPDATE users
SET last_login_at = now()
WHERE user_id = @user_id;

-- name: SetPasswordHash :exec
-- A rehash with stronger parameters; nothing else changes.
UPDATE users
SET password_hash = @password_hash,
    updated_at    = now()
WHERE user_id = @user_id;

-- name: ChangePassword :exec
-- A password the user chose, which clears must_change_password.
UPDATE users
SET password_hash        = @password_hash,
    must_change_password = false,
    updated_at           = now()
WHERE user_id = @user_id;

-- name: SetLocale :exec
UPDATE users
SET locale     = sqlc.narg(locale),
    updated_at = now()
WHERE user_id = @user_id;

-- name: SetVerifiedEmail :exec
UPDATE users
SET email             = @email,
    email_verified_at = now(),
    updated_at        = now()
WHERE user_id = @user_id;

-- name: GetUserPermissions :many
-- The permissions of a staff member's custom role. A deleted role gives
-- none.
SELECT DISTINCT rp.permission_code
FROM user_roles ur
         JOIN roles r ON r.role_id = ur.role_id AND r.deleted_at IS NULL
         JOIN role_permissions rp ON rp.role_id = ur.role_id
WHERE ur.user_id = @user_id
ORDER BY rp.permission_code;

-- Roles

-- name: InsertRole :exec
INSERT INTO roles (role_id, outlet_id, name, description)
VALUES (@role_id, @outlet_id, @name, sqlc.narg(description));

-- name: InsertRolePermissions :exec
INSERT INTO role_permissions (role_id, permission_code)
SELECT @role_id, unnest(@permission_codes::text[]);

-- name: ListManagersOf :many
-- The Managers of the outlets, for the Owner's outlet list.
SELECT user_id, outlet_id, name
FROM users
WHERE is_manager
  AND deleted_at IS NULL
  AND outlet_id = ANY (@outlet_ids::text[])
ORDER BY name, user_id;

-- name: ListRoles :many
-- An outlet's roles, by name, each with its permissions and how many users
-- hold it. Deleted users don't count.
SELECT r.role_id,
       r.name,
       r.description,
       r.created_at,
       r.updated_at,
       (SELECT count(*)
        FROM user_roles ur
                 JOIN users u ON u.user_id = ur.user_id AND u.deleted_at IS NULL
        WHERE ur.role_id = r.role_id)::int AS user_count,
       COALESCE((SELECT array_agg(rp.permission_code ORDER BY rp.permission_code)
                 FROM role_permissions rp
                 WHERE rp.role_id = r.role_id), '{}')::text[] AS permissions
FROM roles r
WHERE r.outlet_id = @outlet_id
  AND r.deleted_at IS NULL
ORDER BY lower(r.name), r.role_id
LIMIT @page_size OFFSET @page_offset;

-- name: CountRoles :one
SELECT count(*)
FROM roles
WHERE outlet_id = @outlet_id
  AND deleted_at IS NULL;

-- name: GetRole :one
SELECT r.role_id,
       r.name,
       r.description,
       r.created_at,
       r.updated_at,
       (SELECT count(*)
        FROM user_roles ur
                 JOIN users u ON u.user_id = ur.user_id AND u.deleted_at IS NULL
        WHERE ur.role_id = r.role_id)::int AS user_count,
       COALESCE((SELECT array_agg(rp.permission_code ORDER BY rp.permission_code)
                 FROM role_permissions rp
                 WHERE rp.role_id = r.role_id), '{}')::text[] AS permissions
FROM roles r
WHERE r.role_id = @role_id
  AND r.outlet_id = @outlet_id
  AND r.deleted_at IS NULL;

-- name: LockRole :one
-- Locks the role for a change; its permissions come along.
SELECT r.role_id,
       r.name,
       r.description,
       COALESCE((SELECT array_agg(rp.permission_code ORDER BY rp.permission_code)
                 FROM role_permissions rp
                 WHERE rp.role_id = r.role_id), '{}')::text[] AS permissions
FROM roles r
WHERE r.role_id = @role_id
  AND r.outlet_id = @outlet_id
  AND r.deleted_at IS NULL
FOR UPDATE OF r;

-- name: ListRoleUsers :many
-- The users who hold the role, by name. Deleted users don't.
SELECT u.user_id, u.name, u.username
FROM user_roles ur
         JOIN users u ON u.user_id = ur.user_id
WHERE ur.role_id = @role_id
  AND u.deleted_at IS NULL
ORDER BY lower(u.name), u.user_id;

-- name: UpdateRole :exec
UPDATE roles
SET name        = @name,
    description = sqlc.narg(description),
    updated_at  = now()
WHERE role_id = @role_id;

-- name: DeleteRolePermissions :exec
DELETE
FROM role_permissions
WHERE role_id = @role_id;

-- name: DeleteRole :exec
UPDATE roles
SET deleted_at = now(),
    updated_at = now()
WHERE role_id = @role_id;

-- Staff accounts

-- name: ListOutletUsers :many
-- An outlet's users, by name, each with their role. search is already
-- escaped for LIKE.
SELECT u.user_id,
       u.name,
       u.username,
       u.email,
       u.phone,
       u.is_manager,
       u.status,
       u.must_change_password,
       u.last_login_at,
       u.created_at,
       u.updated_at,
       r.role_id AS role_id,
       r.name    AS role_name
FROM users u
         LEFT JOIN user_roles ur ON ur.user_id = u.user_id
         LEFT JOIN roles r ON r.role_id = ur.role_id AND r.deleted_at IS NULL
WHERE u.outlet_id = @outlet_id::text
  AND u.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR u.status = sqlc.narg(status)::text)
  AND (sqlc.narg(role_id)::text IS NULL OR r.role_id = sqlc.narg(role_id)::text)
  AND (sqlc.narg(search)::text IS NULL
    OR u.name ILIKE '%' || sqlc.narg(search)::text || '%'
    OR u.username ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY lower(u.name), u.user_id
LIMIT @page_size OFFSET @page_offset;

-- name: CountOutletUsers :one
SELECT count(*)
FROM users u
         LEFT JOIN user_roles ur ON ur.user_id = u.user_id
         LEFT JOIN roles r ON r.role_id = ur.role_id AND r.deleted_at IS NULL
WHERE u.outlet_id = @outlet_id::text
  AND u.deleted_at IS NULL
  AND (sqlc.narg(status)::text IS NULL OR u.status = sqlc.narg(status)::text)
  AND (sqlc.narg(role_id)::text IS NULL OR r.role_id = sqlc.narg(role_id)::text)
  AND (sqlc.narg(search)::text IS NULL
    OR u.name ILIKE '%' || sqlc.narg(search)::text || '%'
    OR u.username ILIKE '%' || sqlc.narg(search)::text || '%');

-- name: GetOutletUser :one
SELECT u.user_id,
       u.name,
       u.username,
       u.email,
       u.phone,
       u.is_manager,
       u.status,
       u.must_change_password,
       u.last_login_at,
       u.created_at,
       u.updated_at,
       r.role_id AS role_id,
       r.name    AS role_name
FROM users u
         LEFT JOIN user_roles ur ON ur.user_id = u.user_id
         LEFT JOIN roles r ON r.role_id = ur.role_id AND r.deleted_at IS NULL
WHERE u.user_id = @user_id
  AND u.outlet_id = @outlet_id::text
  AND u.deleted_at IS NULL;

-- name: LockOutletUser :one
-- Locks one of the outlet's users for a change.
SELECT u.user_id, u.name, u.username, u.email, u.phone, u.is_manager, u.status
FROM users u
WHERE u.user_id = @user_id
  AND u.outlet_id = @outlet_id::text
  AND u.deleted_at IS NULL
FOR UPDATE;

-- name: GetUserRoleID :one
-- The user's custom role, '' when there is none.
SELECT COALESCE((SELECT role_id FROM user_roles WHERE user_id = @user_id), '')::text AS role_id;

-- name: InsertStaff :exec
-- A Manager or staff account someone else made, so the password they typed
-- must be changed at the first sign-in.
INSERT INTO users (user_id, outlet_id, name, username, email, phone, password_hash, must_change_password, is_manager)
VALUES (@user_id, @outlet_id, @name, @username, sqlc.narg(email), sqlc.narg(phone), @password_hash, true, @is_manager);

-- name: UpdateStaffProfile :exec
-- A changed email isn't verified: whoever set it didn't prove the user owns
-- it.
UPDATE users
SET name              = @name,
    email             = sqlc.narg(email),
    email_verified_at = CASE WHEN email IS NOT DISTINCT FROM sqlc.narg(email) THEN email_verified_at END,
    phone             = sqlc.narg(phone),
    updated_at        = now()
WHERE user_id = @user_id;

-- name: SetManager :exec
UPDATE users
SET is_manager = @is_manager,
    updated_at = now()
WHERE user_id = @user_id;

-- name: ClearUserRole :exec
DELETE
FROM user_roles
WHERE user_id = @user_id;

-- name: AssignUserRole :exec
INSERT INTO user_roles (user_id, role_id, outlet_id)
VALUES (@user_id, @role_id, @outlet_id);

-- name: SetUserStatus :exec
UPDATE users
SET status     = @status,
    updated_at = now()
WHERE user_id = @user_id;

-- name: SetTemporaryPassword :exec
-- A password someone else chose for the user: it must be changed at the
-- next sign-in.
UPDATE users
SET password_hash        = @password_hash,
    must_change_password = true,
    updated_at           = now()
WHERE user_id = @user_id;

-- name: DeleteUser :exec
-- The username stays taken: old logs keep meaning the same person.
UPDATE users
SET deleted_at = now(),
    updated_at = now()
WHERE user_id = @user_id;
