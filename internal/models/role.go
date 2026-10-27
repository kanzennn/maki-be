package models

import "time"

// Role is one of an outlet's custom roles.
type Role struct {
	RoleID      string
	Name        string
	Description *string
	Permissions []string // sorted
	UserCount   int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// RoleUser is a user who holds a role.
type RoleUser struct {
	UserID, Name, Username string
}

// RoleInput is a role as a Manager fills it in.
type RoleInput struct {
	Name        string
	Description *string
	Permissions []string
}

// RoleFields is a role's own fields, checked, as the Log Action stores them.
type RoleFields struct {
	Name        string
	Description *string
	Permissions []string
}

// Snapshot is the fields as the Log Action stores them.
func (r RoleFields) Snapshot() map[string]any {
	return map[string]any{"name": r.Name, "description": r.Description, "permissions": r.Permissions}
}

// RoleResponse is a role as the role endpoints send it.
type RoleResponse struct {
	RoleID      string    `json:"role_id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Permissions []string  `json:"permissions"`
	UserCount   int       `json:"user_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RoleUserResponse is a user who holds the role, on its page.
type RoleUserResponse struct {
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

// RoleDetailResponse is a role with the users who hold it.
type RoleDetailResponse struct {
	RoleResponse
	Users []RoleUserResponse `json:"users"`
}

// NewRoleResponse returns the role's response, its times in UTC.
func NewRoleResponse(r Role) RoleResponse {
	return RoleResponse{
		RoleID: r.RoleID, Name: r.Name, Description: r.Description, Permissions: r.Permissions,
		UserCount: r.UserCount, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}
}

// NewRoleDetailResponse returns the role's response with its users.
func NewRoleDetailResponse(r Role, users []RoleUser) RoleDetailResponse {
	out := RoleDetailResponse{RoleResponse: NewRoleResponse(r), Users: make([]RoleUserResponse, len(users))}
	for i, u := range users {
		out.Users[i] = RoleUserResponse(u)
	}
	return out
}
