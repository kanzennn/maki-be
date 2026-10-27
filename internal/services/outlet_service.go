package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/i18n"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// OutletService is the business's outlets, which only the Owner creates and
// changes. The other services read an outlet through it: its language, its
// cashier's receipt header and rates, its opening hours.
type OutletService struct {
	db       *config.Database
	outlets  *repositories.OutletRepository
	users    *UserService
	roles    *RoleService
	payments *PaymentService
	orders   *repositories.OrderRepository
	audit    *AuditService
}

// NewOutletService returns the outlets' service. A new outlet gets its
// cashier role from roles and its payment methods from payments; users
// names each outlet's Managers; orders tells whether an outlet has taken an
// order, after which its code can't change.
func NewOutletService(db *config.Database, outlets *repositories.OutletRepository, users *UserService, roles *RoleService,
	payments *PaymentService, orders *repositories.OrderRepository, audit *AuditService,
) *OutletService {
	return &OutletService{db: db, outlets: outlets, users: users, roles: roles, payments: payments, orders: orders, audit: audit}
}

// Get returns the outlet.
func (s *OutletService) Get(ctx context.Context, outletID string) (models.Outlet, error) {
	return s.outlets.Get(ctx, outletID)
}

// Cashier returns the outlet as its cashier needs it.
func (s *OutletService) Cashier(ctx context.Context, outletID string) (models.OutletCashier, error) {
	return s.outlets.Cashier(ctx, outletID)
}

// HoldForSale is Cashier in a sale's transaction, the one ctx carries. It
// holds the outlet (FOR SHARE) until the sale commits: the code, time zone
// and rates the sale is checked against stay as they are, and a change to
// them waits for it.
func (s *OutletService) HoldForSale(ctx context.Context, outletID string) (models.OutletCashier, error) {
	return s.outlets.HoldForSale(ctx, outletID)
}

// Refs returns the outlets' codes and names by ID, deleted ones too. An ID
// of no outlet is left out.
func (s *OutletService) Refs(ctx context.Context, outletIDs []string) (map[string]models.OutletRef, error) {
	return s.outlets.Refs(ctx, outletIDs)
}

// Hours returns when the outlet opens and closes, in its time zone: a table
// empties itself when the outlet closes.
func (s *OutletService) Hours(ctx context.Context, outletID string) (models.OutletHours, error) {
	return s.outlets.Hours(ctx, outletID)
}

// OutletExists reports whether the outlet exists and isn't deleted, for the
// OutletScope middleware.
func (s *OutletService) OutletExists(ctx context.Context, outletID string) (bool, error) {
	return s.outlets.Exists(ctx, outletID)
}

// List returns a page of outlets, by name, with their Managers, and how
// many match in all.
func (s *OutletService) List(ctx context.Context, f models.OutletFilter, page pagination.Page) ([]models.OutletDetail, int, error) {
	out, total, err := s.outlets.List(ctx, f, page)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]string, len(out))
	for i, d := range out {
		ids[i] = d.OutletID
	}
	managers, err := s.users.ManagersOf(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		out[i].Managers = managers[out[i].OutletID]
	}
	return out, total, nil
}

// Detail returns the outlet with its Managers.
func (s *OutletService) Detail(ctx context.Context, outletID string) (models.OutletDetail, error) {
	d, err := s.outlets.Detail(ctx, outletID)
	if err != nil {
		return models.OutletDetail{}, err
	}
	managers, err := s.users.ManagersOf(ctx, []string{outletID})
	if err != nil {
		return models.OutletDetail{}, err
	}
	d.Managers = managers[outletID]
	return d, nil
}

// Create adds an outlet, with a cashier role for its Manager to start from
// and its payment methods, in one transaction.
func (s *OutletService) Create(ctx context.Context, p models.Principal, ip string, in models.OutletInput) (models.OutletDetail, error) {
	f, err := validations.CheckOutlet(in)
	if err != nil {
		return models.OutletDetail{}, err
	}
	outletID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.outlets.Insert(ctx, outletID, f); err != nil {
			return err
		}
		if err := s.roles.CreateTemplateRole(ctx, outletID, f.Locale); err != nil {
			return err
		}
		if err := s.payments.CreateDefaults(ctx, outletID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "outlet.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "outlet", TargetID: outletID, After: f.Snapshot(), IP: ip,
		})
	})
	if err != nil {
		return models.OutletDetail{}, err
	}
	return s.Detail(ctx, outletID)
}

// Update changes every field of the outlet but its status. The code is
// printed in every receipt number, so it can't change once the outlet has
// taken an order (ErrOutletCodeLocked).
func (s *OutletService) Update(ctx context.Context, p models.Principal, ip, outletID string, in models.OutletInput) (models.OutletDetail, error) {
	next, err := validations.CheckOutlet(in)
	if err != nil {
		return models.OutletDetail{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.outlets.Lock(ctx, outletID)
		if err != nil {
			return err
		}
		next.Status = prev.Status
		before, after := changedAsPrinted(prev.Snapshot(), next.Snapshot())
		if len(after) == 0 {
			return nil
		}
		// The lock above waited for any sale under way, which holds the
		// outlet until it commits, so its order is counted here.
		if next.Code != prev.Code {
			used, err := s.orders.HasOrders(ctx, outletID)
			if err != nil {
				return err
			}
			if used {
				return apperror.ErrOutletCodeLocked
			}
		}
		if err := s.outlets.Update(ctx, outletID, next); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "outlet.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "outlet", TargetID: outletID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.OutletDetail{}, err
	}
	return s.Detail(ctx, outletID)
}

// SetStatus activates or deactivates the outlet. Deactivating it ends its
// users' access at their next request; the Owner can still see it and
// activate it again.
func (s *OutletService) SetStatus(ctx context.Context, p models.Principal, ip, outletID string, active bool) (models.OutletDetail, error) {
	status, action := "inactive", "outlet.deactivated"
	if active {
		status, action = "active", "outlet.activated"
	}
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.outlets.Lock(ctx, outletID)
		if err != nil || prev.Status == status {
			return err
		}
		if err := s.outlets.SetStatus(ctx, outletID, status); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "outlet", TargetID: outletID,
			Before: map[string]any{"status": prev.Status}, After: map[string]any{"status": status}, IP: ip,
		})
	})
	if err != nil {
		return models.OutletDetail{}, err
	}
	return s.Detail(ctx, outletID)
}

// Delete soft-deletes a deactivated outlet, once confirmName repeats its
// name. Its data and its code stay; it is gone from every list.
func (s *OutletService) Delete(ctx context.Context, p models.Principal, ip, outletID, confirmName string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.outlets.Lock(ctx, outletID)
		if err != nil {
			return err
		}
		if prev.Status == "active" {
			return apperror.ErrOutletStillActive
		}
		if strings.TrimSpace(confirmName) != prev.Name {
			return apperror.ErrOutletNameMismatch
		}
		if err := s.outlets.Delete(ctx, outletID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "outlet.deleted", UserID: p.UserID, OutletID: outletID, TargetType: "outlet", TargetID: outletID,
			Before: prev.Snapshot(), IP: ip,
		})
	})
}

// LanguageOf returns the language u sees: their own choice, else their
// outlet's, else Indonesian. The worker writes their emails in it.
func (s *OutletService) LanguageOf(ctx context.Context, u models.User) (string, error) {
	o, _, err := outletOf(ctx, s, u)
	if err != nil {
		return "", err
	}
	return language(u.Locale, o.Locale), nil
}

// outletOf returns the outlet u belongs to, and whether they may use it.
// The Owner, who belongs to none, always may; everyone else only while
// their outlet is active. A deleted outlet counts as closed.
func outletOf(ctx context.Context, outlets *OutletService, u models.User) (models.Outlet, bool, error) {
	if u.OutletID == nil {
		return models.Outlet{}, true, nil
	}
	o, err := outlets.Get(ctx, *u.OutletID)
	if errors.Is(err, apperror.ErrOutletNotFound) {
		return models.Outlet{}, false, nil
	}
	if err != nil {
		return models.Outlet{}, false, fmt.Errorf("find outlet of %s: %w", u.UserID, err)
	}
	return o, o.Active, nil
}

// language is the language a user sees: their own choice, else their
// outlet's, else Indonesian.
func language(own *string, outletLocale string) string {
	for _, l := range []string{deref(own), outletLocale} {
		if l != "" && i18n.Supported(l) {
			return l
		}
	}
	return i18n.Default
}
