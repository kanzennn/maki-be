package validations

import (
	"errors"
	"fmt"
	netmail "net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/password"
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

// CheckOwner checks the Owner's details as `api create-owner` was given
// them, and returns them trimmed, the username and email in lower case. Its
// errors are sentences for the command line.
func CheckOwner(in models.OwnerInput) (models.OwnerInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 100:
		return models.OwnerInput{}, errors.New("the name must be 1 to 100 characters")
	case !usernamePattern.MatchString(in.Username):
		return models.OwnerInput{}, errors.New("the username must be 3 to 32 lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit")
	case !ValidEmail(in.Email):
		return models.OwnerInput{}, fmt.Errorf("%q is not an email address", in.Email)
	}
	if err := password.Check(in.Password, in.Username); err != nil {
		return models.OwnerInput{}, err
	}
	return in, nil
}

// trimmed is s without surrounding space, or nil when that leaves nothing.
func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
