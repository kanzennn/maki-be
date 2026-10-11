package order

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/order/store"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// MaxVoidReason is the most characters a void's reason has.
const MaxVoidReason = 200

// Order returns one of the outlet's orders, with its lines and its payment.
func (s *Service) Order(ctx context.Context, outletID, orderID string) (Order, error) {
	q := store.New(s.pool)
	r, err := q.GetOrder(ctx, store.GetOrderParams{OrderID: orderID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrOrderNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("load order %s: %w", orderID, err)
	}
	items, err := q.ListOrderItems(ctx, store.ListOrderItemsParams{OrderID: orderID, OutletID: outletID})
	if err != nil {
		return Order{}, fmt.Errorf("load the lines of order %s: %w", orderID, err)
	}
	people := []string{r.CashierID}
	if r.VoidedBy != nil {
		people = append(people, *r.VoidedBy)
	}
	names, err := s.users.Names(ctx, people)
	if err != nil {
		return Order{}, err
	}
	o := Order{
		OrderID: r.OrderID, DeviceID: r.DeviceID, OrderNo: r.OrderNo, Type: r.Type, TableID: r.TableID,
		TableName: r.TableName, StandNumber: r.StandNumber, Status: r.Status,
		Cashier: Person{UserID: r.CashierID, Name: names[r.CashierID]},
		Items:   make([]Item, len(items)),
		Totals: Totals{
			Subtotal: r.Subtotal, Discount: r.VoucherDiscount, Service: r.Service, Tax: r.Tax, Total: r.Total,
		},
		VoucherCode: r.VoucherCode,
		ServiceBPS:  int(r.ServiceBps), TaxBPS: int(r.TaxBps),
		Payment: Payment{
			Method: r.Method, Amount: r.Amount, Received: r.Received, Change: r.Change, Status: r.PaymentStatus,
		},
		ClientCreatedAt: r.ClientCreatedAt, CreatedAt: r.CreatedAt, VoidedAt: r.VoidedAt, VoidReason: r.VoidReason,
	}
	if r.VoidedBy != nil {
		o.VoidedBy = &Person{UserID: *r.VoidedBy, Name: names[*r.VoidedBy]}
	}
	for i, it := range items {
		o.Items[i] = Item{
			OrderItemID: it.OrderItemID, MenuID: it.MenuID, MenuName: it.MenuName, Price: it.Price,
			Qty: int(it.Qty), Note: it.Note, LineTotal: it.LineTotal,
		}
	}
	return o, nil
}

// List returns a page of the outlet's orders of today, in its time zone,
// newest first, and how many there are. search narrows them to the receipt
// numbers holding it, in any case.
func (s *Service) List(ctx context.Context, outletID, search string, page httpx.Page) ([]Summary, int, error) {
	o, err := s.outlets.Cashier(ctx, outletID)
	if err != nil {
		return nil, 0, err
	}
	start, end, err := day(s.now(), o.Timezone)
	if err != nil {
		return nil, 0, err
	}
	var pattern *string
	if q := strings.TrimSpace(search); q != "" {
		escaped := likeEscaper.Replace(q)
		pattern = &escaped
	}
	q := store.New(s.pool)
	total, err := q.CountOrders(ctx, store.CountOrdersParams{
		OutletID: outletID, DayStart: start, DayEnd: end, Search: pattern,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}
	rows, err := q.ListOrders(ctx, store.ListOrdersParams{
		OutletID: outletID, DayStart: start, DayEnd: end, Search: pattern,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	cashiers := make([]string, len(rows))
	for i, r := range rows {
		cashiers[i] = r.CashierID
	}
	names, err := s.users.Names(ctx, cashiers)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Summary, len(rows))
	for i, r := range rows {
		out[i] = Summary{
			OrderID: r.OrderID, DeviceID: r.DeviceID, OrderNo: r.OrderNo, Type: r.Type, TableName: r.TableName,
			StandNumber: r.StandNumber, Status: r.Status, Total: r.Total, ItemCount: int(r.ItemCount),
			PaymentMethod: r.Method, Cashier: Person{UserID: r.CashierID, Name: names[r.CashierID]},
			ClientCreatedAt: r.ClientCreatedAt, CreatedAt: r.CreatedAt,
		}
	}
	return out, int(total), nil
}

// likeEscaper keeps LIKE's wildcards in a search literal.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Void cancels a paid order and its payment, for the reason given, puts
// what it sold back in the stock of its menus still tracked, and gives the
// use of its voucher back; the Log Action records order.voided. An order is
// voided once (ErrOrderAlreadyVoid).
func (s *Service) Void(ctx context.Context, p authn.Principal, ip, outletID, orderID, reason string) (Order, error) {
	reason = strings.TrimSpace(reason)
	switch n := utf8.RuneCountInString(reason); {
	case n == 0:
		return Order{}, httpx.Invalid(map[string]string{"reason": "required"})
	case n > MaxVoidReason:
		return Order{}, httpx.Invalid(map[string]string{"reason": "max"})
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		o, err := q.LockOrder(ctx, store.LockOrderParams{OrderID: orderID, OutletID: outletID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOrderNotFound
		}
		if err != nil {
			return fmt.Errorf("lock order %s: %w", orderID, err)
		}
		if o.Status == StatusVoid {
			return ErrOrderAlreadyVoid
		}
		if err := q.VoidOrder(ctx, store.VoidOrderParams{OrderID: orderID, VoidedBy: &p.UserID, VoidReason: &reason}); err != nil {
			return fmt.Errorf("void order %s: %w", orderID, err)
		}
		if err := q.VoidPayment(ctx, store.VoidPaymentParams{OrderID: orderID, OutletID: outletID}); err != nil {
			return fmt.Errorf("void the payment of order %s: %w", orderID, err)
		}
		rows, err := q.SoldByOrder(ctx, store.SoldByOrderParams{OrderID: orderID, OutletID: outletID})
		if err != nil {
			return fmt.Errorf("load what order %s sold: %w", orderID, err)
		}
		sold := make([]menu.Sold, len(rows))
		for i, r := range rows {
			sold[i] = menu.Sold{MenuID: r.MenuID, Qty: int(r.Qty)}
		}
		if err := s.menus.RecordVoid(ctx, tx, p.UserID, outletID, orderID, sold); err != nil {
			return err
		}
		// After the menus, as in a sale.
		if o.VoucherID != nil {
			if err := s.vouchers.Release(ctx, tx, *o.VoucherID); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "order.voided", UserID: p.UserID, OutletID: outletID, TargetType: "order", TargetID: orderID,
			Before: map[string]any{"order_no": o.OrderNo, "total": o.Total, "status": StatusPaid},
			After:  map[string]any{"status": StatusVoid, "reason": reason},
			IP:     ip,
		})
	})
	if err != nil {
		return Order{}, err
	}
	return s.Order(ctx, outletID, orderID)
}
