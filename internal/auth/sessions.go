package auth

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/auth/store"
)

// Sessions ends a user's sessions for other modules: the user module, when
// an account is deactivated, deleted, promoted or demoted, or its password
// reset by someone else. Like audit.Recorder it holds nothing and writes in
// the caller's transaction.
type Sessions struct{}

// EndAll revokes every session of the user. Their next request is refused,
// and so is their next refresh.
func (Sessions) EndAll(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := store.New(tx).RevokeUserSessions(ctx, store.RevokeUserSessionsParams{UserID: userID, KeepFamilyID: ""})
	return err
}
