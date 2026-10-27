package validations

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// CategoryRequest is the body of POST and PUT on a category. The caps only
// bound what the server reads; CheckCategory applies the real rules.
type CategoryRequest struct {
	Name string  `json:"name" binding:"required,max=400"`
	Icon *string `json:"icon" binding:"omitempty,max=64"`
}

// Input converts the request, whose fields models.CategoryInput repeats
// without JSON tags.
func (r CategoryRequest) Input() models.CategoryInput {
	return models.CategoryInput(r)
}

// ActiveRequest turns a category or a menu on or off at the cashier: the
// body of PATCH …/status.
type ActiveRequest struct {
	IsActive *bool `json:"is_active" binding:"required"`
}

// ReorderCategoriesRequest is the body of PUT /outlets/{oid}/categories/order.
type ReorderCategoriesRequest struct {
	CategoryIDs []string `json:"category_ids" binding:"required,max=1000,dive,max=32"`
}

// CheckCategory validates a category as filled in: every problem at once as
// validation_failed.
func CheckCategory(in models.CategoryInput) (models.CategoryFields, error) {
	fields := map[string]string{}
	c := models.CategoryFields{Name: strings.TrimSpace(in.Name), Icon: trimmed(in.Icon)}
	switch n := utf8.RuneCountInString(c.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 50:
		fields["name"] = "max"
	}
	if c.Icon != nil {
		switch {
		case utf8.RuneCountInString(*c.Icon) > 8:
			fields["icon"] = "max"
		case !emojiLike(*c.Icon):
			fields["icon"] = "format"
		}
	}
	if len(fields) > 0 {
		return models.CategoryFields{}, apperror.Invalid(fields)
	}
	return c, nil
}

// emojiLike reports whether s is a pictograph rather than a word: at least
// one pictographic symbol, or a keycap as in 1️⃣, and no letters, spaces or
// control characters. Emoji are such symbols, joined by zero-width joiners
// and variation selectors; "??" or "12" isn't one.
func emojiLike(s string) bool {
	pictograph := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsSpace(r) || unicode.IsControl(r):
			return false
		case unicode.Is(unicode.So, r) || r == '⃣':
			pictograph = true
		}
	}
	return pictograph
}
