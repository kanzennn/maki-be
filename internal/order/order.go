// Package order is the cashier: the catalog it sells from, the orders it
// takes with their lines and payments, and voids. It owns the orders,
// order_items and payments tables; menus, devices, payment methods, outlets,
// vouchers and tables it reaches through their modules.
//
// The device makes each order's ID and receipt number, so the ID is also
// the order's idempotency key: a resend is the same order, never a second
// one, whether it comes seconds later or, with the offline milestone, days.
package order

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/order/store"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/table"
	"github.com/kervzent/maki-be/internal/voucher"
)

// Order types.
const (
	DineIn   = "dine_in"
	Takeaway = "takeaway"
)

// Statuses of an order and its payment.
const (
	StatusPaid = "paid"
	StatusVoid = "void"
)

// Order is one of the cashier's transactions, as it was taken.
type Order struct {
	OrderID  string
	DeviceID string
	// OrderNo is the receipt number the device made:
	// OUTLETCODE-DEVICECODE-YYYYMMDD-0001.
	OrderNo string
	Type    string
	// TableID and TableName are the table a dine-in sale seated its
	// customers at, its name as it was then, for the receipt. Nil for none.
	TableID   *string
	TableName *string
	// StandNumber is the number on the stand a dine-in customer took, where
	// a waiter brings the order. Nil for none.
	StandNumber *string
	Status      string
	Cashier     Person
	Items       []Item
	Totals
	// VoucherCode is the code of the voucher the sale used, as it was then:
	// the receipt prints it beside the discount. Nil for none.
	VoucherCode *string
	// ServiceBPS and TaxBPS are the rates the order was charged at, in basis
	// points.
	ServiceBPS int
	TaxBPS     int
	Payment    Payment
	// ClientCreatedAt is when the device made the order; CreatedAt, when the
	// server took it.
	ClientCreatedAt time.Time
	CreatedAt       time.Time
	VoidedAt        *time.Time
	VoidedBy        *Person
	VoidReason      *string
}

// Item is a line of an order, with the menu's name and price as they were at
// the sale.
type Item struct {
	OrderItemID string
	MenuID      string
	MenuName    string
	Price       int64
	Qty         int
	Note        *string
	LineTotal   int64
}

// Payment is how an order was paid.
type Payment struct {
	Method string
	Amount int64
	// Received and Change are cash's: what the customer handed over, and
	// what they got back.
	Received *int64
	Change   *int64
	Status   string
}

// Person is who took or voided an order.
type Person struct {
	UserID string
	Name   string
}

// Summary is an order in a list.
type Summary struct {
	OrderID         string
	DeviceID        string
	OrderNo         string
	Type            string
	TableName       *string
	StandNumber     *string
	Status          string
	Total           int64
	ItemCount       int // every line's qty, together
	PaymentMethod   string
	Cashier         Person
	ClientCreatedAt time.Time
	CreatedAt       time.Time
}

// Menus is the menu module, as far as the cashier needs it: what it sells,
// and the stock its sales and voids change.
type Menus interface {
	PosCatalog(ctx context.Context, outletID string) ([]menu.PosCategory, []menu.Menu, error)
	LockForSale(ctx context.Context, tx pgx.Tx, outletID string, menuIDs []string) (map[string]menu.SaleMenu, error)
	RecordSale(ctx context.Context, tx pgx.Tx, userID, outletID, orderID string, sold []menu.Sold) error
	RecordVoid(ctx context.Context, tx pgx.Tx, userID, outletID, orderID string, sold []menu.Sold) error
}

// Devices is the device module: which of the outlet's devices a request
// comes from.
type Devices interface {
	Open(ctx context.Context, outletID, token string) (device.Device, error)
	Hold(ctx context.Context, tx pgx.Tx, outletID, token string) (device.Device, error)
}

// Payments is the payment module: the methods the outlet's cashier takes.
type Payments interface {
	Enabled(ctx context.Context, outletID string) ([]string, error)
	Accepts(ctx context.Context, tx pgx.Tx, outletID, method string) (bool, error)
}

// Outlets is the outlet module: the outlet as its cashier needs it.
type Outlets interface {
	Cashier(ctx context.Context, outletID string) (outlet.Cashier, error)
	HoldForSale(ctx context.Context, tx pgx.Tx, outletID string) (outlet.Cashier, error)
}

// Users is the user module: the names of who took and voided orders.
type Users interface {
	Names(ctx context.Context, userIDs []string) (map[string]string, error)
}

// Vouchers is the voucher module: the use a sale takes of the voucher the
// customer gave, in the sale's transaction, and gives back when the sale is
// voided.
type Vouchers interface {
	Redeem(ctx context.Context, tx pgx.Tx, outletID, code string, subtotal int64) (voucher.Taken, error)
	Release(ctx context.Context, tx pgx.Tx, voucherID string) error
}

// Tables is the table module: the table a dine-in sale seats its customers
// at, held in the sale's transaction.
type Tables interface {
	HoldForSale(ctx context.Context, tx pgx.Tx, outletID, tableID string) (table.Seat, error)
	DineInMode(ctx context.Context, outletID string) (table.Mode, error)
	HoldDineInMode(ctx context.Context, tx pgx.Tx, outletID string) (table.Mode, error)
}

// Auditor records Log Action entries inside a transaction; audit.Recorder
// is the real one.
type Auditor interface {
	Record(ctx context.Context, tx pgx.Tx, e audit.Entry) error
}

// Service takes and reads an outlet's orders. Every method takes the outlet
// it acts in; the routes have checked the caller may act there.
type Service struct {
	pool     *pgxpool.Pool
	menus    Menus
	devices  Devices
	payments Payments
	outlets  Outlets
	users    Users
	vouchers Vouchers
	tables   Tables
	audit    Auditor
	// now is the server's clock; tests set it.
	now func() time.Time
}

// NewService returns the order module's service.
func NewService(pool *pgxpool.Pool, menus Menus, devices Devices, payments Payments, outlets Outlets, users Users,
	vouchers Vouchers, tables Tables, auditor Auditor,
) *Service {
	return &Service{
		pool: pool, menus: menus, devices: devices, payments: payments, outlets: outlets, users: users,
		vouchers: vouchers, tables: tables, audit: auditor, now: time.Now,
	}
}

// Records tells other modules about the orders: the outlet module, which
// locks an outlet's code once it has one; the voucher module, which shows
// the sales that used a voucher; and the table module, which seats
// customers where the sales were. Like auth.Sessions it holds nothing and
// reads in the caller's transaction.
type Records struct{}

// HasOrders reports whether the outlet has taken an order.
func (Records) HasOrders(ctx context.Context, tx pgx.Tx, outletID string) (bool, error) {
	ok, err := store.New(tx).OutletHasOrders(ctx, outletID)
	if err != nil {
		return false, fmt.Errorf("find the orders of %s: %w", outletID, err)
	}
	return ok, nil
}

// VoucherUses returns a page of the sales that used the voucher, in every
// outlet, newest first, voided ones too, and how many there are. Each names
// its outlet by ID only.
func (Records) VoucherUses(ctx context.Context, tx pgx.Tx, voucherID string, page httpx.Page) ([]voucher.Use, int, error) {
	q := store.New(tx)
	total, err := q.CountVoucherUses(ctx, &voucherID)
	if err != nil {
		return nil, 0, fmt.Errorf("count the uses of voucher %s: %w", voucherID, err)
	}
	rows, err := q.ListVoucherUses(ctx, store.ListVoucherUsesParams{
		VoucherID:  &voucherID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list the uses of voucher %s: %w", voucherID, err)
	}
	out := make([]voucher.Use, len(rows))
	for i, r := range rows {
		out[i] = voucher.Use{
			OrderID: r.OrderID, OrderNo: r.OrderNo, Outlet: outlet.Ref{OutletID: r.OutletID},
			At: r.ClientCreatedAt, Discount: r.VoucherDiscount, Status: r.Status,
		}
	}
	return out, int(total), nil
}

// TableSales returns when each paid dine-in sale at the outlet's tables
// after since was written, by table ID.
func (Records) TableSales(ctx context.Context, tx pgx.Tx, outletID string, since time.Time) (map[string][]time.Time, error) {
	rows, err := store.New(tx).ListTableSales(ctx, store.ListTableSalesParams{OutletID: outletID, Since: since})
	if err != nil {
		return nil, fmt.Errorf("list the sales at the tables of %s: %w", outletID, err)
	}
	out := map[string][]time.Time{}
	for _, r := range rows {
		out[r.TableID] = append(out[r.TableID], r.CreatedAt)
	}
	return out, nil
}

// day returns the outlet's day that t falls in, in its time zone: from its
// midnight to the next.
func day(t time.Time, timezone string) (start, end time.Time, err error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("load time zone %q: %w", timezone, err)
	}
	y, m, d := t.In(loc).Date()
	start = time.Date(y, m, d, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1), nil
}
