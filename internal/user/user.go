// Package user holds the accounts of everyone who signs in: the one Owner,
// outlet Managers, and staff with a custom role. It owns the users table.
// The auth module checks passwords against these accounts and keeps their
// sessions, going through Service like any other module.
package user

import (
	"errors"
	netmail "net/mail"
	"regexp"
	"time"

	"github.com/kervzent/maki-be/internal/user/store"
)

// User is an account, as other modules see it.
type User struct {
	UserID   string
	OutletID *string // nil for the Owner
	Name     string
	Username string
	Email    *string
	// EmailVerifiedAt is set once the address was confirmed.
	EmailVerifiedAt    *time.Time
	Locale             *string // their own choice; nil follows the outlet
	PasswordHash       string  // argon2id, PHC string
	MustChangePassword bool
	IsOwner            bool
	IsManager          bool
	// Active is false for a deactivated account, which can't sign in.
	Active      bool
	LastLoginAt *time.Time
}

// VerifiedEmail returns the account's email once it has been confirmed.
func (u User) VerifiedEmail() (string, bool) {
	if u.Email == nil || u.EmailVerifiedAt == nil {
		return "", false
	}
	return *u.Email, true
}

var (
	// ErrNotFound means there is no such account, or it was deleted.
	ErrNotFound = errors.New("user: not found")

	// ErrOwnerExists means the Owner account exists already: there is only
	// ever one.
	ErrOwnerExists = errors.New("an Owner account already exists; there is only ever one")
)

// usernamePattern is the users_username_check constraint's rule.
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// ValidEmail accepts a bare address such as owner@example.com.
func ValidEmail(email string) bool {
	if len(email) > 254 {
		return false
	}
	addr, err := netmail.ParseAddress(email)
	return err == nil && addr.Address == email
}

func fromRow(r store.User) User {
	return User{
		UserID:             r.UserID,
		OutletID:           r.OutletID,
		Name:               r.Name,
		Username:           r.Username,
		Email:              r.Email,
		EmailVerifiedAt:    r.EmailVerifiedAt,
		Locale:             r.Locale,
		PasswordHash:       r.PasswordHash,
		MustChangePassword: r.MustChangePassword,
		IsOwner:            r.IsOwner,
		IsManager:          r.IsManager,
		Active:             r.Status == "active",
		LastLoginAt:        r.LastLoginAt,
	}
}
