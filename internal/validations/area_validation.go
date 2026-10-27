package validations

import (
	"strings"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// AreaRequest is the body of POST and PUT on an area. The cap only bounds
// what the server reads; CheckArea applies the real rule.
type AreaRequest struct {
	Name string `json:"name" binding:"required,max=200"`
}

// ReorderAreasRequest is the body of PUT /outlets/{oid}/areas/order: every
// area of the outlet, in its new order.
type ReorderAreasRequest struct {
	AreaIDs []string `json:"area_ids" binding:"required,max=1000,dive,max=64"`
}

// CheckArea validates an area as filled in, and returns its name trimmed.
func CheckArea(in models.AreaInput) (string, error) {
	name := strings.TrimSpace(in.Name)
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		return "", apperror.Invalid(map[string]string{"name": "required"})
	case n > models.MaxAreaName:
		return "", apperror.Invalid(map[string]string{"name": "max"})
	}
	return name, nil
}
