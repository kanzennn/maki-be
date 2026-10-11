package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/order/store"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/payment"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/table"
	"github.com/kervzent/maki-be/internal/voucher"
)

// Limits of an order, the schema's own and the cashier's.
const (
	MaxLines = 100 // lines in one order
	MaxQty   = 999 // of one line
	MaxNote  = 100 // characters in a line's note
	// MaxReceived is the most cash one payment takes in, in Rupiah.
	MaxReceived = 100_000_000_000

	// How far the device's clock may be from the server's for an order sent
	// online: a little ahead, or a day behind, for a resend.
	maxAhead  = 5 * time.Minute
	maxBehind = 24 * time.Hour
)

// NewOrder is an order as the device sends it. The device makes its ID and
// receipt number; every figure in it is checked against the menus and the
// outlet's rates as they are when the server takes it. Nil is a field left
// out.
type NewOrder struct {
	OrderID string
	// OrderNo is OUTLETCODE-DEVICECODE-YYYYMMDD-0001: the device's codes,
	// the outlet's day of ClientCreatedAt, and the device's count that day.
	OrderNo string
	Type    string
	// TableID is the table a dine-in order seats its customers at; nil or
	// blank for none.
	TableID *string
	// StandNumber is the number on the stand a dine-in customer took, in an
	// outlet that seats them by number stands; nil or blank for none.
	StandNumber     *string
	ClientCreatedAt *time.Time
	Items           []NewItem
	// VoucherCode is the voucher the customer gave, one per order; nil or
	// blank for none.
	VoucherCode *string
	// Total is what the cashier charged.
	Total   *int64
	Payment NewPayment
}

// NewItem is a line of a NewOrder. Price is the menu's, as the cashier
// showed it.
type NewItem struct {
	MenuID string
	Qty    *int
	Price  *int64
	Note   *string
}

// NewPayment is how a NewOrder is paid. Received is cash's: what the
// customer handed over.
type NewPayment struct {
	Method   string
	Received *int64
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
// discount off. Then the order, its lines and its payment are
// saved, the stock goes down, and the Log Action records order.created.
func (s *Service) Create(ctx context.Context, p authn.Principal, ip, outletID, token string, in NewOrder) (Order, bool, error) {
	c, err := checkOrder(in, s.now())
	if err != nil {
		return Order{}, false, err
	}
	hash, err := c.hash()
	if err != nil {
		return Order{}, false, err
	}
	if o, ok, err := s.resent(ctx, outletID, c.orderID, hash); err != nil || ok {
		return o, false, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		return s.take(ctx, tx, p, ip, outletID, token, c, hash)
	})
	if errors.Is(err, errResent) {
		o, ok, err := s.resent(ctx, outletID, c.orderID, hash)
		if err == nil && !ok {
			err = fmt.Errorf("order %s was taken, then not found", c.orderID)
		}
		return o, false, err
	}
	if err != nil {
		return Order{}, false, err
	}
	o, err := s.Order(ctx, outletID, c.orderID)
	return o, true, err
}

// errResent is a sale that finds its order taken already, by a resend that
// got there first. Create answers with that order.
var errResent = errors.New("order: taken by a resend")

// resent returns the order under the ID, when the request is a resend of
// it; ok is false when no order has the ID. One that has it with anything
// else in it, or in another outlet, is ErrOrderIDConflict.
func (s *Service) resent(ctx context.Context, outletID, orderID, hash string) (Order, bool, error) {
	r, err := store.New(s.pool).FindOrder(ctx, orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, false, nil
	}
	if err != nil {
		return Order{}, false, fmt.Errorf("find order %s: %w", orderID, err)
	}
	if r.OutletID != outletID || r.RequestHash != hash {
		return Order{}, false, ErrOrderIDConflict
	}
	o, err := s.Order(ctx, outletID, orderID)
	return o, err == nil, err
}

// take is Create's transaction.
func (s *Service) take(ctx context.Context, tx pgx.Tx, p authn.Principal, ip, outletID, token string, c checked, hash string) error {
	// The device first. It is held until the sale commits, so a revocation
	// waits for the sale, and the device's sales go one at a time.
	d, err := s.devices.Hold(ctx, tx, outletID, token)
	if err != nil {
		return err
	}
	q := store.New(tx)
	// A resend sent while the first was on its way waited for the device,
	// and finds its order now.
	if _, err := q.FindOrder(ctx, c.orderID); err == nil {
		return errResent
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("find order %s: %w", c.orderID, err)
	}
	o, err := s.outlets.HoldForSale(ctx, tx, outletID)
	if err != nil {
		return err
	}
	if !o.Active {
		return ErrOutletInactive
	}
	if err := c.checkNumber(o, d); err != nil {
		return err
	}
	accepted, err := s.payments.Accepts(ctx, tx, outletID, c.method)
	if err != nil {
		return err
	}
	if !accepted {
		return ErrPaymentMethodUnavailable
	}
	menus, err := s.menus.LockForSale(ctx, tx, outletID, c.menuIDs())
	if err != nil {
		return err
	}
	if err := c.checkMenus(menus); err != nil {
		return err
	}
	// The voucher after the menus: two sales after its last use, from
	// outlets apart, wait for each other only there.
	var used voucher.Taken
	if c.voucherCode != "" {
		if used, err = s.vouchers.Redeem(ctx, tx, outletID, c.voucherCode, c.subtotal()); err != nil {
			return err
		}
	}
	// How the outlet seats its customers, held until the sale commits: a
	// change of it waits for this one.
	mode, err := s.tables.HoldDineInMode(ctx, tx, outletID)
	if err != nil {
		return err
	}
	if err := c.checkMode(mode); err != nil {
		return err
	}
	// The table last, shared with the other sales at it: a change, a release
	// or a delete of it waits for this one.
	var seat table.Seat
	if c.tableID != "" {
		if seat, err = s.tables.HoldForSale(ctx, tx, outletID, c.tableID); err != nil {
			return err
		}
	}
	t := ComputeTotals(c.totalLines(), used.Discount, o.ServiceBPS, o.TaxBPS)
	if t.Total != c.total {
		return ErrTotalChanged.WithDetails(map[string]any{"total": t.Total})
	}
	if err := insert(ctx, q, p.UserID, outletID, d.DeviceID, c, hash, o, menus, used, seat, t); err != nil {
		return err
	}
	if err := s.menus.RecordSale(ctx, tx, p.UserID, outletID, c.orderID, c.sold()); err != nil {
		return err
	}
	after := map[string]any{"order_no": c.orderNo, "type": c.kind, "total": t.Total, "payment_method": c.method}
	if used.VoucherID != "" {
		after["voucher_code"], after["voucher_discount"] = used.Code, used.Discount
	}
	if seat.TableID != "" {
		after["table"] = seat.Name
	}
	if c.standNumber != "" {
		after["stand_number"] = c.standNumber
	}
	return s.audit.Record(ctx, tx, audit.Entry{
		Action: "order.created", UserID: p.UserID, OutletID: outletID, DeviceID: d.DeviceID,
		TargetType: "order", TargetID: c.orderID, After: after, IP: ip,
	})
}

// insert saves the order, with the voucher it used and its table, if any;
// its lines, with the menus' names as they are; and its payment.
func insert(ctx context.Context, q *store.Queries, cashierID, outletID, deviceID string, c checked, hash string,
	o outlet.Cashier, menus map[string]menu.SaleMenu, used voucher.Taken, seat table.Seat, t Totals,
) error {
	params := store.InsertOrderParams{
		OrderID: c.orderID, OutletID: outletID, DeviceID: deviceID, OrderNo: c.orderNo, Type: c.kind,
		CashierID: cashierID, Subtotal: t.Subtotal, VoucherDiscount: t.Discount,
		ServiceBps: int32(o.ServiceBPS), Service: t.Service, //nolint:gosec // a rate is 0 to 10000
		TaxBps: int32(o.TaxBPS), Tax: t.Tax, Total: t.Total, //nolint:gosec // as above
		ClientCreatedAt: c.createdAt, RequestHash: hash,
	}
	if used.VoucherID != "" {
		params.VoucherID, params.VoucherCode = &used.VoucherID, &used.Code
	}
	if seat.TableID != "" {
		params.TableID, params.TableName = &seat.TableID, &seat.Name
	}
	if c.standNumber != "" {
		params.StandNumber = &c.standNumber
	}
	err := q.InsertOrder(ctx, params)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "orders_pkey":
			return errResent
		case "orders_outlet_order_no_key":
			return ErrOrderNoTaken
		}
	}
	if err != nil {
		return fmt.Errorf("insert order %s: %w", c.orderID, err)
	}
	items := store.InsertOrderItemsParams{OutletID: outletID, OrderID: c.orderID}
	for i, l := range c.lines {
		note := ""
		if l.note != nil {
			note = *l.note
		}
		items.OrderItemIds = append(items.OrderItemIds, id.New())
		items.Positions = append(items.Positions, int32(i)) //nolint:gosec // at most MaxLines
		items.MenuIds = append(items.MenuIds, l.menuID)
		items.MenuNames = append(items.MenuNames, menus[l.menuID].Name)
		items.Prices = append(items.Prices, l.price)
		items.Qtys = append(items.Qtys, int32(l.qty)) //nolint:gosec // at most MaxQty
		items.Notes = append(items.Notes, note)
		items.LineTotals = append(items.LineTotals, l.price*int64(l.qty))
	}
	if err := q.InsertOrderItems(ctx, items); err != nil {
		return fmt.Errorf("insert the lines of order %s: %w", c.orderID, err)
	}
	pay := store.InsertPaymentParams{
		PaymentID: id.New(), OutletID: outletID, OrderID: c.orderID, Method: c.method, Amount: t.Total,
	}
	if c.method == payment.Cash {
		change := *c.received - t.Total
		pay.Received, pay.Change = c.received, &change
	}
	if err := q.InsertPayment(ctx, pay); err != nil {
		return fmt.Errorf("insert the payment of order %s: %w", c.orderID, err)
	}
	return nil
}

// checked is a NewOrder that passed checkOrder.
type checked struct {
	orderID, orderNo, kind string
	// tableID is the table a dine-in order seats its customers at; empty for
	// none.
	tableID string
	// standNumber is the number on a dine-in customer's stand, in capitals;
	// empty for none.
	standNumber string
	// prefix and day are the receipt number's codes and its YYYYMMDD.
	prefix, day string
	createdAt   time.Time
	lines       []line
	// voucherCode is the voucher's, as the vouchers keep it; empty for none.
	voucherCode string
	total       int64
	method      string
	received    *int64
}

type line struct {
	menuID string
	qty    int
	price  int64
	note   *string
}

// orderNoPattern is orders_order_no_check's rule, in parts: the outlet's
// code and the device's, the day, and the device's count that day.
var orderNoPattern = regexp.MustCompile(`^([A-Z0-9]{2,10}-K[0-9]{2,4})-([0-9]{8})-([0-9]{4,6})$`)

// standNumberPattern is orders_stand_number_check's rule: 1 to 10 capitals,
// digits and hyphens, a hyphen only between them, as 12, A5 or B-3.
var standNumberPattern = regexp.MustCompile(`^[A-Z0-9]([A-Z0-9-]{0,8}[A-Z0-9])?$`)

// checkOrder validates an order as the device sent it: every problem at
// once as validation_failed, a line's fields by their path (items.0.qty).
// The receipt number is checked against the device and the outlet in the
// sale, which knows them.
func checkOrder(in NewOrder, now time.Time) (checked, error) {
	fields := map[string]string{}
	c := checked{orderID: in.OrderID, orderNo: in.OrderNo, kind: in.Type, method: in.Payment.Method}
	switch {
	case in.OrderID == "":
		fields["order_id"] = "required"
	case !id.Valid(in.OrderID):
		fields["order_id"] = "format"
	}
	switch m := orderNoPattern.FindStringSubmatch(in.OrderNo); {
	case in.OrderNo == "":
		fields["order_no"] = "required"
	case m == nil || strings.Trim(m[3], "0") == "":
		fields["order_no"] = "format"
	default:
		c.prefix, c.day = m[1], m[2]
	}
	switch in.Type {
	case DineIn, Takeaway:
	case "":
		fields["type"] = "required"
	default:
		fields["type"] = "oneof"
	}
	if tableID := trimmed(in.TableID); tableID != nil {
		if in.Type == DineIn {
			c.tableID = *tableID
		} else {
			fields["table_id"] = "dine_in"
		}
	}
	// As typed on the stand, its small letters made capitals.
	if number := trimmed(in.StandNumber); number != nil {
		switch n := strings.ToUpper(*number); {
		case in.Type != DineIn:
			fields["stand_number"] = "dine_in"
		case c.tableID != "":
			fields["stand_number"] = "with_table"
		case !standNumberPattern.MatchString(n):
			fields["stand_number"] = "format"
		default:
			c.standNumber = n
		}
	}
	switch t := in.ClientCreatedAt; {
	case t == nil:
		fields["client_created_at"] = "required"
	case t.After(now.Add(maxAhead)) || t.Before(now.Add(-maxBehind)):
		fields["client_created_at"] = "range"
	default:
		// As the database keeps it, so a resend compares equal.
		c.createdAt = t.UTC().Truncate(time.Microsecond)
	}
	c.lines = checkLines(in.Items, fields)
	if code := trimmed(in.VoucherCode); code != nil {
		c.voucherCode = voucher.NormalizeCode(*code)
	}
	switch {
	case in.Total == nil:
		fields["total"] = "required"
	case *in.Total < 0:
		fields["total"] = "range"
	default:
		c.total = *in.Total
	}
	switch in.Payment.Method {
	case payment.Cash:
		switch r := in.Payment.Received; {
		case r == nil:
			fields["payment.received"] = "required"
		case *r < 0 || *r > MaxReceived:
			fields["payment.received"] = "range"
		case in.Total != nil && *r < *in.Total:
			fields["payment.received"] = "short"
		default:
			c.received = r
		}
	case payment.QRIS, payment.Debit:
	case "":
		fields["payment.method"] = "required"
	default:
		fields["payment.method"] = "oneof"
	}
	if len(fields) > 0 {
		return checked{}, httpx.Invalid(fields)
	}
	return c, nil
}

// checkLines validates the order's lines into fields, and returns them when
// they all pass. Together they come to at most MaxSubtotal.
func checkLines(items []NewItem, fields map[string]string) []line {
	switch {
	case len(items) == 0:
		fields["items"] = "required"
		return nil
	case len(items) > MaxLines:
		fields["items"] = "max"
		return nil
	}
	lines := make([]line, len(items))
	broken := false
	fail := func(i int, field, rule string) {
		fields["items."+strconv.Itoa(i)+"."+field] = rule
		broken = true
	}
	for i, it := range items {
		l := line{menuID: it.MenuID, note: trimmed(it.Note)}
		switch {
		case it.MenuID == "":
			fail(i, "menu_id", "required")
		case !id.Valid(it.MenuID):
			fail(i, "menu_id", "format")
		}
		switch {
		case it.Qty == nil:
			fail(i, "qty", "required")
		case *it.Qty < 1 || *it.Qty > MaxQty:
			fail(i, "qty", "range")
		default:
			l.qty = *it.Qty
		}
		switch {
		case it.Price == nil:
			fail(i, "price", "required")
		case *it.Price < 0 || *it.Price > menu.MaxPrice:
			fail(i, "price", "range")
		default:
			l.price = *it.Price
		}
		if l.note != nil && utf8.RuneCountInString(*l.note) > MaxNote {
			fail(i, "note", "max")
		}
		lines[i] = l
	}
	if broken {
		return nil
	}
	var subtotal int64
	for _, l := range lines {
		subtotal += l.price * int64(l.qty)
	}
	if subtotal > MaxSubtotal {
		fields["items"] = "range"
		return nil
	}
	return lines
}

// hash is the order as the device sent it, checked, as SHA-256 in hex: the
// same order sent again has the same hash, and any other order another.
func (c checked) hash() (string, error) {
	type hashedLine struct {
		MenuID string  `json:"menu_id"`
		Qty    int     `json:"qty"`
		Price  int64   `json:"price"`
		Note   *string `json:"note"`
	}
	lines := make([]hashedLine, len(c.lines))
	for i, l := range c.lines {
		lines[i] = hashedLine{MenuID: l.menuID, Qty: l.qty, Price: l.price, Note: l.note}
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
	}{c.orderID, c.orderNo, c.kind, c.createdAt, lines, c.voucherCode, c.total, c.method, c.received, c.tableID,
		c.standNumber})
	if err != nil {
		return "", fmt.Errorf("hash order %s: %w", c.orderID, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// checkMode checks the order against how the outlet seats its dine-in
// customers: where they take number stands, a dine-in order carries its
// stand's number, and so no table (checkOrder allows one or the other);
// where they sit at tables, no number.
func (c checked) checkMode(mode table.Mode) error {
	fits := c.standNumber == ""
	if mode == table.ModeNumber {
		fits = c.kind != DineIn || c.standNumber != ""
	}
	if !fits {
		return ErrDineInModeChanged.WithDetails(map[string]any{"mode": string(mode)})
	}
	return nil
}

// checkNumber checks the receipt number against the device and its outlet:
// it starts with their codes, and carries the outlet's day the order was
// made on, which starts the device's count again each day.
func (c checked) checkNumber(o outlet.Cashier, d device.Device) error {
	if c.prefix != o.Code+"-"+d.Code {
		return httpx.Invalid(map[string]string{"order_no": "prefix"})
	}
	start, _, err := day(c.createdAt, o.Timezone)
	if err != nil {
		return err
	}
	if c.day != start.Format("20060102") {
		return httpx.Invalid(map[string]string{"order_no": "date"})
	}
	return nil
}

// checkMenus checks the order against its menus, locked: each is still on
// sale (ErrMenuUnavailable), at the price the cashier showed
// (ErrPricesChanged), and with stock enough for its lines together
// (ErrStockInsufficient). Each error names every menu it is about.
func (c checked) checkMenus(menus map[string]menu.SaleMenu) error {
	var unavailable []string
	for _, menuID := range c.menuIDs() {
		if m, ok := menus[menuID]; !ok || !m.Available {
			unavailable = append(unavailable, menuID)
		}
	}
	if len(unavailable) > 0 {
		return ErrMenuUnavailable.WithDetails(map[string]any{"menus": unavailable})
	}
	var changed []map[string]any
	for _, menuID := range c.menuIDs() {
		m := menus[menuID]
		if slices.ContainsFunc(c.lines, func(l line) bool { return l.menuID == menuID && l.price != m.Price }) {
			changed = append(changed, map[string]any{"menu_id": menuID, "price": m.Price})
		}
	}
	if len(changed) > 0 {
		return ErrPricesChanged.WithDetails(map[string]any{"menus": changed})
	}
	var short []map[string]any
	for _, s := range c.sold() {
		if m := menus[s.MenuID]; m.TrackStock && s.Qty > m.StockQty {
			short = append(short, map[string]any{"menu_id": s.MenuID, "available": max(m.StockQty, 0)})
		}
	}
	if len(short) > 0 {
		return ErrStockInsufficient.WithDetails(map[string]any{"menus": short})
	}
	return nil
}

// menuIDs are the order's menus, each once, sorted: the order LockForSale
// locks them in.
func (c checked) menuIDs() []string {
	ids := make([]string, len(c.lines))
	for i, l := range c.lines {
		ids[i] = l.menuID
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// sold is how many of each menu the order sells, its lines together, by
// menu ID.
func (c checked) sold() []menu.Sold {
	qty := map[string]int{}
	for _, l := range c.lines {
		qty[l.menuID] += l.qty
	}
	ids := c.menuIDs()
	out := make([]menu.Sold, len(ids))
	for i, menuID := range ids {
		out[i] = menu.Sold{MenuID: menuID, Qty: qty[menuID]}
	}
	return out
}

// subtotal is what the lines add up to, the voucher's discount not off yet.
func (c checked) subtotal() int64 {
	var sum int64
	for _, l := range c.lines {
		sum += l.price * int64(l.qty)
	}
	return sum
}

// totalLines are the lines as ComputeTotals reads them.
func (c checked) totalLines() []Line {
	out := make([]Line, len(c.lines))
	for i, l := range c.lines {
		out[i] = Line{Price: l.price, Qty: l.qty}
	}
	return out
}

// trimmed is the text without surrounding spaces, or nil for none.
func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
