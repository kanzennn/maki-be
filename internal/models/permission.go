package models

import "slices"

// PermissionGroup is one module's permissions, in the order a role's
// checklist shows them.
type PermissionGroup struct {
	Module      string
	Permissions []string
}

// permissionCatalog is every permission there is, from the spec. The codes
// are stable: roles store them, and routes name them in RequirePermission.
var permissionCatalog = []PermissionGroup{
	{Module: "dashboard", Permissions: []string{"dashboard.view"}},
	{Module: "role", Permissions: []string{"role.view", "role.manage"}},
	{Module: "user", Permissions: []string{"user.view", "user.manage"}},
	{Module: "menu", Permissions: []string{"menu.view", "menu.manage", "stock.adjust"}},
	{Module: "table", Permissions: []string{"table.view", "table.manage"}},
	{Module: "checkout", Permissions: []string{"checkout.create", "order.void"}},
	{Module: "payment", Permissions: []string{"payment.manage"}},
	{Module: "log", Permissions: []string{"log.view"}},
}

// ownerPermissions follow the spec's access matrix: the Owner looks into
// every outlet and manages its Managers (user.manage, which the staff rules
// limit to Manager accounts), but doesn't run one: no changes to menus,
// tables or payments, and no checkout.
var ownerPermissions = []string{
	"dashboard.view", "role.view", "user.view", "user.manage", "menu.view", "table.view", "log.view",
}

// PermissionCatalog returns every permission, grouped by module.
func PermissionCatalog() []PermissionGroup {
	out := make([]PermissionGroup, len(permissionCatalog))
	for i, g := range permissionCatalog {
		out[i] = PermissionGroup{Module: g.Module, Permissions: slices.Clone(g.Permissions)}
	}
	return out
}

// KnownPermission reports whether code is in the catalog.
func KnownPermission(code string) bool {
	for _, g := range permissionCatalog {
		if slices.Contains(g.Permissions, code) {
			return true
		}
	}
	return false
}

// OwnerPermissions are what the Owner holds, in every outlet.
func OwnerPermissions() []string { return slices.Clone(ownerPermissions) }

// AllPermissions is every code in the catalog: what a Manager holds in their
// outlet.
func AllPermissions() []string {
	var all []string
	for _, g := range permissionCatalog {
		all = append(all, g.Permissions...)
	}
	return all
}

// PermissionGroupResponse is one module's permissions in GET /permissions.
type PermissionGroupResponse struct {
	Module      string   `json:"module"`
	Permissions []string `json:"permissions"`
}

// PermissionsResponse is GET /permissions: every permission, by module.
type PermissionsResponse struct {
	Groups []PermissionGroupResponse `json:"groups"`
}

// NewPermissionsResponse returns the catalog's response.
func NewPermissionsResponse(groups []PermissionGroup) PermissionsResponse {
	out := PermissionsResponse{Groups: make([]PermissionGroupResponse, len(groups))}
	for i, g := range groups {
		out.Groups[i] = PermissionGroupResponse(g)
	}
	return out
}
