package models

import "time"

// Staff is one of an outlet's Manager or staff accounts.
type Staff struct {
	UserID             string
	Name               string
	Username           string
	Email              *string
	Phone              *string
	IsManager          bool
	Active             bool
	MustChangePassword bool
	// Role is a staff member's custom role; nil for a Manager, or for staff
	// whose role was deleted.
	Role        *StaffRole
	LastLoginAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// StaffRole is the role a staff member holds.
type StaffRole struct {
	RoleID, Name string
}

// StaffFilter narrows the list of an outlet's users.
type StaffFilter struct {
	Search string // part of the name or the username
	Status string // active, inactive, or empty for both
	RoleID string
}

// StaffInput is an account as filled in. Username and Password only count
// when creating it; RoleID is for staff, IsManager for a Manager.
type StaffInput struct {
	Name      string
	Username  string
	Email     *string
	Phone     *string
	Password  string
	RoleID    *string
	IsManager bool
}

// StaffProfile is an account's own fields, checked, as the Log Action
// stores them.
type StaffProfile struct {
	Name     string
	Username string
	Email    *string
	Phone    *string
}

// Snapshot is the fields as the Log Action stores them.
func (p StaffProfile) Snapshot() map[string]any {
	return map[string]any{"name": p.Name, "email": p.Email, "phone": p.Phone}
}

// StaffLock is an account as it is locked for a change.
type StaffLock struct {
	UserID    string
	Name      string
	Username  string
	Email     *string
	Phone     *string
	IsManager bool
	Status    string
}

// NewStaff is an account to insert, its password hashed.
type NewStaff struct {
	UserID, OutletID, Name, Username string
	Email, Phone                     *string
	PasswordHash                     string
	IsManager                        bool
}

// StaffRoleResponse is a staff member's role, as the user endpoints send it.
type StaffRoleResponse struct {
	RoleID string `json:"role_id"`
	Name   string `json:"name"`
}

// StaffResponse is an account as the user endpoints send it.
type StaffResponse struct {
	UserID             string             `json:"user_id"`
	Name               string             `json:"name"`
	Username           string             `json:"username"`
	Email              *string            `json:"email"`
	Phone              *string            `json:"phone"`
	IsManager          bool               `json:"is_manager"`
	Role               *StaffRoleResponse `json:"role"`
	Status             string             `json:"status"`
	MustChangePassword bool               `json:"must_change_password"`
	LastLoginAt        *time.Time         `json:"last_login_at"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// NewStaffResponse returns the account's response, its times in UTC.
func NewStaffResponse(s Staff) StaffResponse {
	out := StaffResponse{
		UserID: s.UserID, Name: s.Name, Username: s.Username, Email: s.Email, Phone: s.Phone,
		IsManager: s.IsManager, Status: "inactive", MustChangePassword: s.MustChangePassword,
		CreatedAt: s.CreatedAt.UTC(), UpdatedAt: s.UpdatedAt.UTC(),
	}
	if s.Active {
		out.Status = "active"
	}
	if s.Role != nil {
		out.Role = &StaffRoleResponse{RoleID: s.Role.RoleID, Name: s.Role.Name}
	}
	if s.LastLoginAt != nil {
		t := s.LastLoginAt.UTC()
		out.LastLoginAt = &t
	}
	return out
}
