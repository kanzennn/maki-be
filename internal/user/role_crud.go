package user

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/user/store"
)

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

// ListRoles returns a page of the outlet's roles, by name, and how many
// there are in all.
func (s *Service) ListRoles(ctx context.Context, outletID string, page httpx.Page) ([]Role, int, error) {
	q := store.New(s.pool)
	total, err := q.CountRoles(ctx, outletID)
	if err != nil {
		return nil, 0, fmt.Errorf("count roles: %w", err)
	}
	rows, err := q.ListRoles(ctx, store.ListRolesParams{
		OutletID:   outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list roles: %w", err)
	}
	out := make([]Role, len(rows))
	for i, r := range rows {
		out[i] = Role{
			RoleID: r.RoleID, Name: r.Name, Description: r.Description, Permissions: nonNil(r.Permissions),
			UserCount: int(r.UserCount), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
	}
	return out, int(total), nil
}

// Role returns one of the outlet's roles and the users who hold it.
func (s *Service) Role(ctx context.Context, outletID, roleID string) (Role, []RoleUser, error) {
	q := store.New(s.pool)
	r, err := q.GetRole(ctx, store.GetRoleParams{RoleID: roleID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, nil, ErrRoleNotFound
	}
	if err != nil {
		return Role{}, nil, fmt.Errorf("load role %s: %w", roleID, err)
	}
	rows, err := q.ListRoleUsers(ctx, roleID)
	if err != nil {
		return Role{}, nil, fmt.Errorf("list users of role %s: %w", roleID, err)
	}
	users := make([]RoleUser, len(rows))
	for i, u := range rows {
		users[i] = RoleUser{UserID: u.UserID, Name: u.Name, Username: u.Username}
	}
	return Role{
		RoleID: r.RoleID, Name: r.Name, Description: r.Description, Permissions: nonNil(r.Permissions),
		UserCount: int(r.UserCount), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, users, nil
}

// CreateRole adds a role to the outlet. It may only hold permissions the
// caller holds.
func (s *Service) CreateRole(ctx context.Context, p authn.Principal, ip, outletID string, in RoleInput) (Role, error) {
	next, err := checkRole(in)
	if err != nil {
		return Role{}, err
	}
	if err := grantable(p, next.Permissions); err != nil {
		return Role{}, err
	}
	roleID := id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := nameTaken(q.InsertRole(ctx, store.InsertRoleParams{
			RoleID: roleID, OutletID: outletID, Name: next.Name, Description: next.Description,
		})); err != nil {
			return err
		}
		if err := q.InsertRolePermissions(ctx, store.InsertRolePermissionsParams{
			RoleID: roleID, PermissionCodes: next.Permissions,
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "role.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "role", TargetID: roleID, After: next.snapshot(), IP: ip,
		})
	})
	if err != nil {
		return Role{}, err
	}
	r, _, err := s.Role(ctx, outletID, roleID)
	return r, err
}

// UpdateRole changes a role. Permissions it adds must be the caller's own;
// it may keep or drop ones the caller lacks. The change applies to the
// role's users from their next request.
func (s *Service) UpdateRole(ctx context.Context, p authn.Principal, ip, outletID, roleID string, in RoleInput) (Role, error) {
	next, err := checkRole(in)
	if err != nil {
		return Role{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockRole(ctx, q, outletID, roleID)
		if err != nil {
			return err
		}
		added := slices.DeleteFunc(slices.Clone(next.Permissions), func(c string) bool {
			return slices.Contains(prev.Permissions, c)
		})
		if err := grantable(p, added); err != nil {
			return err
		}
		before, after := changedFields(prev.snapshot(), next.snapshot())
		if len(after) == 0 {
			return nil
		}
		if err := nameTaken(q.UpdateRole(ctx, store.UpdateRoleParams{
			RoleID: roleID, Name: next.Name, Description: next.Description,
		})); err != nil {
			return err
		}
		if err := q.DeleteRolePermissions(ctx, roleID); err != nil {
			return err
		}
		if err := q.InsertRolePermissions(ctx, store.InsertRolePermissionsParams{
			RoleID: roleID, PermissionCodes: next.Permissions,
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "role.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "role", TargetID: roleID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return Role{}, err
	}
	r, _, err := s.Role(ctx, outletID, roleID)
	return r, err
}

// DeleteRole soft-deletes a role nobody holds any more.
func (s *Service) DeleteRole(ctx context.Context, p authn.Principal, ip, outletID, roleID string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockRole(ctx, q, outletID, roleID)
		if err != nil {
			return err
		}
		holders, err := q.ListRoleUsers(ctx, roleID)
		if err != nil {
			return err
		}
		if len(holders) > 0 {
			users := make([]map[string]string, len(holders))
			for i, u := range holders {
				users[i] = map[string]string{"user_id": u.UserID, "name": u.Name}
			}
			return ErrRoleInUse.WithDetails(map[string]any{"users": users})
		}
		if err := q.DeleteRole(ctx, roleID); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "role.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "role", TargetID: roleID, Before: prev.snapshot(), IP: ip,
		})
	})
}

// roleFields is a role's own fields, checked, as the Log Action stores them.
type roleFields struct {
	Name        string
	Description *string
	Permissions []string
}

func (r roleFields) snapshot() map[string]any {
	return map[string]any{"name": r.Name, "description": r.Description, "permissions": r.Permissions}
}

// lockRole locks one of the outlet's roles for a change, so an assignment
// can't slip in while it is deleted.
func lockRole(ctx context.Context, q *store.Queries, outletID, roleID string) (roleFields, error) {
	r, err := q.LockRole(ctx, store.LockRoleParams{RoleID: roleID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return roleFields{}, ErrRoleNotFound
	}
	if err != nil {
		return roleFields{}, fmt.Errorf("lock role %s: %w", roleID, err)
	}
	return roleFields{Name: r.Name, Description: r.Description, Permissions: nonNil(r.Permissions)}, nil
}

// nonNil keeps an empty list of permissions from reaching JSON as null.
func nonNil(codes []string) []string {
	if codes == nil {
		return []string{}
	}
	return codes
}

// checkRole validates a role as filled in: every problem at once as
// validation_failed. Permissions come back sorted, each once.
func checkRole(in RoleInput) (roleFields, error) {
	fields := map[string]string{}
	r := roleFields{Name: strings.TrimSpace(in.Name), Description: trimmed(in.Description)}
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
		if !KnownPermission(c) {
			fields["permissions"] = "oneof"
		}
	}
	if len(fields) > 0 {
		return roleFields{}, httpx.Invalid(fields)
	}
	if r.Permissions == nil {
		r.Permissions = []string{}
	}
	return r, nil
}

// grantable refuses the codes the caller doesn't hold: nobody hands out
// more than they have.
func grantable(p authn.Principal, codes []string) error {
	var missing []string
	for _, c := range codes {
		if !p.Permissions.Has(c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return ErrPermissionNotHeld.WithDetails(map[string]any{"permissions": missing})
	}
	return nil
}

// nameTaken turns the name's unique violation into ErrRoleNameTaken.
func nameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "roles_outlet_name_key" {
		return ErrRoleNameTaken
	}
	return err
}

// changedFields returns the fields that differ, before and after.
func changedFields(prev, next map[string]any) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	for k, v := range next {
		if !reflect.DeepEqual(derefAny(prev[k]), derefAny(v)) {
			before[k], after[k] = prev[k], v
		}
	}
	return before, after
}

// derefAny shows a *string as its value, so two pointers to the same text
// compare equal.
func derefAny(v any) any {
	if s, ok := v.(*string); ok {
		if s == nil {
			return nil
		}
		return *s
	}
	return v
}

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
