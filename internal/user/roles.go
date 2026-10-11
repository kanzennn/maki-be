package user

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/platform/i18n"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/user/store"
)

// Manager is one of an outlet's Managers, as the Owner's outlet list shows
// them.
type Manager struct {
	UserID string
	Name   string
}

// cashierTemplate is what the role every new outlet starts with holds, so
// its Manager can take on cashiers at once. It is an ordinary role after
// that: the Manager can change or delete it.
var cashierTemplate = []string{"checkout.create"}

// CreateTemplateRole gives a new outlet its cashier role, named in the
// outlet's language, in the transaction that creates the outlet.
func (s *Service) CreateTemplateRole(ctx context.Context, tx pgx.Tx, outletID, locale string) error {
	roleID := id.New()
	q := store.New(tx)
	if err := q.InsertRole(ctx, store.InsertRoleParams{
		RoleID:   roleID,
		OutletID: outletID,
		Name:     i18n.For(locale).T("role.template.cashier", nil),
	}); err != nil {
		return fmt.Errorf("create the cashier role of %s: %w", outletID, err)
	}
	return q.InsertRolePermissions(ctx, store.InsertRolePermissionsParams{RoleID: roleID, PermissionCodes: cashierTemplate})
}

// ManagersOf returns the Managers of each of the outlets, by outlet ID.
func (s *Service) ManagersOf(ctx context.Context, outletIDs []string) (map[string][]Manager, error) {
	rows, err := store.New(s.pool).ListManagersOf(ctx, outletIDs)
	if err != nil {
		return nil, fmt.Errorf("list managers: %w", err)
	}
	out := make(map[string][]Manager, len(outletIDs))
	for _, r := range rows {
		out[deref(r.OutletID)] = append(out[deref(r.OutletID)], Manager{UserID: r.UserID, Name: r.Name})
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
