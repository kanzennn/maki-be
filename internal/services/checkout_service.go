package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/validations"
)

// CheckoutService takes the orders the cashier charges for. The device
// makes each order's ID and receipt number, so the ID is also the order's
// idempotency key: a resend is the same order, never a second one, whether
// it comes seconds later or, with the offline milestone, days.
type CheckoutService struct {
	db       *config.Database
	orders   *repositories.OrderRepository
	sales    *OrderService
	menus    *MenuService
	devices  *DeviceService
	payments *PaymentService
	outlets  *OutletService
	vouchers *VoucherService
	tables   *TableService
	modes    *DineInModeService
	audit    *AuditService
	now      func() time.Time
}

// NewCheckoutService returns the checkout's service. sales reads back the
// orders it takes; now is the server's clock, which the device's is checked
// against.
func NewCheckoutService(db *config.Database, orders *repositories.OrderRepository, sales *OrderService, menus *MenuService,
	devices *DeviceService, payments *PaymentService, outlets *OutletService, vouchers *VoucherService, tables *TableService,
	modes *DineInModeService, audit *AuditService, now func() time.Time,
) *CheckoutService {
	return &CheckoutService{
		db: db, orders: orders, sales: sales, menus: menus, devices: devices, payments: payments, outlets: outlets,
		vouchers: vouchers, tables: tables, modes: modes, audit: audit, now: now,
	}
}

// Create takes an order the cashier charged for, from one of the outlet's
// devices (token), and returns it with created true. A resend of an order
// taken already returns it as it was, created false, even from a device
// revoked since; its ID with anything else in it is ErrOrderIDConflict.
//
// Everything is checked in one transaction, against the outlet as it is:
// the device is the outlet's and in use; the receipt number is the device's,
// of the order's day, and not taken; the payment method is on; each menu is
// on sale, at the price shown, with stock enough; the voucher, when there is
// one, is for the sale and has a use left, which the sale takes; the order
// fits the outlet's dine-in mode, at a table or at a stand; the table, when
// there is one, can be sat at; and the total is the server's, the voucher's
// discount off. Then the order, its lines and its payment are saved, the
// stock goes down, and the Log Action records order.created.
func (s *CheckoutService) Create(ctx context.Context, p models.Principal, ip, outletID, token string, in models.NewOrder) (models.Order, bool, error) {
	c, err := validations.CheckOrder(in, s.now())
	if err != nil {
		return models.Order{}, false, err
	}
	hash, err := orderHash(c)
	if err != nil {
		return models.Order{}, false, err
	}
	if o, ok, err := s.resent(ctx, outletID, c.OrderID, hash); err != nil || ok {
		return o, false, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		return s.take(ctx, p, ip, outletID, token, c, hash)
	})
	// A resend got there first: the sale answers with its order.
	if errors.Is(err, apperror.ErrOrderIDTaken) {
		o, ok, err := s.resent(ctx, outletID, c.OrderID, hash)
		if err == nil && !ok {
			err = fmt.Errorf("order %s was taken, then not found", c.OrderID)
		}
		return o, false, err
	}
	if err != nil {
		return models.Order{}, false, err
	}
	o, err := s.sales.Order(ctx, outletID, c.OrderID)
	return o, true, err
}

// resent returns the order under the ID, when the request is a resend of
// it; ok is false when no order has the ID. One that has it with anything
// else in it, or in another outlet, is ErrOrderIDConflict.
func (s *CheckoutService) resent(ctx context.Context, outletID, orderID, hash string) (models.Order, bool, error) {
	owner, sent, err := s.orders.Find(ctx, orderID)
	if errors.Is(err, apperror.ErrOrderNotFound) {
		return models.Order{}, false, nil
	}
	if err != nil {
		return models.Order{}, false, err
	}
	if owner != outletID || sent != hash {
		return models.Order{}, false, apperror.ErrOrderIDConflict
	}
	o, err := s.sales.Order(ctx, outletID, orderID)
	return o, err == nil, err
}

// take is Create's transaction, the one ctx carries.
func (s *CheckoutService) take(ctx context.Context, p models.Principal, ip, outletID, token string, c models.CheckedOrder, hash string) error {
	// The device first. It is held until the sale commits, so a revocation
	// waits for the sale, and the device's sales go one at a time.
	d, err := s.devices.Hold(ctx, outletID, token)
	if err != nil {
		return err
	}
	// A resend sent while the first was on its way waited for the device,
	// and finds its order now.
	if _, _, err := s.orders.Find(ctx, c.OrderID); err == nil {
		return apperror.ErrOrderIDTaken
	} else if !errors.Is(err, apperror.ErrOrderNotFound) {
		return err
	}
	o, err := s.outlets.HoldForSale(ctx, outletID)
	if err != nil {
		return err
	}
	if !o.Active {
		return apperror.ErrOutletInactive
	}
	if err := checkOrderNumber(c, o, d); err != nil {
		return err
	}
	accepted, err := s.payments.Accepts(ctx, outletID, c.Method)
	if err != nil {
		return err
	}
	if !accepted {
		return apperror.ErrPaymentMethodUnavailable
	}
	menus, err := s.menus.LockForSale(ctx, outletID, c.MenuIDs())
	if err != nil {
		return err
	}
	if err := checkSaleMenus(c, menus); err != nil {
		return err
	}
	// The voucher after the menus: two sales after its last use, from
	// outlets apart, wait for each other only there.
	var used models.VoucherTaken
	if c.VoucherCode != "" {
		if used, err = s.vouchers.Redeem(ctx, outletID, c.VoucherCode, c.Subtotal()); err != nil {
			return err
		}
	}
	// How the outlet seats its customers, held until the sale commits: a
	// change of it waits for this one.
	mode, err := s.modes.HoldDineInMode(ctx, outletID)
	if err != nil {
		return err
	}
	if err := checkDineInMode(c, mode); err != nil {
		return err
	}
	// The table last, shared with the other sales at it: a change, a release
	// or a delete of it waits for this one.
	var seat models.Seat
	if c.TableID != "" {
		if seat, err = s.tables.HoldForSale(ctx, outletID, c.TableID); err != nil {
			return err
		}
	}
	t := ComputeTotals(c.TotalLines(), used.Discount, o.ServiceBPS, o.TaxBPS)
	if t.Total != c.Total {
		return apperror.ErrTotalChanged.WithDetails(map[string]any{"total": t.Total})
	}
	if err := s.orders.Insert(ctx, orderRecord(p.UserID, outletID, d.DeviceID, c, hash, o, menus, used, seat, t)); err != nil {
		return err
	}
	if err := s.menus.RecordSale(ctx, p.UserID, outletID, c.OrderID, c.Sold()); err != nil {
		return err
	}
	after := map[string]any{"order_no": c.OrderNo, "type": c.Type, "total": t.Total, "payment_method": c.Method}
	if used.VoucherID != "" {
		after["voucher_code"], after["voucher_discount"] = used.Code, used.Discount
	}
	if seat.TableID != "" {
		after["table"] = seat.Name
	}
	if c.StandNumber != "" {
		after["stand_number"] = c.StandNumber
	}
	return s.audit.Record(ctx, models.AuditEntry{
		Action: "order.created", UserID: p.UserID, OutletID: outletID, DeviceID: d.DeviceID,
		TargetType: "order", TargetID: c.OrderID, After: after, IP: ip,
	})
}

// orderRecord is the order as the sale saves it: with the voucher it used
// and its table, if any; its lines, with the menus' names as they are; and
// its payment.
func orderRecord(cashierID, outletID, deviceID string, c models.CheckedOrder, hash string, o models.OutletCashier,
	menus map[string]models.SaleMenu, used models.VoucherTaken, seat models.Seat, t models.OrderTotals,
) models.OrderRecord {
	rec := models.OrderRecord{
		OrderID: c.OrderID, OutletID: outletID, DeviceID: deviceID, OrderNo: c.OrderNo, Type: c.Type,
		CashierID: cashierID, OrderTotals: t, ServiceBPS: o.ServiceBPS, TaxBPS: o.TaxBPS,
		ClientCreatedAt: c.CreatedAt, RequestHash: hash,
		Items:     make([]models.OrderItem, len(c.Lines)),
		PaymentID: id.New(),
		Payment:   models.OrderPayment{Method: c.Method, Amount: t.Total},
	}
	if used.VoucherID != "" {
		rec.VoucherID, rec.VoucherCode = &used.VoucherID, &used.Code
	}
	if seat.TableID != "" {
		rec.TableID, rec.TableName = &seat.TableID, &seat.Name
	}
	if c.StandNumber != "" {
		rec.StandNumber = &c.StandNumber
	}
	for i, l := range c.Lines {
		rec.Items[i] = models.OrderItem{
			OrderItemID: id.New(), MenuID: l.MenuID, MenuName: menus[l.MenuID].Name, Price: l.Price, Qty: l.Qty,
			Note: l.Note, LineTotal: l.Price * int64(l.Qty),
		}
	}
	if c.Method == models.PaymentCash {
		change := *c.Received - t.Total
		rec.Payment.Received, rec.Payment.Change = c.Received, &change
	}
	return rec
}

// orderHash is the order as the device sent it, checked, as SHA-256 in hex:
// the same order sent again has the same hash, and any other order another.
func orderHash(c models.CheckedOrder) (string, error) {
	type hashedLine struct {
		MenuID string  `json:"menu_id"`
		Qty    int     `json:"qty"`
		Price  int64   `json:"price"`
		Note   *string `json:"note"`
	}
	lines := make([]hashedLine, len(c.Lines))
	for i, l := range c.Lines {
		lines[i] = hashedLine{MenuID: l.MenuID, Qty: l.Qty, Price: l.Price, Note: l.Note}
	}
	b, err := json.Marshal(struct {
		OrderID         string       `json:"order_id"`
		OrderNo         string       `json:"order_no"`
		Type            string       `json:"type"`
		ClientCreatedAt time.Time    `json:"client_created_at"`
		Items           []hashedLine `json:"items"`
		// Left out without a voucher, so the orders taken before vouchers
		// keep their hashes.
		VoucherCode string `json:"voucher_code,omitempty"`
		Total       int64  `json:"total"`
		Method      string `json:"method"`
		Received    *int64 `json:"received"`
		// Left out without a table, as the voucher is; the stand too.
		TableID     string `json:"table_id,omitempty"`
		StandNumber string `json:"stand_number,omitempty"`
	}{c.OrderID, c.OrderNo, c.Type, c.CreatedAt, lines, c.VoucherCode, c.Total, c.Method, c.Received, c.TableID,
		c.StandNumber})
	if err != nil {
		return "", fmt.Errorf("hash order %s: %w", c.OrderID, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// checkDineInMode checks the order against how the outlet seats its dine-in
// customers: where they take number stands, a dine-in order carries its
// stand's number, and so no table (CheckOrder allows one or the other);
// where they sit at tables, no number.
func checkDineInMode(c models.CheckedOrder, mode models.DineInMode) error {
	fits := c.StandNumber == ""
	if mode == models.ModeNumber {
		fits = c.Type != models.OrderDineIn || c.StandNumber != ""
	}
	if !fits {
		return apperror.ErrDineInModeChanged.WithDetails(map[string]any{"mode": string(mode)})
	}
	return nil
}

// checkOrderNumber checks the receipt number against the device and its
// outlet: it starts with their codes, and carries the outlet's day the order
// was made on, which starts the device's count again each day.
func checkOrderNumber(c models.CheckedOrder, o models.OutletCashier, d models.Device) error {
	if c.Prefix != o.Code+"-"+d.Code {
		return apperror.Invalid(map[string]string{"order_no": "prefix"})
	}
	start, _, err := outletDay(c.CreatedAt, o.Timezone)
	if err != nil {
		return err
	}
	if c.Day != start.Format("20060102") {
		return apperror.Invalid(map[string]string{"order_no": "date"})
	}
	return nil
}

// checkSaleMenus checks the order against its menus, locked: each is still
// on sale (ErrMenuUnavailable), at the price the cashier showed
// (ErrPricesChanged), and with stock enough for its lines together
// (ErrOrderStockInsufficient). Each error names every menu it is about.
func checkSaleMenus(c models.CheckedOrder, menus map[string]models.SaleMenu) error {
	var unavailable []string
	for _, menuID := range c.MenuIDs() {
		if m, ok := menus[menuID]; !ok || !m.Available {
			unavailable = append(unavailable, menuID)
		}
	}
	if len(unavailable) > 0 {
		return apperror.ErrMenuUnavailable.WithDetails(map[string]any{"menus": unavailable})
	}
	var changed []map[string]any
	for _, menuID := range c.MenuIDs() {
		m := menus[menuID]
		if slices.ContainsFunc(c.Lines, func(l models.CheckedLine) bool { return l.MenuID == menuID && l.Price != m.Price }) {
			changed = append(changed, map[string]any{"menu_id": menuID, "price": m.Price})
		}
	}
	if len(changed) > 0 {
		return apperror.ErrPricesChanged.WithDetails(map[string]any{"menus": changed})
	}
	var short []map[string]any
	for _, s := range c.Sold() {
		if m := menus[s.MenuID]; m.TrackStock && s.Qty > m.StockQty {
			short = append(short, map[string]any{"menu_id": s.MenuID, "available": max(m.StockQty, 0)})
		}
	}
	if len(short) > 0 {
		return apperror.ErrOrderStockInsufficient.WithDetails(map[string]any{"menus": short})
	}
	return nil
}

// ComputeTotals works out an order's totals, the same way the web and
// Android cashiers do (api/test-vectors/order-totals.json) and the schema
// checks: the lines add up to the subtotal; the discount comes off it; the
// service charge is on what is left, and the tax on that plus the service,
// each rounded to the nearest rupiah, half up. The rates are in basis
// points. The caller has checked the amounts, so none is negative and
// nothing overflows: a subtotal of at most MaxSubtotal, at most 100% rates.
func ComputeTotals(lines []models.OrderLine, discount int64, serviceBps, taxBps int) models.OrderTotals {
	var t models.OrderTotals
	for _, l := range lines {
		t.Subtotal += l.Price * int64(l.Qty)
	}
	t.Discount = discount
	base := t.Subtotal - discount
	t.Service = share(base, serviceBps)
	t.Tax = share(base+t.Service, taxBps)
	t.Total = base + t.Service + t.Tax
	return t
}

// share is bps basis points of amount, to the nearest rupiah, half up.
func share(amount int64, bps int) int64 {
	return (amount*int64(bps) + 5000) / 10000
}
