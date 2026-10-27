package models

import "time"

// Limits of a voucher's fields, the schema's own.
const (
	MaxPercentBPS  = 10_000        // a percentage's value: 100%
	MaxAmount      = 1_000_000_000 // rupiah: as much as an order's subtotal
	MaxQuota       = 1_000_000
	MaxVoucherName = 100
)

// A voucher's statuses, as voucher_status computes them at a moment.
const (
	VoucherActive    = "active"
	VoucherScheduled = "scheduled" // not started yet
	VoucherUsedUp    = "used_up"   // every use taken
	VoucherExpired   = "expired"
	VoucherInactive  = "inactive" // turned off by the Owner
)

// The kinds of discount a voucher gives.
const (
	// VoucherPercent is a share of the subtotal: Value is basis points.
	VoucherPercent = "percent"
	// VoucherAmount is a sum of money: Value is rupiah.
	VoucherAmount = "amount"
)

// VoucherTerms are what a voucher's discount on a subtotal depends on.
type VoucherTerms struct {
	Type string
	// Value is basis points (1000 is 10%) for VoucherPercent, rupiah for
	// VoucherAmount.
	Value int64
	// MaxDiscount caps a percentage's discount, in rupiah; nil caps nothing.
	MaxDiscount *int64
	// MinSpend is the subtotal the voucher needs; nil needs none.
	MinSpend *int64
}

// Voucher is one of the Owner's vouchers.
type Voucher struct {
	VoucherID string
	// Code is what the cashier types: capital letters and digits.
	Code string
	Name *string
	VoucherTerms
	// Quota is how many sales may use it, in every outlet together, and
	// UsedCount how many do; a void gives its sale's use back.
	Quota     int
	UsedCount int
	// StartAt is when it starts, nil at once; it works until ExpiredAt.
	StartAt    *time.Time
	ExpiredAt  time.Time
	AllOutlets bool
	// Outlets are the ones it is for when it isn't for all of them.
	Outlets []OutletRef
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

// VoucherInput is a voucher as the Owner fills it in. Active counts when it
// is made (nil: active); an existing voucher changes it with SetActive.
type VoucherInput struct {
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

// VoucherFilter narrows the Owner's list; empty fields don't.
type VoucherFilter struct {
	Search string // part of the code or the name
	Status string // one of the statuses
}

// VoucherFields are a voucher's own fields, checked.
type VoucherFields struct {
	Code string
	Name *string
	VoucherTerms
	Quota      int
	StartAt    *time.Time
	ExpiredAt  time.Time
	AllOutlets bool
	// OutletIDs are sorted, each once; none for all outlets.
	OutletIDs []string
}

// Snapshot is the fields as the Log Action stores them.
func (v VoucherFields) Snapshot() map[string]any {
	return map[string]any{
		"code": v.Code, "name": v.Name, "type": v.Type, "value": v.Value,
		"max_discount": nullableInt64(v.MaxDiscount), "min_spend": nullableInt64(v.MinSpend), "quota": v.Quota,
		"start_at": nullableTime(v.StartAt), "expired_at": nullableTime(&v.ExpiredAt),
		"all_outlets": v.AllOutlets, "outlet_ids": v.OutletIDs,
	}
}

// VoucherLock is a voucher locked for a change: its fields, and what the
// change can't alter.
type VoucherLock struct {
	VoucherFields
	Active bool
	// Used is whether a sale has used it.
	Used bool
}

// VoucherUse is one sale that used a voucher.
type VoucherUse struct {
	OrderID string
	OrderNo string
	// Outlet is where it was sold; the orders give its ID, and the voucher
	// service its code and name.
	Outlet OutletRef
	// At is when the cashier charged it, by the device's clock.
	At       time.Time
	Discount int64
	// Status is the order's: paid, or void.
	Status string
}

// VoucherQuote is a voucher the cashier checked, with its discount on the
// subtotal the cart has.
type VoucherQuote struct {
	Voucher  Voucher
	Discount int64
	// Applies is false while the subtotal is short of the voucher's minimum
	// spend: the sale can't take it yet.
	Applies bool
}

// VoucherTaken is what a sale gets of the voucher it uses: the voucher, its
// code as the receipt prints it, and the discount.
type VoucherTaken struct {
	VoucherID string
	Code      string
	Discount  int64
}

// VoucherOutletResponse is an outlet a voucher is for, or a use was sold at.
type VoucherOutletResponse struct {
	OutletID string `json:"outlet_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
}

// VoucherResponse is a voucher as the voucher endpoints send it.
type VoucherResponse struct {
	VoucherID   string     `json:"voucher_id"`
	Code        string     `json:"code"`
	Name        *string    `json:"name"`
	Type        string     `json:"type"`
	Value       int64      `json:"value"`
	MaxDiscount *int64     `json:"max_discount"`
	MinSpend    *int64     `json:"min_spend"`
	Quota       int        `json:"quota"`
	UsedCount   int        `json:"used_count"`
	StartAt     *time.Time `json:"start_at"`
	ExpiredAt   time.Time  `json:"expired_at"`
	AllOutlets  bool       `json:"all_outlets"`
	// Outlets are the ones it is for, none when it is for all of them; an
	// outlet's Manager sees only theirs.
	Outlets     []VoucherOutletResponse `json:"outlets"`
	IsActive    bool                    `json:"is_active"`
	Status      string                  `json:"status"`
	FirstUsedAt *time.Time              `json:"first_used_at"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

// NewVoucherResponse returns the voucher's response, its times in UTC.
func NewVoucherResponse(v Voucher) VoucherResponse {
	out := VoucherResponse{
		VoucherID: v.VoucherID, Code: v.Code, Name: v.Name, Type: v.Type, Value: v.Value,
		MaxDiscount: v.MaxDiscount, MinSpend: v.MinSpend, Quota: v.Quota, UsedCount: v.UsedCount,
		StartAt: utc(v.StartAt), ExpiredAt: v.ExpiredAt.UTC(), AllOutlets: v.AllOutlets,
		Outlets:  make([]VoucherOutletResponse, len(v.Outlets)),
		IsActive: v.Active, Status: v.Status, FirstUsedAt: utc(v.FirstUsedAt),
		CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC(),
	}
	for i, o := range v.Outlets {
		out.Outlets[i] = VoucherOutletResponse{OutletID: o.OutletID, Code: o.Code, Name: o.Name}
	}
	return out
}

// NewVoucherResponses returns the vouchers' responses.
func NewVoucherResponses(vs []Voucher) []VoucherResponse {
	out := make([]VoucherResponse, len(vs))
	for i, v := range vs {
		out[i] = NewVoucherResponse(v)
	}
	return out
}

// VoucherTermsResponse is a voucher's terms, as the cashier works its
// discount out from them.
type VoucherTermsResponse struct {
	VoucherID   string  `json:"voucher_id"`
	Code        string  `json:"code"`
	Name        *string `json:"name"`
	Type        string  `json:"type"`
	Value       int64   `json:"value"`
	MaxDiscount *int64  `json:"max_discount"`
	MinSpend    *int64  `json:"min_spend"`
}

// VoucherQuoteResponse is what the cashier gets of a voucher it checks: its
// terms, for working the discount out as the cart changes, and the discount
// on the subtotal it sent.
type VoucherQuoteResponse struct {
	Voucher  VoucherTermsResponse `json:"voucher"`
	Discount int64                `json:"discount"`
	// Applies is false while the subtotal is short of min_spend: the
	// discount is 0, and the sale can't take the voucher yet.
	Applies bool `json:"applies"`
}

// NewVoucherQuoteResponse returns the quote's response.
func NewVoucherQuoteResponse(q VoucherQuote) VoucherQuoteResponse {
	v := q.Voucher
	return VoucherQuoteResponse{
		Voucher: VoucherTermsResponse{
			VoucherID: v.VoucherID, Code: v.Code, Name: v.Name, Type: v.Type, Value: v.Value,
			MaxDiscount: v.MaxDiscount, MinSpend: v.MinSpend,
		},
		Discount: q.Discount,
		Applies:  q.Applies,
	}
}

// VoucherUseResponse is a sale that used a voucher, as its history shows it.
type VoucherUseResponse struct {
	OrderID string                `json:"order_id"`
	OrderNo string                `json:"order_no"`
	Outlet  VoucherOutletResponse `json:"outlet"`
	// ClientCreatedAt is when the cashier charged the sale.
	ClientCreatedAt time.Time `json:"client_created_at"`
	VoucherDiscount int64     `json:"voucher_discount"`
	// Status is the order's: paid, or void, which gave the use back.
	Status string `json:"status"`
}

// NewVoucherUseResponse returns the use's response, its time in UTC.
func NewVoucherUseResponse(u VoucherUse) VoucherUseResponse {
	return VoucherUseResponse{
		OrderID: u.OrderID, OrderNo: u.OrderNo,
		Outlet:          VoucherOutletResponse{OutletID: u.Outlet.OutletID, Code: u.Outlet.Code, Name: u.Outlet.Name},
		ClientCreatedAt: u.At.UTC(), VoucherDiscount: u.Discount, Status: u.Status,
	}
}

// nullableInt64 is an optional amount as the Log Action stores it: the
// number, or null.
func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// nullableTime is an optional time as the Log Action stores it: RFC 3339
// in UTC, or null.
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
