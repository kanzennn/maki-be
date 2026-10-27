package validations

import (
	"strings"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// ActivateDeviceRequest is the body of POST /outlets/{oid}/devices. The caps
// only bound what the server reads; CheckDevice applies the real rules.
type ActivateDeviceRequest struct {
	Name     string `json:"name" binding:"required,max=200"`
	Platform string `json:"platform" binding:"required,max=16"`
}

// CheckDevice validates a device's name and platform, every problem at once
// as validation_failed, and returns the name trimmed.
func CheckDevice(name, platform string) (string, error) {
	name = strings.TrimSpace(name)
	fields := map[string]string{}
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		fields["name"] = "required"
	case n > 50:
		fields["name"] = "max"
	}
	if platform != models.PlatformWeb && platform != models.PlatformAndroid {
		fields["platform"] = "oneof"
	}
	if len(fields) > 0 {
		return "", apperror.Invalid(fields)
	}
	return name, nil
}
