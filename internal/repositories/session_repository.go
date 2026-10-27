package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/auth"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// SessionRepository owns refresh_tokens. A session is a family of refresh
// tokens, of which only the latest is current; each is kept only as its
// hash.
type SessionRepository struct {
	db *config.Database
}

// NewSessionRepository returns the sessions' repository.
func NewSessionRepository(db *config.Database) *SessionRepository {
	return &SessionRepository{db: db}
}

// Insert stores a refresh token of the family, which lasts ttl from now.
func (r *SessionRepository) Insert(ctx context.Context, refreshTokenID, userID, familyID, tokenHash string, ttl time.Duration) error {
	return store.New(r.db.Conn(ctx)).InsertRefreshToken(ctx, store.InsertRefreshTokenParams{
		RefreshTokenID: refreshTokenID,
		UserID:         userID,
		FamilyID:       familyID,
		TokenHash:      tokenHash,
		TtlSeconds:     ttl.Seconds(),
	})
}

// LockForRefresh locks the refresh token with the hash for its exchange:
// ErrUnauthorized if there is none. A token replaced less than grace ago is
// RecentlyReplaced.
func (r *SessionRepository) LockForRefresh(ctx context.Context, tokenHash string, grace time.Duration) (models.RefreshToken, error) {
	row, err := store.New(r.db.Conn(ctx)).GetRefreshTokenForUpdate(ctx, store.GetRefreshTokenForUpdateParams{
		TokenHash:    tokenHash,
		GraceSeconds: grace.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.RefreshToken{}, apperror.ErrUnauthorized
	}
	if err != nil {
		return models.RefreshToken{}, err
	}
	return models.RefreshToken{
		RefreshTokenID: row.RefreshTokenID, UserID: row.UserID, FamilyID: row.FamilyID,
		Revoked: row.Revoked, Expired: row.Expired, Replaced: row.Replaced, RecentlyReplaced: row.RecentlyReplaced,
	}, nil
}

// MarkReplaced notes that the token was exchanged for replacedBy.
func (r *SessionRepository) MarkReplaced(ctx context.Context, refreshTokenID, replacedBy string) error {
	return store.New(r.db.Conn(ctx)).MarkRefreshTokenReplaced(ctx, store.MarkRefreshTokenReplacedParams{
		RefreshTokenID: refreshTokenID,
		ReplacedBy:     &replacedBy,
	})
}

// FamilyOf returns the user and the session of the refresh token with the
// hash, whatever its state: ErrUnauthorized if there is none.
func (r *SessionRepository) FamilyOf(ctx context.Context, tokenHash string) (userID, familyID string, err error) {
	row, err := store.New(r.db.Conn(ctx)).GetRefreshTokenFamily(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", apperror.ErrUnauthorized
	}
	if err != nil {
		return "", "", err
	}
	return row.UserID, row.FamilyID, nil
}

// RevokeFamily ends every token of the session, and returns how many were
// still live.
func (r *SessionRepository) RevokeFamily(ctx context.Context, familyID string) (int64, error) {
	return store.New(r.db.Conn(ctx)).RevokeFamily(ctx, familyID)
}

// RevokeUserSessions ends every session of the user but keepFamilyID; an
// empty one keeps none.
func (r *SessionRepository) RevokeUserSessions(ctx context.Context, userID, keepFamilyID string) error {
	_, err := store.New(r.db.Conn(ctx)).RevokeUserSessions(ctx, store.RevokeUserSessionsParams{UserID: userID, KeepFamilyID: keepFamilyID})
	return err
}

// Alive reports whether the user's session still has a current token:
// neither replaced, revoked nor expired.
func (r *SessionRepository) Alive(ctx context.Context, userID, familyID string) (bool, error) {
	return store.New(r.db.Conn(ctx)).SessionAlive(ctx, store.SessionAliveParams{UserID: userID, FamilyID: familyID})
}
