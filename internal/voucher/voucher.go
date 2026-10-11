// Package voucher is the Owner's discount vouchers: a percentage or an
// amount off an order's subtotal, for every outlet or some, a number of
// uses across the outlets together, and a period. The Owner manages them;
// an outlet's Manager sees the ones its cashiers can take; the cashier
// checks a code before charging; and the order module takes a use in the
// sale's own transaction and gives it back on a void. It owns the vouchers
// and voucher_outlets tables.
package voucher

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Limits of a voucher's fields, the schema's own.
const (
	MaxPercentBPS = 10_000        // a percentage's value: 100%
	MaxAmount     = 1_000_000_000 // rupiah: as much as an order's subtotal
	MaxQuota      = 1_000_000
	MaxName       = 100
)

// A voucher's statuses, as voucher_status computes them at a moment.
const (
	StatusActive    = "active"
	StatusScheduled = "scheduled" // not started yet
	StatusUsedUp    = "used_up"   // every use taken
	StatusExpired   = "expired"
	StatusInactive  = "inactive" // turned off by the Owner
)

// Voucher is one of the Owner's vouchers.
type Voucher struct {
	VoucherID string
	// Code is what the cashier types: capital letters and digits.
	Code string
	Name *string
	Terms
	// Quota is how many sales may use it, in every outlet together, and
	// UsedCount how many do; a void gives its sale's use back.
	Quota     int
	UsedCount int
	// StartAt is when it starts, nil at once; it works until ExpiredAt.
	StartAt    *time.Time
	ExpiredAt  time.Time
	AllOutlets bool
	// Outlets are the ones it is for when it isn't for all of them.
	Outlets []outlet.Ref
	Active  bool
	// Status is the voucher's at the moment it was read.
	Status string
	// FirstUsedAt is when a sale first used it, voided since or not; nil
	// while none has. From then on its code and type stay, its quota only
	// grows, its expiry only moves later, and it can't be deleted.
	FirstUsedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Input is a voucher as the Owner fills it in. Active counts when it is
// made (nil: active); an existing voucher changes it with SetActive.
type Input struct {
	Code        string
	Name        *string
	Type        string
	Value       *int64
	MaxDiscount *int64
	MinSpend    *int64
	Quota       *int
	StartAt     *time.Time
	ExpiredAt   *time.Time
	AllOutlets  bool
	OutletIDs   []string
	Active      *bool
}

// Filter narrows the Owner's list; empty fields don't.
type Filter struct {
	Search string // part of the code or the name
	Status string // one of the statuses
}

// Use is one sale that used a voucher.
type Use struct {
	OrderID string
	OrderNo string
	// Outlet is where it was sold; the order module gives its ID, and the
	// voucher module its code and name.
	Outlet outlet.Ref
	// At is when the cashier charged it, by the device's clock.
	At       time.Time
	Discount int64
	// Status is the order's: paid, or void.
	Status string
}

// Quote is a voucher the cashier checked, with its discount on the subtotal
// the cart has.
type Quote struct {
	Voucher  Voucher
	Discount int64
	// Applies is false while the subtotal is short of the voucher's minimum
	// spend: the sale can't take it yet.
	Applies bool
}

// Taken is what a sale gets of the voucher it uses: the voucher, its code
// as the receipt prints it, and the discount.
type Taken struct {
	VoucherID string
	Code      string
	Discount  int64
}

// Outlets is the outlet module, as far as vouchers need it: the outlets'
// codes and names, deleted ones too.
type Outlets interface {
	Refs(ctx context.Context, outletIDs []string) (map[string]outlet.Ref, error)
}

// Devices is the device module, as far as vouchers need it: a voucher is
// checked from one of the outlet's devices in use, as a sale is made.
type Devices interface {
	Current(ctx context.Context, outletID, token string) (device.Device, error)
}

// Orders is the order module, as far as vouchers need it: the sales that
// used one. order.Records is the real one; it reads in the caller's
// transaction.
type Orders interface {
	VoucherUses(ctx context.Context, tx pgx.Tx, voucherID string, page httpx.Page) ([]Use, int, error)
}

// Auditor records Log Action entries inside a transaction; audit.Recorder
// is the real one.
type Auditor interface {
	Record(ctx context.Context, tx pgx.Tx, e audit.Entry) error
}

// Service is the voucher module.
type Service struct {
	pool    *pgxpool.Pool
	outlets Outlets
	devices Devices
	orders  Orders
	audit   Auditor
	now     func() time.Time
}

// NewService returns the voucher module.
func NewService(pool *pgxpool.Pool, outlets Outlets, devices Devices, orders Orders, auditor Auditor) *Service {
	return &Service{pool: pool, outlets: outlets, devices: devices, orders: orders, audit: auditor, now: time.Now}
}
