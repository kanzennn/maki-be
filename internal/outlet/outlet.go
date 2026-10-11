// Package outlet holds the business's outlets, which only the Owner creates
// and changes. It owns the outlets table.
package outlet

import (
	"net/http"
	"time"

	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/user"
)

// Outlet is an outlet, as other modules see it.
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

// Detail is an outlet with everything the Owner sees and changes.
type Detail struct {
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
	Managers   []user.Manager
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Cashier is the outlet as its cashier needs it: the receipt's header, the
// rates, and the code and time zone its receipt numbers are made of.
type Cashier struct {
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

// Hours are when an outlet opens and closes, in its time zone: "15:04" each,
// both nil when it has no hours. Closing before opening means past midnight.
type Hours struct {
	Timezone string
	Open     *string
	Close    *string
}

// Ref names an outlet in another module's records, which keep it after it
// is deleted: a voucher's history, say.
type Ref struct {
	OutletID string
	Code     string
	Name     string
	// Deleted is whether the Owner has deleted the outlet since.
	Deleted bool
}

// Input is an outlet as the Owner fills it in.
type Input struct {
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

// Filter narrows the outlet list.
type Filter struct {
	Search string // part of the name or the code
	Status string // active, inactive, or empty for both
}

var (
	// ErrNotFound means there is no such outlet, or it was deleted.
	ErrNotFound = httpx.NewError(http.StatusNotFound, "outlet_not_found")
	// ErrCodeTaken is a code another outlet has, or had before it was
	// deleted: receipts print it, so it is never reused.
	ErrCodeTaken = httpx.NewError(http.StatusConflict, "outlet_code_taken")
	// ErrCodeLocked is changing the code of an outlet that has taken an
	// order: its receipt numbers carry the code.
	ErrCodeLocked = httpx.NewError(http.StatusConflict, "outlet_code_locked")
	// ErrStillActive is deleting an outlet that wasn't deactivated first.
	ErrStillActive = httpx.NewError(http.StatusConflict, "outlet_active")
	// ErrNameMismatch is deleting an outlet with a confirmation that isn't
	// its name.
	ErrNameMismatch = httpx.NewError(http.StatusUnprocessableEntity, "outlet_name_mismatch")
)
