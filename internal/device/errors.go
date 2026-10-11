package device

import (
	"net/http"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Errors of the device endpoints, and of the cashier's, which check the
// device each request comes from. Messages live in the i18n catalogs.
var (
	// ErrDeviceNotFound is a device that isn't the outlet's.
	ErrDeviceNotFound = httpx.NewError(http.StatusNotFound, "device_not_found")
	// ErrDeviceNotRegistered is a request from a device the outlet hasn't
	// activated: without a device token, with an unknown one, or with
	// another outlet's.
	ErrDeviceNotRegistered = httpx.NewError(http.StatusForbidden, "device_not_registered")
	// ErrDeviceRevoked is a request from a device that was revoked.
	ErrDeviceRevoked = httpx.NewError(http.StatusForbidden, "device_revoked")
	// ErrDeviceActive is activating a device that is one of the outlet's
	// already, and in use.
	ErrDeviceActive = httpx.NewError(http.StatusConflict, "device_already_active")
)
