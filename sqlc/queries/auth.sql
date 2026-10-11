-- Sessions (refresh token families)

-- name: InsertRefreshToken :exec
INSERT INTO refresh_tokens (refresh_token_id, user_id, family_id, token_hash, expires_at)
VALUES (@refresh_token_id, @user_id, @family_id, @token_hash,
        now() + interval '1 second' * @ttl_seconds::float8);

-- name: GetRefreshTokenForUpdate :one
-- Locks the token for its refresh. The booleans use the database clock.
SELECT refresh_token_id,
       user_id,
       family_id,
       (revoked_at IS NOT NULL)::bool AS revoked,
       (replaced_by IS NOT NULL)::bool AS replaced,
       COALESCE(replaced_at > now() - interval '1 second' * @grace_seconds::float8, false)::bool AS recently_replaced,
       (expires_at <= now())::bool AS expired
FROM refresh_tokens
WHERE token_hash = @token_hash
FOR UPDATE;

-- name: GetRefreshTokenFamily :one
SELECT user_id, family_id
FROM refresh_tokens
WHERE token_hash = @token_hash;

-- name: MarkRefreshTokenReplaced :exec
UPDATE refresh_tokens
SET replaced_by = @replaced_by,
    replaced_at = now()
WHERE refresh_token_id = @refresh_token_id;

-- name: RevokeFamily :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE family_id = @family_id
  AND revoked_at IS NULL;

-- name: RevokeUserSessions :execrows
-- Ends every session of the user except keep_family_id (empty keeps none).
UPDATE refresh_tokens
SET revoked_at = now()
WHERE user_id = @user_id
  AND revoked_at IS NULL
  AND family_id <> @keep_family_id;

-- name: SessionAlive :one
-- Whether the session still has a current token: neither replaced, revoked
-- nor expired. Asked on every authenticated request.
SELECT EXISTS (SELECT 1
               FROM refresh_tokens
               WHERE family_id = @family_id
                 AND user_id = @user_id
                 AND revoked_at IS NULL
                 AND replaced_by IS NULL
                 AND expires_at > now());

-- Email tokens

-- name: InsertAuthToken :exec
INSERT INTO auth_tokens (auth_token_id, user_id, purpose, email, token_hash, expires_at)
VALUES (@auth_token_id, @user_id, @purpose, sqlc.narg(email), @token_hash,
        now() + interval '1 second' * @ttl_seconds::float8);

-- name: GetAuthTokenForUpdate :one
-- Locks an email token for its single use. Only usable ones match.
SELECT auth_token_id, user_id, email
FROM auth_tokens
WHERE token_hash = @token_hash
  AND purpose = @purpose
  AND used_at IS NULL
  AND expires_at > now()
FOR UPDATE;

-- name: UseAuthTokens :exec
-- Spends every open token of the purpose for the user: the one just used,
-- and any older link still sitting in an inbox.
UPDATE auth_tokens
SET used_at = now()
WHERE user_id = @user_id
  AND purpose = @purpose
  AND used_at IS NULL;

-- name: GetPendingEmail :one
-- The address waiting for confirmation after an email change ('' when there
-- is none).
SELECT COALESCE((SELECT email
                 FROM auth_tokens
                 WHERE user_id = @user_id
                   AND purpose = 'email_verify'
                   AND used_at IS NULL
                   AND expires_at > now()
                 ORDER BY created_at DESC
                 LIMIT 1), '')::text AS pending_email;
