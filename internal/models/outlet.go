package models

import "time"

// DefaultTimezone is an outlet's time zone when the Owner picks none.
const DefaultTimezone = "Asia/Jakarta"

// Outlet is one of the business's outlets, as the other services see it.
type Outlet struct {
	OutletID string
	Code     string
	Name     string
	// Timezone is an IANA name such as Asia/Jakarta: every time the outlet
	// shows is in this zone.
	Timezone string
	// Locale is the default language: receipts, and users who haven't
	// chosen one.
	Locale string
	// Active is false while the Owner has the outlet deactivated: its users
	// can't sign in.
	Active bool
}

// OutletDetail is an outlet with everything the Owner sees and changes.
type OutletDetail struct {
	Outlet
	Address *string
	Phone   *string
	// OpenTime and CloseTime are "15:04", both nil when no hours are set.
	// Closing before opening means past midnight.
	OpenTime  *string
	CloseTime *string
	// TaxBPS and ServiceBPS are basis points: 1100 is 11%.
	TaxBPS     int
	ServiceBPS int
	Managers   []Manager
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// OutletCashier is the outlet as its cashier needs it: the receipt's
// header, the rates, and the code and time zone its receipt numbers are
// made of.
type OutletCashier struct {
	OutletID string
	Code     string
	Name     string
	Address  *string
	Phone    *string
	Timezone string
	// Locale is the receipts' language.
	Locale string
	// TaxBPS and ServiceBPS are basis points: 1100 is 11%.
	TaxBPS     int
	ServiceBPS int
	// Active is false once the Owner has deactivated the outlet: it can't
	// sell.
	Active bool
}

// OutletHours are when an outlet opens and closes, in its time zone:
// "15:04" each, both nil when it has no hours. Closing before opening means
// past midnight.
type OutletHours struct {
	Timezone string
	Open     *string
	Close    *string
}

// OutletRef names an outlet in other records, which keep it after it is
// deleted: a voucher's history, say.
type OutletRef struct {
	OutletID string
	Code     string
	Name     string
	// Deleted is whether the Owner has deleted the outlet since.
	Deleted bool
}

// OutletInput is an outlet as the Owner fills it in.
type OutletInput struct {
	Code      string
	Name      string
	Address   *string
	Phone     *string
	Timezone  string // empty is Asia/Jakarta
	Locale    string // empty is id
	OpenTime  *string
	CloseTime *string
	// TaxPct and ServicePct are percentages with at most two decimals.
	TaxPct     float64
	ServicePct float64
}

// OutletFilter narrows the outlet list.
type OutletFilter struct {
	Search string // part of the name or the code
	Status string // active, inactive, or empty for both
}

// OutletFields are an outlet's own fields, checked: what the Owner sets,
// and its status.
type OutletFields struct {
	Code     string
	Name     string
	Address  *string
	Phone    *string
	Timezone string
	Locale   string
	// OpenTime and CloseTime are "15:04", both nil when no hours are set.
	OpenTime   *string
	CloseTime  *string
	TaxBPS     int
	ServiceBPS int
	Status     string
}

// Snapshot is the fields as the Log Action stores them.
func (f OutletFields) Snapshot() map[string]any {
	return map[string]any{
		"code":        f.Code,
		"name":        f.Name,
		"address":     f.Address,
		"phone":       f.Phone,
		"timezone":    f.Timezone,
		"locale":      f.Locale,
		"open_time":   f.OpenTime,
		"close_time":  f.CloseTime,
		"tax_bps":     f.TaxBPS,
		"service_bps": f.ServiceBPS,
		"status":      f.Status,
	}
}

// OutletResponse is an outlet as the outlet endpoints send it.
type OutletResponse struct {
	OutletID   string           `json:"outlet_id"`
	Code       string           `json:"code"`
	Name       string           `json:"name"`
	Address    *string          `json:"address"`
	Phone      *string          `json:"phone"`
	Timezone   string           `json:"timezone"`
	Locale     string           `json:"locale"`
	OpenTime   *string          `json:"open_time"`
	CloseTime  *string          `json:"close_time"`
	TaxPct     float64          `json:"tax_pct"`
	ServicePct float64          `json:"service_pct"`
	Status     string           `json:"status"`
	Managers   []PersonResponse `json:"managers"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

// NewOutletResponse returns the outlet's response, its rates as
// percentages and its times in UTC.
func NewOutletResponse(d OutletDetail) OutletResponse {
	status := "inactive"
	if d.Active {
		status = "active"
	}
	managers := make([]PersonResponse, len(d.Managers))
	for i, m := range d.Managers {
		managers[i] = PersonResponse(m)
	}
	return OutletResponse{
		OutletID:   d.OutletID,
		Code:       d.Code,
		Name:       d.Name,
		Address:    d.Address,
		Phone:      d.Phone,
		Timezone:   d.Timezone,
		Locale:     d.Locale,
		OpenTime:   d.OpenTime,
		CloseTime:  d.CloseTime,
		TaxPct:     float64(d.TaxBPS) / 100,
		ServicePct: float64(d.ServiceBPS) / 100,
		Status:     status,
		Managers:   managers,
		CreatedAt:  d.CreatedAt.UTC(),
		UpdatedAt:  d.UpdatedAt.UTC(),
	}
}
