package services

import (
	"context"
	"slices"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/password"
	"github.com/kervzent/maki-be/internal/validations"
)

// UserService is the accounts of everyone who signs in: the one Owner,
// outlet Managers, and staff with a custom role. Signing in checks passwords
// against them and keeps their sessions; the other services name who did
// what through it. Its writes go in the transaction the context carries.
type UserService struct {
	users  *repositories.UserRepository
	hasher *password.Hasher
}

// NewUserService returns the accounts' service. hasher hashes the Owner's
// password.
func NewUserService(users *repositories.UserRepository, hasher *password.Hasher) *UserService {
	return &UserService{users: users, hasher: hasher}
}

// ByUsername returns the account signing in with username.
func (s *UserService) ByUsername(ctx context.Context, username string) (models.User, error) {
	return s.users.ByUsername(ctx, username)
}

// ByID returns the account.
func (s *UserService) ByID(ctx context.Context, userID string) (models.User, error) {
	return s.users.ByID(ctx, userID)
}

// Names returns the names of the accounts, by user ID, deleted ones
// included: for records that name who made them. An unknown ID is left out.
func (s *UserService) Names(ctx context.Context, userIDs []string) (map[string]string, error) {
	return s.users.Names(ctx, userIDs)
}

// OwnerByLogin returns the Owner, if login is their username or their
// verified email.
func (s *UserService) OwnerByLogin(ctx context.Context, login string) (models.User, error) {
	return s.users.OwnerByLogin(ctx, login)
}

// OwnerExists reports whether the Owner account exists.
func (s *UserService) OwnerExists(ctx context.Context) (bool, error) {
	return s.users.OwnerExists(ctx)
}

// ManagersOf returns the Managers of each of the outlets, by outlet ID.
func (s *UserService) ManagersOf(ctx context.Context, outletIDs []string) (map[string][]models.Manager, error) {
	return s.users.ManagersOf(ctx, outletIDs)
}

// RecordLogin notes a successful sign-in.
func (s *UserService) RecordLogin(ctx context.Context, userID string) error {
	return s.users.RecordLogin(ctx, userID)
}

// SetPasswordHash replaces the hash of an unchanged password, made with
// stronger parameters.
func (s *UserService) SetPasswordHash(ctx context.Context, userID, hash string) error {
	return s.users.SetPasswordHash(ctx, userID, hash)
}

// ChangePassword stores a password the user chose, which clears
// must_change_password.
func (s *UserService) ChangePassword(ctx context.Context, userID, hash string) error {
	return s.users.ChangePassword(ctx, userID, hash)
}

// SetLocale sets the user's own language; nil follows their outlet's.
func (s *UserService) SetLocale(ctx context.Context, userID string, locale *string) error {
	return s.users.SetLocale(ctx, userID, locale)
}

// SetVerifiedEmail makes a confirmed address the account's email.
func (s *UserService) SetVerifiedEmail(ctx context.Context, userID, email string) error {
	return s.users.SetVerifiedEmail(ctx, userID, email)
}

// PrepareOwner checks the Owner's details and hashes the password. It runs
// outside any transaction: a hash takes a tenth of a second.
func (s *UserService) PrepareOwner(ctx context.Context, in models.OwnerInput) (models.NewOwner, error) {
	in, err := validations.CheckOwner(in)
	if err != nil {
		return models.NewOwner{}, err
	}
	if exists, err := s.users.OwnerExists(ctx); err != nil {
		return models.NewOwner{}, err
	} else if exists {
		return models.NewOwner{}, apperror.ErrOwnerExists
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return models.NewOwner{}, err
	}
	return models.NewOwner{UserID: id.New(), Name: in.Name, Username: in.Username, Email: in.Email, PasswordHash: hash}, nil
}

// InsertOwner stores the Owner account. The database refuses a second
// Owner, even from a concurrent run.
func (s *UserService) InsertOwner(ctx context.Context, o models.NewOwner) error {
	return s.users.InsertOwner(ctx, o)
}

// Permissions returns what u may do. The Owner and Manager roles are fixed
// here in code, so nobody can change or delete them. Staff hold their
// custom role's permissions, less any code the catalog no longer has.
func (s *UserService) Permissions(ctx context.Context, u models.User) (models.Permissions, error) {
	switch {
	case u.IsOwner:
		return models.NewPermissions(models.OwnerPermissions()...), nil
	case u.IsManager:
		return models.NewPermissions(models.AllPermissions()...), nil
	}
	codes, err := s.users.Permissions(ctx, u.UserID)
	if err != nil {
		return nil, err
	}
	return models.NewPermissions(slices.DeleteFunc(codes, func(c string) bool { return !models.KnownPermission(c) })...), nil
}
