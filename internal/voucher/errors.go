package voucher

import (
	"net/http"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Errors of the voucher endpoints, and of a sale that uses a voucher.
// Messages live in the i18n catalogs.
var (
	// ErrVoucherNotFound is a voucher ID or code of no voucher, or of a
	// deleted one.
	ErrVoucherNotFound = httpx.NewError(http.StatusNotFound, "voucher_not_found")
	// ErrVoucherCodeTaken is a code another voucher has.
	ErrVoucherCodeTaken = httpx.NewError(http.StatusConflict, "voucher_code_taken")
	// ErrVoucherUsed is deleting a voucher a sale has used.
	ErrVoucherUsed = httpx.NewError(http.StatusConflict, "voucher_used")

	// What keeps a voucher from a sale, at the cashier and when the sale is
	// taken.
	ErrVoucherInactive     = httpx.NewError(http.StatusConflict, "voucher_inactive")
	ErrVoucherNotStarted   = httpx.NewError(http.StatusConflict, "voucher_not_started")
	ErrVoucherExpired      = httpx.NewError(http.StatusConflict, "voucher_expired")
	ErrVoucherUsedUp       = httpx.NewError(http.StatusConflict, "voucher_used_up")
	ErrVoucherNotForOutlet = httpx.NewError(http.StatusConflict, "voucher_not_for_outlet")
	// ErrVoucherMinSpend is a sale whose subtotal is short of the voucher's
	// minimum spend; details.min_spend says how much it needs.
	ErrVoucherMinSpend = httpx.NewError(http.StatusConflict, "voucher_min_spend")
)
