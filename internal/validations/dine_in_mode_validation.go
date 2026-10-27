package validations

import (
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// DineInModeRequest is the body of PUT /outlets/{oid}/dine-in-mode.
type DineInModeRequest struct {
	Mode string `json:"mode" binding:"required,max=16"`
}

// CheckDineInMode validates a dine-in mode: one of the modes.
func CheckDineInMode(mode models.DineInMode) error {
	if !mode.Valid() {
		return apperror.Invalid(map[string]string{"mode": "oneof"})
	}
	return nil
}
