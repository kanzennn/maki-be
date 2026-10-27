package validations

import (
	"strings"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// The bodies of the /outlets/{oid}/users endpoints. Their caps only bound
// what the server reads; CheckStaff and password.Check apply the real rules.
type (
	// CreateStaffRequest is the body of POST.
	CreateStaffRequest struct {
		Name      string  `json:"name" binding:"required,max=400"`
		Username  string  `json:"username" binding:"required,max=64"`
		Email     *string `json:"email" binding:"omitempty,max=254"`
		Phone     *string `json:"phone" binding:"omitempty,max=128"`
		Password  string  `json:"password" binding:"required,max=1024"`
		RoleID    *string `json:"role_id" binding:"omitempty,max=32"`
		IsManager bool    `json:"is_manager"`
	}

	// UpdateStaffRequest is the body of PUT.
	UpdateStaffRequest struct {
		Name      string  `json:"name" binding:"required,max=400"`
		Email     *string `json:"email" binding:"omitempty,max=254"`
		Phone     *string `json:"phone" binding:"omitempty,max=128"`
		RoleID    *string `json:"role_id" binding:"omitempty,max=32"`
		IsManager bool    `json:"is_manager"`
	}

	// StaffStatusRequest is the body of PATCH .../status.
	StaffStatusRequest struct {
		Status string `json:"status" binding:"required,oneof=active inactive"`
	}

	// ResetStaffPasswordRequest is the body of POST .../reset-password.
	ResetStaffPasswordRequest struct {
		Password string `json:"password" binding:"required,max=1024"`
	}
)

// CheckStaffFilter checks the list's filters: a status is active or
// inactive. The search comes back trimmed.
func CheckStaffFilter(f models.StaffFilter) (models.StaffFilter, error) {
	if f.Status != "" && f.Status != "active" && f.Status != "inactive" {
		return models.StaffFilter{}, apperror.Invalid(map[string]string{"status": "oneof"})
	}
	f.Search = strings.TrimSpace(f.Search)
	return f, nil
}

// CheckStaff validates an account as filled in, every problem at once as
// validation_failed. creating also checks the username; the password has
// rules of its own, which password.Check applies.
func CheckStaff(in models.StaffInput, creating bool) (models.StaffProfile, error) {
	fields := map[string]string{}
	p := models.StaffProfile{
		Name:     strings.TrimSpace(in.Name),
		Username: strings.ToLower(strings.TrimSpace(in.Username)),
		Email:    trimmed(in.Email),
		Phone:    trimmed(in.Phone),
	}
	switch n := utf8.RuneCountInString(p.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 100:
		fields["name"] = "max"
	}
	if creating && !usernamePattern.MatchString(p.Username) {
		fields["username"] = "format"
	}
	if p.Email != nil {
		lower := strings.ToLower(*p.Email)
		p.Email = &lower
		if !ValidEmail(lower) {
			fields["email"] = "email"
		}
	}
	if p.Phone != nil && utf8.RuneCountInString(*p.Phone) > 32 {
		fields["phone"] = "max"
	}
	if !in.IsManager && (in.RoleID == nil || *in.RoleID == "") {
		fields["role_id"] = "required"
	}
	if len(fields) > 0 {
		return models.StaffProfile{}, apperror.Invalid(fields)
	}
	return p, nil
}
