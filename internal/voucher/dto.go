package voucher

import "time"

// voucherRequest is the body of POST and PUT. The caps here only bound what
// the server reads; checkVoucher applies the real rules.
type voucherRequest struct {
	Code string  `json:"code" binding:"required,max=64"`
	Name *string `json:"name" binding:"omitempty,max=400"`
	Type string  `json:"type" binding:"required,max=16"`
	// Value is basis points for a percentage (1000 is 10%), rupiah for an
	// amount.
	Value       *int64     `json:"value" binding:"required"`
	MaxDiscount *int64     `json:"max_discount"`
	MinSpend    *int64     `json:"min_spend"`
	Quota       *int       `json:"quota" binding:"required"`
	StartAt     *time.Time `json:"start_at"`
	ExpiredAt   *time.Time `json:"expired_at" binding:"required"`
	AllOutlets  *bool      `json:"all_outlets" binding:"required"`
	OutletIDs   []string   `json:"outlet_ids" binding:"max=1000,dive,max=64"`
	// IsActive is for a new voucher, active when left out; an existing one
	// changes it with PATCH …/status.
	IsActive *bool `json:"is_active"`
}

func (r voucherRequest) input() Input {
	return Input{
		Code: r.Code, Name: r.Name, Type: r.Type, Value: r.Value, MaxDiscount: r.MaxDiscount, MinSpend: r.MinSpend,
		Quota: r.Quota, StartAt: r.StartAt, ExpiredAt: r.ExpiredAt, AllOutlets: r.AllOutlets != nil && *r.AllOutlets,
		OutletIDs: r.OutletIDs, Active: r.IsActive,
	}
}

type statusRequest struct {
	IsActive *bool `json:"is_active" binding:"required"`
}

// validateRequest is the body of POST /outlets/{oid}/vouchers/validate:
// the code the customer gave, and the cart's subtotal.
type validateRequest struct {
	Code     string `json:"code" binding:"required,max=64"`
	Subtotal *int64 `json:"subtotal" binding:"required"`
}

type outletResponse struct {
	OutletID string `json:"outlet_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
}

type voucherResponse struct {
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
	Outlets     []outletResponse `json:"outlets"`
	IsActive    bool             `json:"is_active"`
	Status      string           `json:"status"`
	FirstUsedAt *time.Time       `json:"first_used_at"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

func toResponse(v Voucher) voucherResponse {
	out := voucherResponse{
		VoucherID: v.VoucherID, Code: v.Code, Name: v.Name, Type: v.Type, Value: v.Value,
		MaxDiscount: v.MaxDiscount, MinSpend: v.MinSpend, Quota: v.Quota, UsedCount: v.UsedCount,
		StartAt: utcPtr(v.StartAt), ExpiredAt: v.ExpiredAt.UTC(), AllOutlets: v.AllOutlets,
		Outlets:  make([]outletResponse, len(v.Outlets)),
		IsActive: v.Active, Status: v.Status, FirstUsedAt: utcPtr(v.FirstUsedAt),
		CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC(),
	}
	for i, o := range v.Outlets {
		out.Outlets[i] = outletResponse{OutletID: o.OutletID, Code: o.Code, Name: o.Name}
	}
	return out
}

// validateResponse is what the cashier gets of a voucher: its terms, for
// working the discount out as the cart changes, and the discount on the
// subtotal it sent.
type validateResponse struct {
	Voucher  termsResponse `json:"voucher"`
	Discount int64         `json:"discount"`
	// Applies is false while the subtotal is short of min_spend: the
	// discount is 0, and the sale can't take the voucher yet.
	Applies bool `json:"applies"`
}

type termsResponse struct {
	VoucherID   string  `json:"voucher_id"`
	Code        string  `json:"code"`
	Name        *string `json:"name"`
	Type        string  `json:"type"`
	Value       int64   `json:"value"`
	MaxDiscount *int64  `json:"max_discount"`
	MinSpend    *int64  `json:"min_spend"`
}

func toValidateResponse(q Quote) validateResponse {
	v := q.Voucher
	return validateResponse{
		Voucher: termsResponse{
			VoucherID: v.VoucherID, Code: v.Code, Name: v.Name, Type: v.Type, Value: v.Value,
			MaxDiscount: v.MaxDiscount, MinSpend: v.MinSpend,
		},
		Discount: q.Discount,
		Applies:  q.Applies,
	}
}

type redemptionResponse struct {
	OrderID string         `json:"order_id"`
	OrderNo string         `json:"order_no"`
	Outlet  outletResponse `json:"outlet"`
	// ClientCreatedAt is when the cashier charged the sale.
	ClientCreatedAt time.Time `json:"client_created_at"`
	VoucherDiscount int64     `json:"voucher_discount"`
	// Status is the order's: paid, or void, which gave the use back.
	Status string `json:"status"`
}

func toRedemptionResponse(u Use) redemptionResponse {
	return redemptionResponse{
		OrderID: u.OrderID, OrderNo: u.OrderNo,
		Outlet:          outletResponse{OutletID: u.Outlet.OutletID, Code: u.Outlet.Code, Name: u.Outlet.Name},
		ClientCreatedAt: u.At.UTC(), VoucherDiscount: u.Discount, Status: u.Status,
	}
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
