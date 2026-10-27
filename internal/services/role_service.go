package services

import (
	"context"
	"fmt"
	"slices"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/i18n"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// RoleService is each outlet's custom roles: the permissions its staff
// hold. The Owner and Manager roles are fixed in code (UserService's
// Permissions), so nobody can change or delete them.
type RoleService struct {
	db    *config.Database
	roles *repositories.RoleRepository
	audit *AuditService
}

// NewRoleService returns the roles' service.
func NewRoleService(db *config.Database, roles *repositories.RoleRepository, audit *AuditService) *RoleService {
	return &RoleService{db: db, roles: roles, audit: audit}
}

// Catalog returns every permission, grouped by module.
func (s *RoleService) Catalog() []models.PermissionGroup {
	return models.PermissionCatalog()
}

// ListRoles returns a page of the outlet's roles, by name, and how many
// there are in all.
func (s *RoleService) ListRoles(ctx context.Context, outletID string, page pagination.Page) ([]models.Role, int, error) {
	return s.roles.List(ctx, outletID, page)
}

// Role returns one of the outlet's roles and the users who hold it.
func (s *RoleService) Role(ctx context.Context, outletID, roleID string) (models.Role, []models.RoleUser, error) {
	r, err := s.roles.Get(ctx, outletID, roleID)
	if err != nil {
		return models.Role{}, nil, err
	}
	users, err := s.roles.Users(ctx, roleID)
	if err != nil {
		return models.Role{}, nil, err
	}
	return r, users, nil
}

// CreateRole adds a role to the outlet. It may only hold permissions the
// caller holds.
func (s *RoleService) CreateRole(ctx context.Context, p models.Principal, ip, outletID string, in models.RoleInput) (models.Role, error) {
	next, err := validations.CheckRole(in)
	if err != nil {
		return models.Role{}, err
	}
	if err := grantable(p, next.Permissions); err != nil {
		return models.Role{}, err
	}
	roleID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.roles.Insert(ctx, outletID, roleID, next); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "role.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "role", TargetID: roleID, After: next.Snapshot(), IP: ip,
		})
	})
	if err != nil {
		return models.Role{}, err
	}
	return s.roles.Get(ctx, outletID, roleID)
}

// UpdateRole changes a role. Permissions it adds must be the caller's own;
// it may keep or drop ones the caller lacks. The change applies to the
// role's users from their next request.
func (s *RoleService) UpdateRole(ctx context.Context, p models.Principal, ip, outletID, roleID string, in models.RoleInput) (models.Role, error) {
	next, err := validations.CheckRole(in)
	if err != nil {
		return models.Role{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.roles.Lock(ctx, outletID, roleID)
		if err != nil {
			return err
		}
		added := slices.DeleteFunc(slices.Clone(next.Permissions), func(c string) bool {
			return slices.Contains(prev.Permissions, c)
		})
		if err := grantable(p, added); err != nil {
			return err
		}
		before, after := changedFields(prev.Snapshot(), next.Snapshot())
		if len(after) == 0 {
			return nil
		}
		if err := s.roles.Update(ctx, roleID, next); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "role.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "role", TargetID: roleID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Role{}, err
	}
	return s.roles.Get(ctx, outletID, roleID)
}

// DeleteRole soft-deletes a role nobody holds any more.
func (s *RoleService) DeleteRole(ctx context.Context, p models.Principal, ip, outletID, roleID string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.roles.Lock(ctx, outletID, roleID)
		if err != nil {
			return err
		}
		holders, err := s.roles.Users(ctx, roleID)
		if err != nil {
			return err
		}
		if len(holders) > 0 {
			users := make([]map[string]string, len(holders))
			for i, u := range holders {
				users[i] = map[string]string{"user_id": u.UserID, "name": u.Name}
			}
			return apperror.ErrRoleInUse.WithDetails(map[string]any{"users": users})
		}
		if err := s.roles.Delete(ctx, roleID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "role.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "role", TargetID: roleID, Before: prev.Snapshot(), IP: ip,
		})
	})
}

// cashierTemplate is what the role every new outlet starts with holds, so
// its Manager can take on cashiers at once. It is an ordinary role after
// that: the Manager can change or delete it.
var cashierTemplate = []string{"checkout.create"}

// CreateTemplateRole gives a new outlet its cashier role, named in the
// outlet's language, in the transaction that creates the outlet.
func (s *RoleService) CreateTemplateRole(ctx context.Context, outletID, locale string) error {
	err := s.roles.Insert(ctx, outletID, id.New(), models.RoleFields{
		Name:        i18n.For(locale).T("role.template.cashier", nil),
		Permissions: cashierTemplate,
	})
	if err != nil {
		return fmt.Errorf("create the cashier role of %s: %w", outletID, err)
	}
	return nil
}

// grantable refuses the codes the caller doesn't hold: nobody hands out
// more than they have.
func grantable(p models.Principal, codes []string) error {
	var missing []string
	for _, c := range codes {
		if !p.Permissions.Has(c) {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return apperror.ErrPermissionNotHeld.WithDetails(map[string]any{"permissions": missing})
	}
	return nil
}
