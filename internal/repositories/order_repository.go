package repositories

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/order"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// OrderRepository owns orders, order_items and payments: the cashier's
// sales, their lines with the menus' names and prices as they were, and how
// each was paid. Who took or voided an order is named by user ID only.
//
// It also tells others about the orders: the outlets, which lock an
// outlet's code once it has one; the vouchers, which show the sales that
// used one; and the tables, which seat customers where the sales were.
type OrderRepository struct {
	db *config.Database
}

// NewOrderRepository returns the orders' repository.
func NewOrderRepository(db *config.Database) *OrderRepository {
	return &OrderRepository{db: db}
}

// Find returns the outlet of the order under the ID, whichever outlet's,
// and the hash of the order as its device sent it: ErrOrderNotFound if no
// order has the ID. The ID is the device's idempotency key, so a resend is
// told apart from another order that happened to send the same ID.
func (r *OrderRepository) Find(ctx context.Context, orderID string) (outletID, requestHash string, err error) {
	row, err := store.New(r.db.Conn(ctx)).FindOrder(ctx, orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", apperror.ErrOrderNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("find order %s: %w", orderID, err)
	}
	return row.OutletID, row.RequestHash, nil
}

// Get returns one of the outlet's orders, with its lines and its payment:
// ErrOrderNotFound if it has no such order. Its cashier, and who voided it,
// are left unnamed.
func (r *OrderRepository) Get(ctx context.Context, outletID, orderID string) (models.Order, error) {
	q := store.New(r.db.Conn(ctx))
	row, err := q.GetOrder(ctx, store.GetOrderParams{OrderID: orderID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Order{}, apperror.ErrOrderNotFound
	}
	if err != nil {
		return models.Order{}, fmt.Errorf("load order %s: %w", orderID, err)
	}
	items, err := q.ListOrderItems(ctx, store.ListOrderItemsParams{OrderID: orderID, OutletID: outletID})
	if err != nil {
		return models.Order{}, fmt.Errorf("load the lines of order %s: %w", orderID, err)
	}
	o := models.Order{
		OrderID: row.OrderID, DeviceID: row.DeviceID, OrderNo: row.OrderNo, Type: row.Type, TableID: row.TableID,
		TableName: row.TableName, StandNumber: row.StandNumber, Status: row.Status,
		Cashier: models.Person{UserID: row.CashierID},
		Items:   make([]models.OrderItem, len(items)),
		OrderTotals: models.OrderTotals{
			Subtotal: row.Subtotal, Discount: row.VoucherDiscount, Service: row.Service, Tax: row.Tax, Total: row.Total,
		},
		VoucherCode: row.VoucherCode,
		ServiceBPS:  int(row.ServiceBps), TaxBPS: int(row.TaxBps),
		Payment: models.OrderPayment{
			Method: row.Method, Amount: row.Amount, Received: row.Received, Change: row.Change, Status: row.PaymentStatus,
		},
		ClientCreatedAt: row.ClientCreatedAt, CreatedAt: row.CreatedAt, VoidedAt: row.VoidedAt,
		VoidReason: row.VoidReason,
	}
	if row.VoidedBy != nil {
		o.VoidedBy = &models.Person{UserID: *row.VoidedBy}
	}
	for i, it := range items {
		o.Items[i] = models.OrderItem{
			OrderItemID: it.OrderItemID, MenuID: it.MenuID, MenuName: it.MenuName, Price: it.Price,
			Qty: int(it.Qty), Note: it.Note, LineTotal: it.LineTotal,
		}
	}
	return o, nil
}

// List returns a page of the outlet's orders made from start until end,
// newest first, and how many there are. A search narrows them to the
// receipt numbers holding it, in any case. Their cashiers are left unnamed.
func (r *OrderRepository) List(ctx context.Context, outletID string, start, end time.Time, search string, page pagination.Page) ([]models.OrderSummary, int, error) {
	pattern := optional(search)
	if pattern != nil {
		*pattern = likeEscaper.Replace(*pattern)
	}
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountOrders(ctx, store.CountOrdersParams{
		OutletID: outletID, DayStart: start, DayEnd: end, Search: pattern,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count orders: %w", err)
	}
	rows, err := q.ListOrders(ctx, store.ListOrdersParams{
		OutletID: outletID, DayStart: start, DayEnd: end, Search: pattern,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	out := make([]models.OrderSummary, len(rows))
	for i, row := range rows {
		out[i] = models.OrderSummary{
			OrderID: row.OrderID, DeviceID: row.DeviceID, OrderNo: row.OrderNo, Type: row.Type,
			TableName: row.TableName, StandNumber: row.StandNumber, Status: row.Status, Total: row.Total,
			ItemCount: int(row.ItemCount), PaymentMethod: row.Method, Cashier: models.Person{UserID: row.CashierID},
			ClientCreatedAt: row.ClientCreatedAt, CreatedAt: row.CreatedAt,
		}
	}
	return out, int(total), nil
}

// LastNumber returns the highest count among the receipt numbers of the
// device's orders made from start until end: 0 for none.
func (r *OrderRepository) LastNumber(ctx context.Context, outletID, deviceID string, start, end time.Time) (int, error) {
	last, err := store.New(r.db.Conn(ctx)).LastOrderNumber(ctx, store.LastOrderNumberParams{
		OutletID: outletID, DeviceID: deviceID, DayStart: start, DayEnd: end,
	})
	if err != nil {
		return 0, fmt.Errorf("find the last receipt number of device %s: %w", deviceID, err)
	}
	return int(last), nil
}

// Insert saves the order, its lines and its payment, in the transaction the
// context carries: ErrOrderIDTaken if an order has its ID already,
// ErrOrderNoTaken if one of the outlet's has its receipt number.
func (r *OrderRepository) Insert(ctx context.Context, o models.OrderRecord) error {
	q := store.New(r.db.Conn(ctx))
	err := q.InsertOrder(ctx, store.InsertOrderParams{
		OrderID: o.OrderID, OutletID: o.OutletID, DeviceID: o.DeviceID, OrderNo: o.OrderNo, Type: o.Type,
		TableID: o.TableID, TableName: o.TableName, StandNumber: o.StandNumber, CashierID: o.CashierID,
		Subtotal: o.Subtotal, VoucherID: o.VoucherID, VoucherCode: o.VoucherCode, VoucherDiscount: o.Discount,
		ServiceBps: int32(o.ServiceBPS), Service: o.Service, //nolint:gosec // a rate is 0 to 10000
		TaxBps: int32(o.TaxBPS), Tax: o.Tax, Total: o.Total, //nolint:gosec // as above
		ClientCreatedAt: o.ClientCreatedAt, RequestHash: o.RequestHash,
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "orders_pkey":
			return apperror.ErrOrderIDTaken
		case "orders_outlet_order_no_key":
			return apperror.ErrOrderNoTaken
		}
	}
	if err != nil {
		return fmt.Errorf("insert order %s: %w", o.OrderID, err)
	}
	items := store.InsertOrderItemsParams{OutletID: o.OutletID, OrderID: o.OrderID}
	for i, it := range o.Items {
		note := ""
		if it.Note != nil {
			note = *it.Note
		}
		items.OrderItemIds = append(items.OrderItemIds, it.OrderItemID)
		items.Positions = append(items.Positions, int32(i)) //nolint:gosec // at most MaxOrderLines
		items.MenuIds = append(items.MenuIds, it.MenuID)
		items.MenuNames = append(items.MenuNames, it.MenuName)
		items.Prices = append(items.Prices, it.Price)
		items.Qtys = append(items.Qtys, int32(it.Qty)) //nolint:gosec // at most MaxOrderQty
		items.Notes = append(items.Notes, note)
		items.LineTotals = append(items.LineTotals, it.LineTotal)
	}
	if err := q.InsertOrderItems(ctx, items); err != nil {
		return fmt.Errorf("insert the lines of order %s: %w", o.OrderID, err)
	}
	err = q.InsertPayment(ctx, store.InsertPaymentParams{
		PaymentID: o.PaymentID, OutletID: o.OutletID, OrderID: o.OrderID, Method: o.Payment.Method,
		Amount: o.Payment.Amount, Received: o.Payment.Received, Change: o.Payment.Change,
	})
	if err != nil {
		return fmt.Errorf("insert the payment of order %s: %w", o.OrderID, err)
	}
	return nil
}

// Lock locks one of the outlet's orders for its void, in the transaction
// the context carries: ErrOrderNotFound if it has no such order.
func (r *OrderRepository) Lock(ctx context.Context, outletID, orderID string) (models.OrderLock, error) {
	row, err := store.New(r.db.Conn(ctx)).LockOrder(ctx, store.LockOrderParams{OrderID: orderID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.OrderLock{}, apperror.ErrOrderNotFound
	}
	if err != nil {
		return models.OrderLock{}, fmt.Errorf("lock order %s: %w", orderID, err)
	}
	return models.OrderLock{OrderNo: row.OrderNo, Total: row.Total, Status: row.Status, VoucherID: row.VoucherID}, nil
}

// Void voids the order and its payment, by the user, for the reason.
func (r *OrderRepository) Void(ctx context.Context, outletID, orderID, userID, reason string) error {
	q := store.New(r.db.Conn(ctx))
	if err := q.VoidOrder(ctx, store.VoidOrderParams{OrderID: orderID, VoidedBy: &userID, VoidReason: &reason}); err != nil {
		return fmt.Errorf("void order %s: %w", orderID, err)
	}
	if err := q.VoidPayment(ctx, store.VoidPaymentParams{OrderID: orderID, OutletID: outletID}); err != nil {
		return fmt.Errorf("void the payment of order %s: %w", orderID, err)
	}
	return nil
}

// Sold returns how many of each menu the order sold, its lines together.
func (r *OrderRepository) Sold(ctx context.Context, outletID, orderID string) ([]models.Sold, error) {
	rows, err := store.New(r.db.Conn(ctx)).SoldByOrder(ctx, store.SoldByOrderParams{OrderID: orderID, OutletID: outletID})
	if err != nil {
		return nil, fmt.Errorf("load what order %s sold: %w", orderID, err)
	}
	out := make([]models.Sold, len(rows))
	for i, row := range rows {
		out[i] = models.Sold{MenuID: row.MenuID, Qty: int(row.Qty)}
	}
	return out, nil
}

// HasOrders reports whether the outlet has taken an order.
func (r *OrderRepository) HasOrders(ctx context.Context, outletID string) (bool, error) {
	ok, err := store.New(r.db.Conn(ctx)).OutletHasOrders(ctx, outletID)
	if err != nil {
		return false, fmt.Errorf("find the orders of %s: %w", outletID, err)
	}
	return ok, nil
}

// VoucherUses returns a page of the sales that used the voucher, in every
// outlet, newest first, voided ones too, and how many there are. Each names
// its outlet by ID only.
func (r *OrderRepository) VoucherUses(ctx context.Context, voucherID string, page pagination.Page) ([]models.VoucherUse, int, error) {
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountVoucherUses(ctx, &voucherID)
	if err != nil {
		return nil, 0, fmt.Errorf("count the uses of voucher %s: %w", voucherID, err)
	}
	rows, err := q.ListVoucherUses(ctx, store.ListVoucherUsesParams{
		VoucherID:  &voucherID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list the uses of voucher %s: %w", voucherID, err)
	}
	out := make([]models.VoucherUse, len(rows))
	for i, row := range rows {
		out[i] = models.VoucherUse{
			OrderID: row.OrderID, OrderNo: row.OrderNo, Outlet: models.OutletRef{OutletID: row.OutletID},
			At: row.ClientCreatedAt, Discount: row.VoucherDiscount, Status: row.Status,
		}
	}
	return out, int(total), nil
}

// TableSales returns when each paid dine-in sale at the outlet's tables
// after since was written, by table ID.
func (r *OrderRepository) TableSales(ctx context.Context, outletID string, since time.Time) (map[string][]time.Time, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListTableSales(ctx, store.ListTableSalesParams{OutletID: outletID, Since: since})
	if err != nil {
		return nil, fmt.Errorf("list the sales at the tables of %s: %w", outletID, err)
	}
	out := map[string][]time.Time{}
	for _, row := range rows {
		out[row.TableID] = append(out[row.TableID], row.CreatedAt)
	}
	return out, nil
}
