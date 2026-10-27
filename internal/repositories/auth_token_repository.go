package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/auth"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
)

// AuthTokenRepository owns auth_tokens: the single-use tokens of the links
// emailed for a password reset or an address to confirm, each kept only as
// its hash.
type AuthTokenRepository struct {
	db *config.Database
}

// NewAuthTokenRepository returns the link tokens' repository.
func NewAuthTokenRepository(db *config.Database) *AuthTokenRepository {
	return &AuthTokenRepository{db: db}
}

// Insert stores a token of the user for purpose, which lasts ttl from now.
// email is the address a verification link confirms.
func (r *AuthTokenRepository) Insert(ctx context.Context, userID, purpose string, email *string, tokenHash string, ttl time.Duration) error {
	err := store.New(r.db.Conn(ctx)).InsertAuthToken(ctx, store.InsertAuthTokenParams{
		AuthTokenID: id.New(),
		UserID:      userID,
		Purpose:     purpose,
		Email:       email,
		TokenHash:   tokenHash,
		TtlSeconds:  ttl.Seconds(),
	})
	if err != nil {
		return fmt.Errorf("store %s token: %w", purpose, err)
	}
	return nil
}

// Lock locks the usable token for purpose with the hash, for its single
// use: ErrTokenInvalid if there is none.
func (r *AuthTokenRepository) Lock(ctx context.Context, tokenHash, purpose string) (models.AuthToken, error) {
	row, err := store.New(r.db.Conn(ctx)).GetAuthTokenForUpdate(ctx, store.GetAuthTokenForUpdateParams{
		TokenHash: tokenHash, Purpose: purpose,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AuthToken{}, apperror.ErrTokenInvalid
	}
	if err != nil {
		return models.AuthToken{}, fmt.Errorf("find %s token: %w", purpose, err)
	}
	return models.AuthToken(row), nil
}

// UseAll spends every open token of the user for purpose: the one just
// used, and any older link still sitting in an inbox.
func (r *AuthTokenRepository) UseAll(ctx context.Context, userID, purpose string) error {
	return store.New(r.db.Conn(ctx)).UseAuthTokens(ctx, store.UseAuthTokensParams{UserID: userID, Purpose: purpose})
}

// PendingEmail returns the address the user asked to change to and hasn't
// confirmed yet, or "".
func (r *AuthTokenRepository) PendingEmail(ctx context.Context, userID string) (string, error) {
	email, err := store.New(r.db.Conn(ctx)).GetPendingEmail(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("load pending email of %s: %w", userID, err)
	}
	return email, nil
}
