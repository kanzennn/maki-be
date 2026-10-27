package apperror

import (
	"errors"
	"net/http"
)

// The catalog: the errors the services and the utilities return, by what
// they are about. Coded errors reach the client with their status and
// code, and the message the i18n catalogs hold for "error.<code>"; plain
// ones are for the services that call each other, which decide what they
// mean to the client. The middleware's own answers (unauthorized,
// forbidden, not_found and the like) are written where they happen.

// Signing in, sessions, and the account's credentials.
var (
	// ErrInvalidCredentials is a wrong password — or an unknown username,
	// which gets the same answer so it can't be told apart.
	ErrInvalidCredentials = NewError(http.StatusUnauthorized, "invalid_credentials")
	// ErrAccountDisabled is the right password for a deactivated account.
	ErrAccountDisabled = NewError(http.StatusForbidden, "account_disabled")
	// ErrOutletInactive is the right password for an account whose outlet
	// the Owner has deactivated or deleted, or a sale in an outlet
	// deactivated while it was on its way.
	ErrOutletInactive = NewError(http.StatusForbidden, "outlet_inactive")
	// ErrUnauthorized is a request without a live session, or a refresh
	// token that is missing, unknown, expired, revoked, or already replaced.
	ErrUnauthorized = NewError(http.StatusUnauthorized, "unauthorized")
	// ErrRefreshSuperseded is a refresh token replaced a moment ago by
	// another request, such as a second browser tab; retrying works.
	ErrRefreshSuperseded = NewError(http.StatusConflict, "refresh_superseded")
	// ErrWrongPassword is a wrong current password when changing the
	// password or the email address.
	ErrWrongPassword = NewError(http.StatusUnprocessableEntity, "wrong_password")
	// ErrTokenInvalid is an emailed link's token that is unknown, used or
	// expired.
	ErrTokenInvalid = NewError(http.StatusBadRequest, "token_invalid")
	// ErrEmailUnchanged is a request to change the email to the one the
	// account already has.
	ErrEmailUnchanged = NewError(http.StatusUnprocessableEntity, "email_unchanged")
)

// A new password that breaks the rules, which password.Check returns with
// the limit in its details.
var (
	ErrPasswordTooShort       = NewError(http.StatusUnprocessableEntity, "password_too_short")
	ErrPasswordTooLong        = NewError(http.StatusUnprocessableEntity, "password_too_long")
	ErrPasswordSameAsUsername = NewError(http.StatusUnprocessableEntity, "password_same_as_username")
)

// ErrRateLimited is too many attempts at an endpoint an attacker would
// hammer; ratelimit.Limited adds how long to wait.
var ErrRateLimited = NewError(http.StatusTooManyRequests, "rate_limited")

// Accounts.
var (
	// ErrNoAccount means there is no such account, or it was deleted.
	ErrNoAccount = errors.New("user: not found")
	// ErrOwnerExists means the Owner account exists already: there is only
	// ever one.
	ErrOwnerExists = errors.New("an Owner account already exists; there is only ever one")
)

// Outlets.
var (
	// ErrOutletNotFound means there is no such outlet, or it was deleted.
	ErrOutletNotFound = NewError(http.StatusNotFound, "outlet_not_found")
	// ErrOutletCodeTaken is a code another outlet has, or had before it was
	// deleted: receipts print it, so it is never reused.
	ErrOutletCodeTaken = NewError(http.StatusConflict, "outlet_code_taken")
	// ErrOutletCodeLocked is changing the code of an outlet that has taken
	// an order: its receipt numbers carry the code.
	ErrOutletCodeLocked = NewError(http.StatusConflict, "outlet_code_locked")
	// ErrOutletStillActive is deleting an outlet that wasn't deactivated
	// first.
	ErrOutletStillActive = NewError(http.StatusConflict, "outlet_active")
	// ErrOutletNameMismatch is deleting an outlet with a confirmation that
	// isn't its name.
	ErrOutletNameMismatch = NewError(http.StatusUnprocessableEntity, "outlet_name_mismatch")
)

// Roles.
var (
	// ErrRoleNotFound is a role that doesn't exist in the outlet, or was
	// deleted.
	ErrRoleNotFound = NewError(http.StatusNotFound, "role_not_found")
	// ErrRoleNameTaken is a name another role of the outlet has.
	ErrRoleNameTaken = NewError(http.StatusConflict, "role_name_taken")
	// ErrRoleInUse is deleting a role users still hold; details.users names
	// them.
	ErrRoleInUse = NewError(http.StatusConflict, "role_in_use")
	// ErrPermissionNotHeld is granting a permission the caller doesn't hold
	// themselves, or managing a user who holds one; details.permissions names
	// them.
	ErrPermissionNotHeld = NewError(http.StatusForbidden, "permission_not_held")
)

// Staff accounts.
var (
	// ErrUserNotFound is a user who isn't in the outlet, or was deleted.
	ErrUserNotFound = NewError(http.StatusNotFound, "user_not_found")
	// ErrUsernameTaken is a username someone has, or had before they were
	// deleted: a username is never used again.
	ErrUsernameTaken = NewError(http.StatusConflict, "username_taken")
	// ErrOwnAccount is managing one's own account through the staff
	// endpoints; one's own settings go through /me.
	ErrOwnAccount = NewError(http.StatusForbidden, "own_account")
	// ErrManagerAccount is anyone but the Owner creating, changing or
	// promoting to a Manager account.
	ErrManagerAccount = NewError(http.StatusForbidden, "manager_account")
	// ErrOwnerManagesManagers is the Owner managing a staff account, which
	// is the outlet's Managers' job; promoting one is the exception.
	ErrOwnerManagesManagers = NewError(http.StatusForbidden, "owner_manages_managers")
)

// Menu categories.
var (
	// ErrCategoryNotFound is a category that isn't the outlet's, or was
	// deleted.
	ErrCategoryNotFound = NewError(http.StatusNotFound, "category_not_found")
	// ErrCategoryNameTaken is a name another category of the outlet has.
	ErrCategoryNameTaken = NewError(http.StatusConflict, "category_name_taken")
	// ErrCategoryInUse is deleting a category that still holds menus;
	// details.menus counts them.
	ErrCategoryInUse = NewError(http.StatusConflict, "category_in_use")
	// ErrCategoriesChanged is a new order that doesn't list the outlet's
	// categories as they are now, each once: one was added or deleted since
	// the list was read.
	ErrCategoriesChanged = NewError(http.StatusConflict, "categories_changed")
)

// Menus and their stock.
var (
	// ErrMenuNotFound is a menu that isn't the outlet's, or was deleted.
	ErrMenuNotFound = NewError(http.StatusNotFound, "menu_not_found")
	// ErrMenuSKUTaken is a SKU another menu of the outlet has.
	ErrMenuSKUTaken = NewError(http.StatusConflict, "menu_sku_taken")
	// ErrStockInsufficient is taking away more than the count;
	// details.available is what there is.
	ErrStockInsufficient = NewError(http.StatusConflict, "stock_insufficient")
	// ErrStockNotTracked is adjusting, or setting the alert of, a menu whose
	// stock isn't tracked.
	ErrStockNotTracked = NewError(http.StatusConflict, "stock_not_tracked")
)

// Dining areas and their layouts.
var (
	// ErrAreaNotFound is an area ID of no area of the outlet, or of a deleted
	// one.
	ErrAreaNotFound = NewError(http.StatusNotFound, "area_not_found")
	// ErrAreaNameTaken is a name another of the outlet's areas has.
	ErrAreaNameTaken = NewError(http.StatusConflict, "area_name_taken")
	// ErrAreaNotEmpty is deleting an area that still holds tables;
	// details.tables says how many.
	ErrAreaNotEmpty = NewError(http.StatusConflict, "area_not_empty")
	// ErrAreasChanged is putting the areas in an order that doesn't list
	// each of them once: the caller's list is out of date.
	ErrAreasChanged = NewError(http.StatusConflict, "areas_changed")
	// ErrLayoutChanged is saving a layout on a version the area has moved
	// past: its layout or one of its tables changed since the editor opened
	// it. details.version is the area's now.
	ErrLayoutChanged = NewError(http.StatusConflict, "layout_changed")
)

// Tables.
var (
	// ErrTableNotFound is a table ID of no table of the outlet, or of a
	// deleted one.
	ErrTableNotFound = NewError(http.StatusNotFound, "table_not_found")
	// ErrTableNameTaken is a name another of the outlet's tables has.
	ErrTableNameTaken = NewError(http.StatusConflict, "table_name_taken")
	// ErrTableOccupied is deleting a table customers are seated at: the
	// cashier releases it first.
	ErrTableOccupied = NewError(http.StatusConflict, "table_occupied")
	// ErrTableUnavailable is a sale at a table the cashier can't seat it at:
	// details.status is the table's, inactive or scheduled, or missing for
	// one deleted or not the outlet's.
	ErrTableUnavailable = NewError(http.StatusConflict, "table_unavailable")
)

// Table schedules.
var (
	// ErrScheduleNotFound is a schedule ID of no schedule of the outlet.
	ErrScheduleNotFound = NewError(http.StatusNotFound, "schedule_not_found")
	// ErrScheduleClosed is changing or cancelling a schedule that is over:
	// finished, or cancelled already.
	ErrScheduleClosed = NewError(http.StatusConflict, "schedule_closed")
	// ErrScheduleOverlaps is a schedule that has some of its tables out of
	// service while another has them out too; details.schedules names those,
	// and the tables they share. A warning, not a refusal: the schedule is
	// saved with confirm_overlap.
	ErrScheduleOverlaps = NewError(http.StatusConflict, "schedule_overlaps")
)

// Vouchers, and a sale that uses one.
var (
	// ErrVoucherNotFound is a voucher ID or code of no voucher, or of a
	// deleted one.
	ErrVoucherNotFound = NewError(http.StatusNotFound, "voucher_not_found")
	// ErrVoucherCodeTaken is a code another voucher has.
	ErrVoucherCodeTaken = NewError(http.StatusConflict, "voucher_code_taken")
	// ErrVoucherUsed is deleting a voucher a sale has used.
	ErrVoucherUsed = NewError(http.StatusConflict, "voucher_used")

	// What keeps a voucher from a sale, at the cashier and when the sale is
	// taken.
	ErrVoucherInactive     = NewError(http.StatusConflict, "voucher_inactive")
	ErrVoucherNotStarted   = NewError(http.StatusConflict, "voucher_not_started")
	ErrVoucherExpired      = NewError(http.StatusConflict, "voucher_expired")
	ErrVoucherUsedUp       = NewError(http.StatusConflict, "voucher_used_up")
	ErrVoucherNotForOutlet = NewError(http.StatusConflict, "voucher_not_for_outlet")
	// ErrVoucherMinSpend is a sale whose subtotal is short of the voucher's
	// minimum spend; details.min_spend says how much it needs.
	ErrVoucherMinSpend = NewError(http.StatusConflict, "voucher_min_spend")
)

// Devices, and the cashier's endpoints, which check the device each request
// comes from.
var (
	// ErrDeviceNotFound is a device that isn't the outlet's.
	ErrDeviceNotFound = NewError(http.StatusNotFound, "device_not_found")
	// ErrDeviceNotRegistered is a request from a device the outlet hasn't
	// activated: without a device token, with an unknown one, or with
	// another outlet's.
	ErrDeviceNotRegistered = NewError(http.StatusForbidden, "device_not_registered")
	// ErrDeviceRevoked is a request from a device that was revoked.
	ErrDeviceRevoked = NewError(http.StatusForbidden, "device_revoked")
	// ErrDeviceActive is activating a device that is one of the outlet's
	// already, and in use.
	ErrDeviceActive = NewError(http.StatusConflict, "device_already_active")
)

// Orders: the cashier's sales and their voids.
//
// An online sale is charged with the figures on the cashier's screen, so the
// server refuses it when they are no longer right, naming what changed: the
// cashier reloads its catalog and shows the new figures before charging
// again. A sale's voucher is refused with the voucher errors
// (ErrVoucherUsedUp and the rest): the cashier takes it off the order and
// says why. A sale in an outlet deactivated while it was on its way is
// ErrOutletInactive.
var (
	// ErrOrderNotFound is an order that isn't the outlet's.
	ErrOrderNotFound = NewError(http.StatusNotFound, "order_not_found")
	// ErrOrderIDConflict is an order ID taken by another order: one with
	// other contents, or another outlet's. A resend of the same order is no
	// error; it gets the order back.
	ErrOrderIDConflict = NewError(http.StatusConflict, "order_id_conflict")
	// ErrOrderIDTaken means an order with the ID was saved first, by a
	// resend that got there before this one: the sale answers with that
	// order, or ErrOrderIDConflict.
	ErrOrderIDTaken = errors.New("order: ID taken")
	// ErrOrderNoTaken is a receipt number another of the outlet's orders
	// has, as when the cashier is open in two tabs: the device takes the
	// next number from GET /pos/device and sends the order again.
	ErrOrderNoTaken = NewError(http.StatusConflict, "order_no_taken")
	// ErrMenuUnavailable is an order of menus no longer on sale: deleted,
	// inactive, or in an inactive category; details.menus lists their IDs.
	ErrMenuUnavailable = NewError(http.StatusConflict, "menu_unavailable")
	// ErrPricesChanged is an order at prices that aren't the menus' now;
	// details.menus lists each menu_id with its price.
	ErrPricesChanged = NewError(http.StatusConflict, "prices_changed")
	// ErrOrderStockInsufficient is an order of more than a menu has in
	// stock, its lines together; details.menus lists each menu_id with what
	// is available.
	ErrOrderStockInsufficient = NewError(http.StatusConflict, "order_stock_insufficient")
	// ErrTotalChanged is a total that isn't the server's, from the prices
	// and the outlet's rates now: a rate changed since the catalog was
	// loaded. details.total is the server's.
	ErrTotalChanged = NewError(http.StatusConflict, "total_changed")
	// ErrPaymentMethodUnavailable is a payment method the outlet's cashier
	// doesn't take.
	ErrPaymentMethodUnavailable = NewError(http.StatusConflict, "payment_method_unavailable")
	// ErrOrderAlreadyVoid is voiding an order that is void already.
	ErrOrderAlreadyVoid = NewError(http.StatusConflict, "order_already_void")
	// ErrDineInModeChanged is a sale that doesn't fit how the outlet seats
	// its dine-in customers now: at a table, or dine-in without its stand's
	// number, where they take number stands; at a stand where they sit at
	// tables. The Manager changed the mode since the cashier loaded its
	// catalog; details.mode is the outlet's.
	ErrDineInModeChanged = NewError(http.StatusConflict, "dine_in_mode_changed")
)
