package services

import (
	"context"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
)

// The purposes of the emailed links' tokens.
const (
	purposePasswordReset = "password_reset"
	purposeEmailVerify   = "email_verify"
)

// AuthTokenService is the single-use tokens of the links the worker emails:
// a password reset, an address to confirm. A token is made as its email
// goes out, so a plain token only ever exists in the email itself — never
// in the database, the outbox or a production log. A retried job makes a
// new token; the unsent ones are never seen by anyone and simply expire.
type AuthTokenService struct {
	tokens *repositories.AuthTokenRepository
}

// NewAuthTokenService returns the link tokens' service.
func NewAuthTokenService(tokens *repositories.AuthTokenRepository) *AuthTokenService {
	return &AuthTokenService{tokens: tokens}
}

// NewPasswordReset returns a new token for the user's password reset link.
// It lasts ResetLinkTTL.
func (s *AuthTokenService) NewPasswordReset(ctx context.Context, userID string) (string, error) {
	token, hash := newSecret()
	if err := s.tokens.Insert(ctx, userID, purposePasswordReset, nil, hash, ResetLinkTTL); err != nil {
		return "", err
	}
	return token, nil
}

// NewEmailVerification returns a new token for the link that confirms
// email as the user's address. It lasts VerifyLinkTTL, and replaces any
// older link still waiting, so only the latest address asked for can be
// confirmed.
func (s *AuthTokenService) NewEmailVerification(ctx context.Context, userID, email string) (string, error) {
	if err := s.tokens.UseAll(ctx, userID, purposeEmailVerify); err != nil {
		return "", err
	}
	token, hash := newSecret()
	if err := s.tokens.Insert(ctx, userID, purposeEmailVerify, &email, hash, VerifyLinkTTL); err != nil {
		return "", err
	}
	return token, nil
}

// PendingEmail returns the address the user asked to change to and hasn't
// confirmed yet, or "".
func (s *AuthTokenService) PendingEmail(ctx context.Context, userID string) (string, error) {
	return s.tokens.PendingEmail(ctx, userID)
}

// lock locks the usable token of a link for purpose, for its single use:
// ErrTokenInvalid if the token is unknown, used or expired.
func (s *AuthTokenService) lock(ctx context.Context, token, purpose string) (models.AuthToken, error) {
	return s.tokens.Lock(ctx, hashSecret(token), purpose)
}

// useAll spends every open token of the user for purpose.
func (s *AuthTokenService) useAll(ctx context.Context, userID, purpose string) error {
	return s.tokens.UseAll(ctx, userID, purpose)
}
