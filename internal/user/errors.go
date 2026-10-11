package user

import (
	"net/http"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Errors of the role and user endpoints. Messages live in the i18n
// catalogs.
var (
	// ErrRoleNotFound is a role that doesn't exist in the outlet, or was
	// deleted.
	ErrRoleNotFound = httpx.NewError(http.StatusNotFound, "role_not_found")
	// ErrRoleNameTaken is a name another role of the outlet has.
	ErrRoleNameTaken = httpx.NewError(http.StatusConflict, "role_name_taken")
	// ErrRoleInUse is deleting a role users still hold; details.users names
	// them.
	ErrRoleInUse = httpx.NewError(http.StatusConflict, "role_in_use")
	// ErrPermissionNotHeld is granting a permission the caller doesn't hold
	// themselves, or managing a user who holds one; details.permissions names
	// them.
	ErrPermissionNotHeld = httpx.NewError(http.StatusForbidden, "permission_not_held")

	// ErrUserNotFound is a user who isn't in the outlet, or was deleted.
	ErrUserNotFound = httpx.NewError(http.StatusNotFound, "user_not_found")
	// ErrUsernameTaken is a username someone has, or had before they were
	// deleted: a username is never used again.
	ErrUsernameTaken = httpx.NewError(http.StatusConflict, "username_taken")
	// ErrOwnAccount is managing one's own account through the staff
	// endpoints; one's own settings go through /me.
	ErrOwnAccount = httpx.NewError(http.StatusForbidden, "own_account")
	// ErrManagerAccount is anyone but the Owner creating, changing or
	// promoting to a Manager account.
	ErrManagerAccount = httpx.NewError(http.StatusForbidden, "manager_account")
	// ErrOwnerManagesManagers is the Owner managing a staff account, which
	// is the outlet's Managers' job; promoting one is the exception.
	ErrOwnerManagesManagers = httpx.NewError(http.StatusForbidden, "owner_manages_managers")
)
