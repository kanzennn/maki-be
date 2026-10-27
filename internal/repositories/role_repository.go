package repositories

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/user"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// RoleRepository owns roles, each outlet's custom roles, and
// role_permissions, what each of them holds.
type RoleRepository struct {
	db *config.Database
}

// NewRoleRepository returns the roles' repository.
func NewRoleRepository(db *config.Database) *RoleRepository {
	return &RoleRepository{db: db}
}

// List returns a page of the outlet's roles, by name, and how many there
// are in all.
func (r *RoleRepository) List(ctx context.Context, outletID string, page pagination.Page) ([]models.Role, int, error) {
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountRoles(ctx, outletID)
	if err != nil {
		return nil, 0, fmt.Errorf("count roles: %w", err)
	}
	rows, err := q.ListRoles(ctx, store.ListRolesParams{
		OutletID:   outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list roles: %w", err)
	}
	out := make([]models.Role, len(rows))
	for i, row := range rows {
		out[i] = roleFrom(store.GetRoleRow(row))
	}
	return out, int(total), nil
}

// Get returns one of the outlet's roles: ErrRoleNotFound if it has no such
// role.
func (r *RoleRepository) Get(ctx context.Context, outletID, roleID string) (models.Role, error) {
	row, err := store.New(r.db.Conn(ctx)).GetRole(ctx, store.GetRoleParams{RoleID: roleID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Role{}, apperror.ErrRoleNotFound
	}
	if err != nil {
		return models.Role{}, fmt.Errorf("load role %s: %w", roleID, err)
	}
	return roleFrom(row), nil
}

// Users returns the users who hold the role, by name.
func (r *RoleRepository) Users(ctx context.Context, roleID string) ([]models.RoleUser, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListRoleUsers(ctx, roleID)
	if err != nil {
		return nil, fmt.Errorf("list users of role %s: %w", roleID, err)
	}
	out := make([]models.RoleUser, len(rows))
	for i, row := range rows {
		out[i] = models.RoleUser(row)
	}
	return out, nil
}

// Lock locks one of the outlet's roles for a change, so an assignment can't
// slip in while it is deleted: ErrRoleNotFound if it has no such role.
func (r *RoleRepository) Lock(ctx context.Context, outletID, roleID string) (models.RoleFields, error) {
	row, err := store.New(r.db.Conn(ctx)).LockRole(ctx, store.LockRoleParams{RoleID: roleID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.RoleFields{}, apperror.ErrRoleNotFound
	}
	if err != nil {
		return models.RoleFields{}, fmt.Errorf("lock role %s: %w", roleID, err)
	}
	return models.RoleFields{Name: row.Name, Description: row.Description, Permissions: nonNil(row.Permissions)}, nil
}

// Insert stores a new role of the outlet with its permissions:
// ErrRoleNameTaken if another of its roles has the name.
func (r *RoleRepository) Insert(ctx context.Context, outletID, roleID string, f models.RoleFields) error {
	q := store.New(r.db.Conn(ctx))
	if err := nameTaken(q.InsertRole(ctx, store.InsertRoleParams{
		RoleID: roleID, OutletID: outletID, Name: f.Name, Description: f.Description,
	})); err != nil {
		return err
	}
	return q.InsertRolePermissions(ctx, store.InsertRolePermissionsParams{RoleID: roleID, PermissionCodes: f.Permissions})
}

// Update replaces the role's fields and permissions: ErrRoleNameTaken if
// another role of its outlet has the name.
func (r *RoleRepository) Update(ctx context.Context, roleID string, f models.RoleFields) error {
	q := store.New(r.db.Conn(ctx))
	if err := nameTaken(q.UpdateRole(ctx, store.UpdateRoleParams{
		RoleID: roleID, Name: f.Name, Description: f.Description,
	})); err != nil {
		return err
	}
	if err := q.DeleteRolePermissions(ctx, roleID); err != nil {
		return err
	}
	return q.InsertRolePermissions(ctx, store.InsertRolePermissionsParams{RoleID: roleID, PermissionCodes: f.Permissions})
}

// Delete soft-deletes the role.
func (r *RoleRepository) Delete(ctx context.Context, roleID string) error {
	return store.New(r.db.Conn(ctx)).DeleteRole(ctx, roleID)
}

// nameTaken turns the name's unique violation into ErrRoleNameTaken.
func nameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "roles_outlet_name_key" {
		return apperror.ErrRoleNameTaken
	}
	return err
}

func roleFrom(row store.GetRoleRow) models.Role {
	return models.Role{
		RoleID: row.RoleID, Name: row.Name, Description: row.Description, Permissions: nonNil(row.Permissions),
		UserCount: int(row.UserCount), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// nonNil keeps an empty list of permissions from reaching JSON as null.
func nonNil(codes []string) []string {
	if codes == nil {
		return []string{}
	}
	return codes
}
