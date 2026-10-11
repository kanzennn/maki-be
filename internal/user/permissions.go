package user

import (
	"context"
	"fmt"
	"slices"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/user/store"
)

// PermissionGroup is one module's permissions, in the order a role's
// checklist shows them.
type PermissionGroup struct {
	Module      string
	Permissions []string
}

// catalog is every permission there is, from the spec. The codes are
// stable: roles store them, and routes name them in RequirePermission.
var catalog = []PermissionGroup{
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
// every outlet and manages its Managers (user.manage, which the user rules
// limit to Manager accounts), but doesn't run one: no changes to menus,
// tables or payments, and no checkout.
var ownerPermissions = []string{
	"dashboard.view", "role.view", "user.view", "user.manage", "menu.view", "table.view", "log.view",
}

// Catalog returns every permission, grouped by module.
func Catalog() []PermissionGroup {
	out := make([]PermissionGroup, len(catalog))
	for i, g := range catalog {
		out[i] = PermissionGroup{Module: g.Module, Permissions: slices.Clone(g.Permissions)}
	}
	return out
}

// KnownPermission reports whether code is in the catalog.
func KnownPermission(code string) bool {
	for _, g := range catalog {
		if slices.Contains(g.Permissions, code) {
			return true
		}
	}
	return false
}

// allPermissions is every code in the catalog: what a Manager holds in
// their outlet.
func allPermissions() []string {
	var all []string
	for _, g := range catalog {
		all = append(all, g.Permissions...)
	}
	return all
}

// Permissions returns what u may do. The Owner and Manager roles are fixed
// here in code, so nobody can change or delete them. Staff hold their
// custom role's permissions, less any code the catalog no longer has.
func (s *Service) Permissions(ctx context.Context, u User) (authn.Permissions, error) {
	switch {
	case u.IsOwner:
		return authn.NewPermissions(ownerPermissions...), nil
	case u.IsManager:
		return authn.NewPermissions(allPermissions()...), nil
	}
	codes, err := store.New(s.pool).GetUserPermissions(ctx, u.UserID)
	if err != nil {
		return nil, fmt.Errorf("load permissions of %s: %w", u.UserID, err)
	}
	return authn.NewPermissions(slices.DeleteFunc(codes, func(c string) bool { return !KnownPermission(c) })...), nil
}
