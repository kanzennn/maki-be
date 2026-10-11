package order

import (
	"net/http"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Errors of the cashier's endpoints. Messages live in the i18n catalogs.
//
// An online sale is charged with the figures on the cashier's screen, so the
// server refuses it when they are no longer right, naming what changed: the
// cashier reloads its catalog and shows the new figures before charging
// again. A sale's voucher is refused with the voucher module's errors
// (voucher.ErrVoucherUsedUp and the rest): the cashier takes it off the
// order and says why.
var (
	// ErrOrderNotFound is an order that isn't the outlet's.
	ErrOrderNotFound = httpx.NewError(http.StatusNotFound, "order_not_found")
	// ErrOrderIDConflict is an order ID taken by another order: one with
	// other contents, or another outlet's. A resend of the same order is no
	// error; it gets the order back.
	ErrOrderIDConflict = httpx.NewError(http.StatusConflict, "order_id_conflict")
	// ErrOrderNoTaken is a receipt number another of the outlet's orders
	// has, as when the cashier is open in two tabs: the device takes the
	// next number from GET /pos/device and sends the order again.
	ErrOrderNoTaken = httpx.NewError(http.StatusConflict, "order_no_taken")
	// ErrMenuUnavailable is an order of menus no longer on sale: deleted,
	// inactive, or in an inactive category; details.menus lists their IDs.
	ErrMenuUnavailable = httpx.NewError(http.StatusConflict, "menu_unavailable")
	// ErrPricesChanged is an order at prices that aren't the menus' now;
	// details.menus lists each menu_id with its price.
	ErrPricesChanged = httpx.NewError(http.StatusConflict, "prices_changed")
	// ErrStockInsufficient is an order of more than a menu has in stock,
	// its lines together; details.menus lists each menu_id with what is
	// available.
	ErrStockInsufficient = httpx.NewError(http.StatusConflict, "order_stock_insufficient")
	// ErrTotalChanged is a total that isn't the server's, from the prices
	// and the outlet's rates now: a rate changed since the catalog was
	// loaded. details.total is the server's.
	ErrTotalChanged = httpx.NewError(http.StatusConflict, "total_changed")
	// ErrPaymentMethodUnavailable is a payment method the outlet's cashier
	// doesn't take.
	ErrPaymentMethodUnavailable = httpx.NewError(http.StatusConflict, "payment_method_unavailable")
	// ErrOrderAlreadyVoid is voiding an order that is void already.
	ErrOrderAlreadyVoid = httpx.NewError(http.StatusConflict, "order_already_void")
	// ErrOutletInactive is a sale in an outlet deactivated while it was on
	// its way.
	ErrOutletInactive = httpx.NewError(http.StatusForbidden, "outlet_inactive")
	// ErrDineInModeChanged is a sale that doesn't fit how the outlet seats
	// its dine-in customers now: at a table, or dine-in without its stand's
	// number, where they take number stands; at a stand where they sit at
	// tables. The Manager changed the mode since the cashier loaded its
	// catalog; details.mode is the outlet's.
	ErrDineInModeChanged = httpx.NewError(http.StatusConflict, "dine_in_mode_changed")
)
