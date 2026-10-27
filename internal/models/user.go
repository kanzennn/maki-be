package models

import "time"

// User is an account of someone who signs in: the one Owner, an outlet's
// Manager, or staff with a custom role.
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

// OwnerInput is what `api create-owner` asks for.
type OwnerInput struct {
	Name, Username, Email, Password string
}

// NewOwner is a checked Owner account with its password hashed, ready to
// insert.
type NewOwner struct {
	UserID, Name, Username, Email, PasswordHash string
}

// Manager is one of an outlet's Managers, as the Owner's outlet list shows
// them.
type Manager struct {
	UserID string
	Name   string
}
