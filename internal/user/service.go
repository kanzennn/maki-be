package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/platform/password"
	"github.com/kervzent/maki-be/internal/user/store"
)

// Auditor records Log Action entries inside a transaction; audit.Recorder
// is the real one.
type Auditor interface {
	Record(ctx context.Context, tx pgx.Tx, e audit.Entry) error
}

// Sessions ends a user's sessions in a transaction; auth.Sessions is the
// real one. The sessions belong to the auth module.
type Sessions interface {
	EndAll(ctx context.Context, tx pgx.Tx, userID string) error
}

// Service reads and changes accounts and roles. Methods that take a pgx.Tx
// write in the caller's transaction, so another module's change and the
// account's commit together.
type Service struct {
	pool     *pgxpool.Pool
	hasher   *password.Hasher
	audit    Auditor
	sessions Sessions
}

// NewService returns the user service. hasher hashes the passwords it sets.
func NewService(pool *pgxpool.Pool, hasher *password.Hasher, auditor Auditor, sessions Sessions) *Service {
	return &Service{pool: pool, hasher: hasher, audit: auditor, sessions: sessions}
}

// ByUsername returns the account signing in with username.
func (s *Service) ByUsername(ctx context.Context, username string) (User, error) {
	return one(store.New(s.pool).GetUserByUsername(ctx, username))
}

// ByID returns the account.
func (s *Service) ByID(ctx context.Context, userID string) (User, error) {
	return one(store.New(s.pool).GetUserByID(ctx, userID))
}

// Names returns the names of the accounts, by user ID, deleted ones
// included: for records that name who made them. An unknown ID is left out.
func (s *Service) Names(ctx context.Context, userIDs []string) (map[string]string, error) {
	rows, err := store.New(s.pool).ListUserNames(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("list user names: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.UserID] = r.Name
	}
	return out, nil
}

// OwnerByLogin returns the Owner, if login is their username or their
// verified email.
func (s *Service) OwnerByLogin(ctx context.Context, login string) (User, error) {
	return one(store.New(s.pool).GetOwnerByLogin(ctx, login))
}

// OwnerExists reports whether the Owner account exists.
func (s *Service) OwnerExists(ctx context.Context) (bool, error) {
	return store.New(s.pool).OwnerExists(ctx)
}

// RecordLogin notes a successful sign-in.
func (s *Service) RecordLogin(ctx context.Context, tx pgx.Tx, userID string) error {
	return store.New(tx).RecordLogin(ctx, userID)
}

// SetPasswordHash replaces the hash of an unchanged password, made with
// stronger parameters.
func (s *Service) SetPasswordHash(ctx context.Context, tx pgx.Tx, userID, hash string) error {
	return store.New(tx).SetPasswordHash(ctx, store.SetPasswordHashParams{UserID: userID, PasswordHash: hash})
}

// ChangePassword stores a password the user chose, which clears
// must_change_password.
func (s *Service) ChangePassword(ctx context.Context, tx pgx.Tx, userID, hash string) error {
	return store.New(tx).ChangePassword(ctx, store.ChangePasswordParams{UserID: userID, PasswordHash: hash})
}

// SetLocale sets the user's own language; nil follows their outlet's.
func (s *Service) SetLocale(ctx context.Context, userID string, locale *string) error {
	return store.New(s.pool).SetLocale(ctx, store.SetLocaleParams{UserID: userID, Locale: locale})
}

// SetVerifiedEmail makes a confirmed address the account's email.
func (s *Service) SetVerifiedEmail(ctx context.Context, tx pgx.Tx, userID, email string) error {
	return store.New(tx).SetVerifiedEmail(ctx, store.SetVerifiedEmailParams{UserID: userID, Email: &email})
}

// OwnerInput is what `api create-owner` asks for.
type OwnerInput struct {
	Name, Username, Email, Password string
}

// NewOwner is a checked Owner account with its password hashed, ready for
// InsertOwner.
type NewOwner struct {
	UserID, Name, Username, Email string

	passwordHash string
}

// PrepareOwner checks the Owner's details and hashes the password. It runs
// outside any transaction: a hash takes a tenth of a second.
func (s *Service) PrepareOwner(ctx context.Context, in OwnerInput) (NewOwner, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 100:
		return NewOwner{}, errors.New("the name must be 1 to 100 characters")
	case !usernamePattern.MatchString(in.Username):
		return NewOwner{}, errors.New("the username must be 3 to 32 lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit")
	case !ValidEmail(in.Email):
		return NewOwner{}, fmt.Errorf("%q is not an email address", in.Email)
	}
	if err := password.Check(in.Password, in.Username); err != nil {
		return NewOwner{}, err
	}
	if exists, err := s.OwnerExists(ctx); err != nil {
		return NewOwner{}, err
	} else if exists {
		return NewOwner{}, ErrOwnerExists
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return NewOwner{}, err
	}
	return NewOwner{UserID: id.New(), Name: in.Name, Username: in.Username, Email: in.Email, passwordHash: hash}, nil
}

// InsertOwner stores the Owner account. The database refuses a second
// Owner, even from a concurrent run.
func (s *Service) InsertOwner(ctx context.Context, tx pgx.Tx, o NewOwner) error {
	err := store.New(tx).InsertOwner(ctx, store.InsertOwnerParams{
		UserID:       o.UserID,
		Name:         o.Name,
		Username:     o.Username,
		Email:        &o.Email,
		PasswordHash: o.passwordHash,
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.ConstraintName {
		case "users_single_owner_key":
			return ErrOwnerExists
		case "users_username_key":
			return fmt.Errorf("the username %q is taken", o.Username)
		}
	}
	return err
}

// one maps a single-row query's result.
func one(r store.User, err error) (User, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	return fromRow(r), nil
}
