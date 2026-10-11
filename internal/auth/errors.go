package auth

import (
	"net/http"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// The auth module's errors. Their messages are the "error.<code>" entries of
// the i18n catalogs.
var (
	// ErrInvalidCredentials is a wrong password — or an unknown username,
	// which gets the same answer so it can't be told apart.
	ErrInvalidCredentials = httpx.NewError(http.StatusUnauthorized, "invalid_credentials")
	// ErrAccountDisabled is the right password for a deactivated account.
	ErrAccountDisabled = httpx.NewError(http.StatusForbidden, "account_disabled")
	// ErrOutletInactive is the right password for an account whose outlet
	// the Owner has deactivated or deleted.
	ErrOutletInactive = httpx.NewError(http.StatusForbidden, "outlet_inactive")
	// ErrUnauthorized is a refresh token that is missing, unknown, expired,
	// revoked, or already replaced.
	ErrUnauthorized = httpx.NewError(http.StatusUnauthorized, "unauthorized")
	// ErrRefreshSuperseded is a refresh token replaced a moment ago by
	// another request, such as a second browser tab; retrying works.
	ErrRefreshSuperseded = httpx.NewError(http.StatusConflict, "refresh_superseded")
	// ErrWrongPassword is a wrong current password when changing the
	// password or the email address.
	ErrWrongPassword = httpx.NewError(http.StatusUnprocessableEntity, "wrong_password")
	// ErrTokenInvalid is an emailed link's token that is unknown, used or
	// expired.
	ErrTokenInvalid = httpx.NewError(http.StatusBadRequest, "token_invalid")
	// ErrEmailUnchanged is a request to change the email to the one the
	// account already has.
	ErrEmailUnchanged = httpx.NewError(http.StatusUnprocessableEntity, "email_unchanged")
)
