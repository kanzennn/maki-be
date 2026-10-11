package auth

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/outbox"
	"github.com/kervzent/maki-be/internal/user"
)

// ErrOwnerExists means the Owner account exists already: there is only ever
// one.
var ErrOwnerExists = user.ErrOwnerExists

// OwnerInput is what `api create-owner` asks for.
type OwnerInput = user.OwnerInput

// OwnerExists reports whether the Owner account exists.
func (s *Service) OwnerExists(ctx context.Context) (bool, error) {
	return s.users.OwnerExists(ctx)
}

// CreateOwner creates the one Owner account, records it in the Log Action,
// and emails a link to confirm its address — which password reset needs. It
// returns the Owner's user ID. The database refuses a second Owner, even
// from a concurrent run.
func (s *Service) CreateOwner(ctx context.Context, in OwnerInput) (string, error) {
	owner, err := s.users.PrepareOwner(ctx, in)
	if err != nil {
		return "", err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.users.InsertOwner(ctx, tx, owner); err != nil {
			return err
		}
		if _, err := outbox.Enqueue(ctx, tx, JobSendEmailVerification, jobEmail{UserID: owner.UserID, Email: owner.Email}); err != nil {
			return err
		}
		// Run on the server, so there is no client address.
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "auth.owner_created", UserID: owner.UserID, TargetType: "user", TargetID: owner.UserID,
			After: map[string]any{"username": owner.Username, "email": owner.Email},
		})
	})
	if err != nil {
		return "", err
	}
	return owner.UserID, nil
}
