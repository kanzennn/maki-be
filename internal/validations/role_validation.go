package validations

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// RoleRequest is the body of POST and PUT /outlets/{oid}/roles. The caps
// only bound what the server reads; CheckRole applies the real rules.
type RoleRequest struct {
	Name        string   `json:"name" binding:"required,max=200"`
	Description *string  `json:"description" binding:"omitempty,max=800"`
	Permissions []string `json:"permissions" binding:"max=100"`
}

// CheckRole validates a role as filled in: every problem at once as
// validation_failed. Permissions come back sorted, each once.
func CheckRole(in models.RoleInput) (models.RoleFields, error) {
	fields := map[string]string{}
	r := models.RoleFields{Name: strings.TrimSpace(in.Name), Description: trimmed(in.Description)}
	switch n := utf8.RuneCountInString(r.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 50:
		fields["name"] = "max"
	}
	if r.Description != nil && utf8.RuneCountInString(*r.Description) > 200 {
		fields["description"] = "max"
	}
	r.Permissions = slices.Compact(slices.Sorted(slices.Values(in.Permissions)))
	for _, c := range r.Permissions {
		if !models.KnownPermission(c) {
			fields["permissions"] = "oneof"
		}
	}
	if len(fields) > 0 {
		return models.RoleFields{}, apperror.Invalid(fields)
	}
	if r.Permissions == nil {
		r.Permissions = []string{}
	}
	return r, nil
}
