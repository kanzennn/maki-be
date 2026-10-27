package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// OrderService reads an outlet's orders and voids them. Every method takes
// the outlet it acts in; the routes have checked the caller may act there.
type OrderService struct {
	db       *config.Database
	orders   *repositories.OrderRepository
	menus    *MenuService
	outlets  *OutletService
	users    *UserService
	vouchers *VoucherService
	audit    *AuditService
	now      func() time.Time
}

// NewOrderService returns the orders' service. users names who took and
// voided each order; a void puts what it sold back through menus and gives
// its voucher's use back through vouchers; now is the clock that says which
// day is today.
func NewOrderService(db *config.Database, orders *repositories.OrderRepository, menus *MenuService, outlets *OutletService,
	users *UserService, vouchers *VoucherService, audit *AuditService, now func() time.Time,
) *OrderService {
	return &OrderService{
		db: db, orders: orders, menus: menus, outlets: outlets, users: users, vouchers: vouchers, audit: audit, now: now,
	}
}

// Order returns one of the outlet's orders, with its lines and its payment.
func (s *OrderService) Order(ctx context.Context, outletID, orderID string) (models.Order, error) {
	o, err := s.orders.Get(ctx, outletID, orderID)
	if err != nil {
		return models.Order{}, err
	}
	people := []string{o.Cashier.UserID}
	if o.VoidedBy != nil {
		people = append(people, o.VoidedBy.UserID)
	}
	names, err := s.users.Names(ctx, people)
	if err != nil {
		return models.Order{}, err
	}
	o.Cashier.Name = names[o.Cashier.UserID]
	if o.VoidedBy != nil {
		o.VoidedBy.Name = names[o.VoidedBy.UserID]
	}
	return o, nil
}

// List returns a page of the outlet's orders of today, in its time zone,
// newest first, and how many there are. search narrows them to the receipt
// numbers holding it, in any case.
func (s *OrderService) List(ctx context.Context, outletID, search string, page pagination.Page) ([]models.OrderSummary, int, error) {
	o, err := s.outlets.Cashier(ctx, outletID)
	if err != nil {
		return nil, 0, err
	}
	start, end, err := outletDay(s.now(), o.Timezone)
	if err != nil {
		return nil, 0, err
	}
	out, total, err := s.orders.List(ctx, outletID, start, end, strings.TrimSpace(search), page)
	if err != nil {
		return nil, 0, err
	}
	cashiers := make([]string, len(out))
	for i, o := range out {
		cashiers[i] = o.Cashier.UserID
	}
	names, err := s.users.Names(ctx, cashiers)
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		out[i].Cashier.Name = names[out[i].Cashier.UserID]
	}
	return out, total, nil
}

// Void cancels a paid order and its payment, for the reason given, puts
// what it sold back in the stock of its menus still tracked, and gives the
// use of its voucher back; the Log Action records order.voided. An order is
// voided once (ErrOrderAlreadyVoid).
func (s *OrderService) Void(ctx context.Context, p models.Principal, ip, outletID, orderID, reason string) (models.Order, error) {
	reason, err := validations.CheckVoidReason(reason)
	if err != nil {
		return models.Order{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		o, err := s.orders.Lock(ctx, outletID, orderID)
		if err != nil {
			return err
		}
		if o.Status == models.OrderVoid {
			return apperror.ErrOrderAlreadyVoid
		}
		if err := s.orders.Void(ctx, outletID, orderID, p.UserID, reason); err != nil {
			return err
		}
		sold, err := s.orders.Sold(ctx, outletID, orderID)
		if err != nil {
			return err
		}
		if err := s.menus.RecordVoid(ctx, p.UserID, outletID, orderID, sold); err != nil {
			return err
		}
		// After the menus, as in a sale.
		if o.VoucherID != nil {
			if err := s.vouchers.Release(ctx, *o.VoucherID); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "order.voided", UserID: p.UserID, OutletID: outletID, TargetType: "order", TargetID: orderID,
			Before: map[string]any{"order_no": o.OrderNo, "total": o.Total, "status": models.OrderPaid},
			After:  map[string]any{"status": models.OrderVoid, "reason": reason},
			IP:     ip,
		})
	})
	if err != nil {
		return models.Order{}, err
	}
	return s.Order(ctx, outletID, orderID)
}

// outletDay returns the outlet's day that t falls in, in its time zone:
// from its midnight to the next.
func outletDay(t time.Time, timezone string) (start, end time.Time, err error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("load time zone %q: %w", timezone, err)
	}
	y, m, d := t.In(loc).Date()
	start = time.Date(y, m, d, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1), nil
}
