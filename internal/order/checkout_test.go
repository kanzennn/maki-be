package order

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/platform/id"
)

func ptr[T any](v T) *T { return &v }

func TestCreateOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi susu", 18000, ptr(10))
	jeruk := h.menu("o1", h.drinks, "Es jeruk", 8000, nil)
	tl := h.till("o1", "Kasir depan")

	less := item(kopi, 2)
	less["note"] = "  less sugar  "
	body := h.order(tl, less, item(jeruk, 1), item(kopi, 1))
	body["type"] = DineIn
	body["payment"] = map[string]any{"method": "cash", "received": 100000}
	r := h.do(http.MethodPost, ordersPath, "cashier", tl.token, body).status(http.StatusCreated, "")
	var o orderResponse
	r.decode(&o)

	// 3 × 18,000 + 8,000 = 62,000; service 5% = 3,100; tax 10% of 65,100 =
	// 6,510; total 71,610.
	if o.OrderID != body["order_id"] || o.OrderNo != "BDG01-K01-20261001-0001" || o.Type != DineIn ||
		o.Status != StatusPaid || o.DeviceID != tl.DeviceID || o.Cashier != (personResponse{UserID: "k1", Name: "Budi"}) {
		t.Errorf("order = %+v", o)
	}
	if o.Subtotal != 62000 || o.VoucherDiscount != 0 || o.ServiceBPS != o1Service || o.Service != 3100 ||
		o.TaxBPS != o1Tax || o.Tax != 6510 || o.Total != 71610 {
		t.Errorf("totals = %+v", o)
	}
	if p := o.Payment; p.Method != "cash" || p.Amount != 71610 || p.Received == nil || *p.Received != 100000 ||
		p.Change == nil || *p.Change != 28390 || p.Status != StatusPaid {
		t.Errorf("payment = %+v", p)
	}
	if !o.ClientCreatedAt.Equal(h.clock) || o.VoidedAt != nil || o.VoidedBy != nil || o.VoidReason != nil {
		t.Errorf("times = %+v", o)
	}
	// The lines in the order sent, with the menu's name and price copied.
	if len(o.Items) != 3 {
		t.Fatalf("items = %+v", o.Items)
	}
	first := o.Items[0]
	if first.MenuID != kopi.MenuID || first.MenuName != "Kopi susu" || first.Price != 18000 || first.Qty != 2 ||
		first.LineTotal != 36000 || first.Note == nil || *first.Note != "less sugar" {
		t.Errorf("first line = %+v", first)
	}
	if o.Items[1].MenuName != "Es jeruk" || o.Items[1].Note != nil || o.Items[2].Qty != 1 || o.Items[2].MenuID != kopi.MenuID {
		t.Errorf("lines = %+v", o.Items)
	}

	// The stock goes down once for the menu's lines together, as a sale
	// naming the order, by the cashier; an untracked menu has none.
	if got := h.stock(kopi.MenuID); got != 7 {
		t.Errorf("stock = %d, want 7", got)
	}
	var kind, userID, orderID string
	var change int
	h.scalar(&kind, `SELECT type FROM stock_movements WHERE menu_id = $1 ORDER BY created_at DESC LIMIT 1`, kopi.MenuID)
	h.scalar(&userID, `SELECT user_id FROM stock_movements WHERE menu_id = $1 AND type = 'sale'`, kopi.MenuID)
	h.scalar(&orderID, `SELECT order_id FROM stock_movements WHERE menu_id = $1 AND type = 'sale'`, kopi.MenuID)
	h.scalar(&change, `SELECT qty_change FROM stock_movements WHERE menu_id = $1 AND type = 'sale'`, kopi.MenuID)
	if kind != "sale" || userID != "k1" || orderID != o.OrderID || change != -3 {
		t.Errorf("movement: %s by %s for %s, %d", kind, userID, orderID, change)
	}
	if n := h.count(`SELECT count(*) FROM stock_movements WHERE menu_id = $1`, jeruk.MenuID); n != 0 {
		t.Errorf("an untracked menu has %d movements", n)
	}
	// The Log Action names the device, and the device was seen.
	if got := h.actions(o.OrderID); !slices.Equal(got, []string{"order.created"}) {
		t.Errorf("log = %v", got)
	}
	var deviceID string
	h.scalar(&deviceID, `SELECT device_id FROM action_logs WHERE target_id = $1`, o.OrderID)
	var seen *time.Time
	h.scalar(&seen, `SELECT last_seen_at FROM devices WHERE device_id = $1`, tl.DeviceID)
	if deviceID != tl.DeviceID || seen == nil {
		t.Errorf("log's device %q, last seen %v", deviceID, seen)
	}
	// And the order reads back as it was taken.
	var got orderResponse
	h.do(http.MethodGet, ordersPath+"/"+o.OrderID, "cashier", "", nil).status(http.StatusOK, "").decode(&got)
	if got.OrderNo != o.OrderNo || got.Total != o.Total || len(got.Items) != 3 || *got.Payment.Change != 28390 {
		t.Errorf("read back = %+v", got)
	}
}

func TestOrderRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")

	cases := map[string]struct {
		change      func(b map[string]any)
		field, rule string
	}{
		"no order ID":         {func(b map[string]any) { delete(b, "order_id") }, "order_id", "required"},
		"not an ID":           {func(b map[string]any) { b["order_id"] = "Not-An-ID" }, "order_id", "format"},
		"no receipt number":   {func(b map[string]any) { delete(b, "order_no") }, "order_no", "required"},
		"receipt number":      {func(b map[string]any) { b["order_no"] = "BDG01-K01-2026101-0001" }, "order_no", "format"},
		"lower case":          {func(b map[string]any) { b["order_no"] = "bdg01-K01-20261001-0001" }, "order_no", "format"},
		"count of zero":       {func(b map[string]any) { b["order_no"] = "BDG01-K01-20261001-0000" }, "order_no", "format"},
		"no type":             {func(b map[string]any) { delete(b, "type") }, "type", "required"},
		"unknown type":        {func(b map[string]any) { b["type"] = "delivery" }, "type", "oneof"},
		"no time":             {func(b map[string]any) { delete(b, "client_created_at") }, "client_created_at", "required"},
		"from the future":     {func(b map[string]any) { b["client_created_at"] = h.clock.Add(6 * time.Minute) }, "client_created_at", "range"},
		"from days ago":       {func(b map[string]any) { b["client_created_at"] = h.clock.Add(-25 * time.Hour) }, "client_created_at", "range"},
		"no items":            {func(b map[string]any) { b["items"] = []any{} }, "items", "required"},
		"items left out":      {func(b map[string]any) { delete(b, "items") }, "items", "required"},
		"too many lines":      {func(b map[string]any) { b["items"] = slices.Repeat([]any{item(kopi, 1)}, MaxLines+1) }, "items", "max"},
		"no menu":             {func(b map[string]any) { firstLine(b)["menu_id"] = "" }, "items.0.menu_id", "required"},
		"not a menu ID":       {func(b map[string]any) { firstLine(b)["menu_id"] = "Kopi!" }, "items.0.menu_id", "format"},
		"no qty":              {func(b map[string]any) { delete(firstLine(b), "qty") }, "items.0.qty", "required"},
		"qty of zero":         {func(b map[string]any) { firstLine(b)["qty"] = 0 }, "items.0.qty", "range"},
		"qty too high":        {func(b map[string]any) { firstLine(b)["qty"] = MaxQty + 1 }, "items.0.qty", "range"},
		"no price":            {func(b map[string]any) { delete(firstLine(b), "price") }, "items.0.price", "required"},
		"price below zero":    {func(b map[string]any) { firstLine(b)["price"] = -1 }, "items.0.price", "range"},
		"price too high":      {func(b map[string]any) { firstLine(b)["price"] = 100_000_001 }, "items.0.price", "range"},
		"note too long":       {func(b map[string]any) { firstLine(b)["note"] = strings.Repeat("é", MaxNote+1) }, "items.0.note", "max"},
		"over a billion":      {func(b map[string]any) { firstLine(b)["price"] = 100_000_000; firstLine(b)["qty"] = 11 }, "items", "range"},
		"no total":            {func(b map[string]any) { delete(b, "total") }, "total", "required"},
		"total below zero":    {func(b map[string]any) { b["total"] = -1 }, "total", "range"},
		"no payment":          {func(b map[string]any) { delete(b, "payment") }, "payment", "required"},
		"no method":           {func(b map[string]any) { pay(b)["method"] = "" }, "payment.method", "required"},
		"unknown method":      {func(b map[string]any) { pay(b)["method"] = "card" }, "payment.method", "oneof"},
		"cash without amount": {func(b map[string]any) { delete(pay(b), "received") }, "payment.received", "required"},
		"too little cash":     {func(b map[string]any) { pay(b)["received"] = b["total"].(int64) - 1 }, "payment.received", "short"},
		"too much cash":       {func(b map[string]any) { pay(b)["received"] = MaxReceived + 1 }, "payment.received", "range"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := h.order(tl, item(kopi, 1))
			tc.change(body)
			r := h.do(http.MethodPost, ordersPath, "cashier", tl.token, body).status(http.StatusUnprocessableEntity, "validation_failed")
			if got := r.fields()[tc.field]; got != tc.rule {
				t.Errorf("fields = %v, want %s: %s", r.fields(), tc.field, tc.rule)
			}
		})
	}
	// Every problem at once.
	body := h.order(tl, item(kopi, 0), item(kopi, 1))
	body["type"] = "x"
	firstLine(body)["price"] = -5
	r := h.do(http.MethodPost, ordersPath, "cashier", tl.token, body).status(http.StatusUnprocessableEntity, "")
	if f := r.fields(); f["type"] != "oneof" || f["items.0.qty"] != "range" || f["items.0.price"] != "range" || len(f) != 3 {
		t.Errorf("fields = %v", f)
	}
	// Exactly the cash due, and the most there may be, are fine.
	exact := h.order(tl, item(kopi, 1))
	pay(exact)["received"] = exact["total"]
	h.sell(tl, "cashier", exact)
	// 100 lines of 999 at 10,000: 999,000,000, under the billion.
	air := h.menu("o1", h.drinks, "Air", 10000, nil)
	most := h.order(tl, item(air, MaxQty))
	most["items"] = slices.Repeat([]any{firstLine(most)}, MaxLines)
	most["total"] = ComputeTotals(slices.Repeat([]Line{{Price: 10000, Qty: MaxQty}}, MaxLines), 0, o1Service, o1Tax).Total
	pay(most)["received"] = MaxReceived
	h.sell(tl, "cashier", most)
	if n := h.count(`SELECT count(*) FROM orders`); n != 2 {
		t.Errorf("%d orders, want the 2 that passed", n)
	}
}

// firstLine is the first line of an order's body.
func firstLine(b map[string]any) map[string]any {
	switch items := b["items"].(type) {
	case []map[string]any:
		return items[0]
	case []any:
		return items[0].(map[string]any)
	}
	panic("no lines")
}

// pay is an order's payment.
func pay(b map[string]any) map[string]any { return b["payment"].(map[string]any) }

func TestReceiptNumberRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	other := h.till("o1", "Kasir 2")

	cases := map[string]struct {
		orderNo string
		at      time.Time
		rule    string
	}{
		// Another device's codes, or another outlet's.
		"another device's": {"BDG01-K02-20261001-0001", h.clock, "prefix"},
		"another outlet's": {"JKT01-K01-20261001-0001", h.clock, "prefix"},
		// The day is the outlet's day of the order: 23:30 UTC on 30
		// September is 06:30 on 1 October in Bandung.
		"yesterday's day":         {"BDG01-K01-20260930-0001", h.clock, "date"},
		"the UTC day":             {"BDG01-K01-20260930-0001", time.Date(2026, 9, 30, 23, 30, 0, 0, time.UTC), "date"},
		"the device's local time": {"BDG01-K01-20261001-0001", time.Date(2026, 10, 1, 0, 30, 0, 0, time.FixedZone("", 9*3600)), "date"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := h.order(tl, item(kopi, 1))
			body["order_no"], body["client_created_at"] = tc.orderNo, tc.at
			r := h.do(http.MethodPost, ordersPath, "cashier", tl.token, body).status(http.StatusUnprocessableEntity, "validation_failed")
			if got := r.fields()["order_no"]; got != tc.rule {
				t.Errorf("fields = %v, want order_no: %s", r.fields(), tc.rule)
			}
		})
	}
	// The same, right: the outlet's day of an order made at 23:30 UTC, and a
	// count past four digits.
	body := h.order(tl, item(kopi, 1))
	body["order_no"], body["client_created_at"] = "BDG01-K01-20261001-0001", time.Date(2026, 9, 30, 23, 30, 0, 0, time.UTC)
	h.sell(tl, "cashier", body)
	body = h.order(tl, item(kopi, 1))
	body["order_no"] = "BDG01-K01-20261001-12345"
	h.sell(tl, "cashier", body)

	// A number the outlet has used already, as from a second tab.
	again := h.order(tl, item(kopi, 1))
	again["order_no"] = "BDG01-K01-20261001-0001"
	h.do(http.MethodPost, ordersPath, "cashier", tl.token, again).status(http.StatusConflict, "order_no_taken")
	// Each device counts on its own.
	theirs := h.order(other, item(kopi, 1))
	theirs["order_no"] = "BDG01-K02-20261001-0001"
	h.sell(other, "cashier", theirs)
	if n := h.count(`SELECT count(*) FROM orders`); n != 3 {
		t.Errorf("%d orders, want 3", n)
	}
}

func TestFiguresThatChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := t.Context()
	mgr := principals["manager"]
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, ptr(5))
	jeruk := h.menu("o1", h.drinks, "Es jeruk", 8000, ptr(1))
	tl := h.till("o1", "Kasir")
	refused := func(body map[string]any, code string) map[string]any {
		t.Helper()
		return h.do(http.MethodPost, ordersPath, "cashier", tl.token, body).status(http.StatusConflict, code).
			envelope().Error.Details
	}

	// A price changed since the catalog was loaded: each changed menu, with
	// its price now.
	if _, err := h.menus.UpdateMenu(ctx, mgr, "", "o1", kopi.MenuID, menuInput(kopi, 20000)); err != nil {
		t.Fatal(err)
	}
	d := refused(h.order(tl, item(jeruk, 1), item(kopi, 2)), "prices_changed")
	if menus, _ := d["menus"].([]any); len(menus) != 1 || menus[0].(map[string]any)["menu_id"] != kopi.MenuID ||
		menus[0].(map[string]any)["price"] != float64(20000) {
		t.Errorf("details = %v", d)
	}
	kopi.Price = 20000

	// More than there is, the menu's lines together: each short menu, with
	// what is available.
	d = refused(h.order(tl, item(kopi, 3), item(jeruk, 2), item(kopi, 3)), "order_stock_insufficient")
	if menus, _ := d["menus"].([]any); len(menus) != 2 {
		t.Errorf("details = %v", d)
	} else {
		got := map[string]float64{}
		for _, m := range menus {
			got[m.(map[string]any)["menu_id"].(string)] = m.(map[string]any)["available"].(float64)
		}
		if got[kopi.MenuID] != 5 || got[jeruk.MenuID] != 1 {
			t.Errorf("available = %v", got)
		}
	}

	// Menus no longer on sale: inactive, deleted, in an inactive category,
	// another outlet's, or none at all.
	snacks := h.category("o1", "Snack")
	keripik := h.menu("o1", snacks, "Keripik", 5000, nil)
	if _, err := h.menus.SetCategoryActive(ctx, mgr, "", "o1", snacks, false); err != nil {
		t.Fatal(err)
	}
	off := h.menu("o1", h.drinks, "Jus", 15000, nil)
	if _, err := h.menus.SetMenuActive(ctx, mgr, "", "o1", off.MenuID, false); err != nil {
		t.Fatal(err)
	}
	gone := h.menu("o1", h.drinks, "Soda", 12000, nil)
	if err := h.menus.DeleteMenu(ctx, mgr, "", "o1", gone.MenuID); err != nil {
		t.Fatal(err)
	}
	theirs := h.menu("o2", h.category("o2", "Drinks"), "Coffee", 30000, nil)
	unknown := item(kopi, 1)
	unknown["menu_id"] = id.New()
	d = refused(h.order(tl, item(kopi, 1), item(keripik, 1), item(off, 1), item(gone, 1), item(theirs, 1), unknown),
		"menu_unavailable")
	menus, _ := d["menus"].([]any)
	if len(menus) != 5 || !slices.Contains(menus, any(keripik.MenuID)) || !slices.Contains(menus, any(theirs.MenuID)) ||
		slices.Contains(menus, any(kopi.MenuID)) {
		t.Errorf("details = %v", d)
	}

	// The rates changed: the server's total.
	h.exec(`UPDATE outlets SET tax_bps = 1100 WHERE outlet_id = 'o1'`)
	d = refused(h.order(tl, item(kopi, 1)), "total_changed")
	// 20,000 + 1,000 service + 11% of 21,000.
	if d["total"] != float64(23310) {
		t.Errorf("details = %v", d)
	}
	h.exec(`UPDATE outlets SET tax_bps = $1 WHERE outlet_id = 'o1'`, o1Tax)

	// A method the outlet doesn't take.
	qris := h.order(tl, item(kopi, 1))
	qris["payment"] = map[string]any{"method": "qris"}
	refused(qris, "payment_method_unavailable")

	// Nothing of any of them was taken.
	if n := h.count(`SELECT count(*) FROM orders`); n != 0 {
		t.Errorf("%d orders taken", n)
	}
	if h.stock(kopi.MenuID) != 5 || h.stock(jeruk.MenuID) != 1 {
		t.Error("refused orders changed the stock")
	}
	// The figures as they are now sell.
	h.sell(tl, "cashier", h.order(tl, item(kopi, 5), item(jeruk, 1)))
	if h.stock(kopi.MenuID) != 0 || h.stock(jeruk.MenuID) != 0 {
		t.Error("the stock after selling it all")
	}
}

// menuInput is the menu as its form would send it, at a new price.
func menuInput(m menu.Menu, price int64) menu.MenuInput {
	return menu.MenuInput{
		Name: m.Name, CategoryID: m.Category.CategoryID, Price: &price, Description: m.Description, SKU: m.SKU,
		TrackStock: m.TrackStock, StockAlert: m.StockAlert,
	}
}

func TestOutletInactiveDuringTheSale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	// Deactivated after Authenticate let the cashier in.
	h.exec(`UPDATE outlets SET status = 'inactive' WHERE outlet_id = 'o1'`)
	h.do(http.MethodPost, ordersPath, "cashier", tl.token, h.order(tl, item(kopi, 1))).
		status(http.StatusForbidden, "outlet_inactive")
}

func TestResend(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, ptr(10))
	tl := h.till("o1", "Kasir")
	body := h.order(tl, item(kopi, 2))
	first := h.sell(tl, "cashier", body)

	// The same order again, as after a lost answer: the order as it was, not
	// a second one, whoever sends it. Spacing and the time's zone don't make
	// it another.
	again := h.order(tl, item(kopi, 2))
	again["order_id"], again["order_no"] = body["order_id"], body["order_no"]
	firstLine(again)["note"] = "   "
	again["client_created_at"] = h.clock.In(time.FixedZone("WITA", 8*3600))
	var o orderResponse
	h.do(http.MethodPost, ordersPath, "cashier2", tl.token, again).status(http.StatusOK, "").decode(&o)
	if o.OrderID != first.OrderID || o.OrderNo != first.OrderNo || o.Total != first.Total || o.Cashier.UserID != "k1" {
		t.Errorf("resend = %+v, want %+v", o, first)
	}
	// The same ID with anything else in it is another order.
	// The same ID with anything else in it is another order: a note, with
	// the same total.
	changed := h.order(tl, item(kopi, 2))
	changed["order_id"], changed["order_no"] = body["order_id"], body["order_no"]
	firstLine(changed)["note"] = "less sugar"
	h.do(http.MethodPost, ordersPath, "cashier", tl.token, changed).status(http.StatusConflict, "order_id_conflict")
	// And so is the same ID from another outlet, which learns nothing of it,
	// even sending the very same order.
	elsewhere := h.till("o2", "Kasir")
	h.do(http.MethodPost, "/api/v1/outlets/o2/orders", "other", elsewhere.token, body).
		status(http.StatusConflict, "order_id_conflict")
	theirs := h.order(elsewhere, item(h.menu("o2", h.category("o2", "Drinks"), "Coffee", 30000, nil), 1))
	theirs["order_id"] = body["order_id"]
	r := h.do(http.MethodPost, "/api/v1/outlets/o2/orders", "other", elsewhere.token, theirs).
		status(http.StatusConflict, "order_id_conflict")
	if strings.Contains(r.Body.String(), first.OrderNo) {
		t.Errorf("the conflict tells another outlet of the order: %s", r.Body)
	}
	// A resend from the device after it was revoked still gets its order.
	if err := h.devices.Revoke(t.Context(), principals["manager"], "", "o1", tl.DeviceID); err != nil {
		t.Fatal(err)
	}
	h.do(http.MethodPost, ordersPath, "cashier", tl.token, body).status(http.StatusOK, "")

	if n := h.count(`SELECT count(*) FROM orders`); n != 1 {
		t.Errorf("%d orders, want 1", n)
	}
	if got := h.stock(kopi.MenuID); got != 8 {
		t.Errorf("stock = %d, want 8: taken once", got)
	}
	if got := h.actions(first.OrderID); len(got) != 1 {
		t.Errorf("log = %v, want one order.created", got)
	}
}

func TestResendWhileTheFirstIsOnItsWay(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	// The last one: the resend still gets its order, not a refusal for the
	// stock the order took.
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, ptr(1))
	tl := h.till("o1", "Kasir")
	body := h.order(tl, item(kopi, 1))

	// Hold the device, so the order and its resend both wait for it.
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM devices WHERE device_id = $1 FOR UPDATE`, tl.DeviceID); err != nil {
		t.Fatal(err)
	}
	codes := make(chan int, 2)
	for range 2 {
		go func() { codes <- h.do(http.MethodPost, ordersPath, "cashier", tl.token, body).Code }()
	}
	h.waitForLock(2, waitingOn("devices"))
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	got := []int{<-codes, <-codes}
	slices.Sort(got)
	if !slices.Equal(got, []int{http.StatusOK, http.StatusCreated}) {
		t.Errorf("statuses = %v, want one 201 and one 200", got)
	}
	if n := h.count(`SELECT count(*) FROM orders`); n != 1 {
		t.Errorf("%d orders, want 1", n)
	}
	if got := h.stock(kopi.MenuID); got != 0 {
		t.Errorf("stock = %d, want 0", got)
	}
}

func TestTwoCashiersBuyTheLastStock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	croissant := h.menu("o1", h.drinks, "Croissant", 25000, ptr(1))
	front, back := h.till("o1", "Kasir depan"), h.till("o1", "Kasir belakang")
	sales := []struct {
		tl   *till
		who  string
		body map[string]any
	}{
		{front, "cashier", h.order(front, item(croissant, 1))},
		{back, "cashier2", h.order(back, item(croissant, 1))},
	}

	// Hold the menu, so both sales wait for it, then let them go at once.
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM menus WHERE menu_id = $1 FOR UPDATE`, croissant.MenuID); err != nil {
		t.Fatal(err)
	}
	results := make(chan response, len(sales))
	for _, s := range sales {
		go func() { results <- h.do(http.MethodPost, ordersPath, s.who, s.tl.token, s.body) }()
	}
	h.waitForLock(2, waitingOn("menus"))
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var sold, refused int
	for range sales {
		r := <-results
		switch r.Code {
		case http.StatusCreated:
			sold++
		case http.StatusConflict:
			refused++
			d := r.envelope().Error.Details
			if r.envelope().Error.Code != "order_stock_insufficient" ||
				d["menus"].([]any)[0].(map[string]any)["available"] != float64(0) {
				t.Errorf("refusal = %s", r.Body)
			}
		default:
			t.Errorf("status %d: %s", r.Code, r.Body)
		}
	}
	if sold != 1 || refused != 1 {
		t.Errorf("%d sold and %d refused, want exactly one of each", sold, refused)
	}
	if got := h.stock(croissant.MenuID); got != 0 {
		t.Errorf("stock = %d, want 0", got)
	}
	if n := h.count(`SELECT count(*) FROM stock_movements WHERE menu_id = $1 AND type = 'sale'`, croissant.MenuID); n != 1 {
		t.Errorf("%d sales recorded, want 1", n)
	}
}

func TestTwoCashiersShareTheStock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, ptr(5))
	jeruk := h.menu("o1", h.drinks, "Es jeruk", 8000, ptr(5))
	front, back := h.till("o1", "Kasir depan"), h.till("o1", "Kasir belakang")
	// The same menus in the other order: neither waits for the other in a
	// circle, and both sell.
	first := h.order(front, item(kopi, 2), item(jeruk, 1))
	second := h.order(back, item(jeruk, 2), item(kopi, 1))

	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM menus WHERE menu_id = ANY ($1) FOR UPDATE`,
		[]string{kopi.MenuID, jeruk.MenuID}); err != nil {
		t.Fatal(err)
	}
	codes := make(chan int, 2)
	go func() { codes <- h.do(http.MethodPost, ordersPath, "cashier", front.token, first).Code }()
	go func() { codes <- h.do(http.MethodPost, ordersPath, "cashier2", back.token, second).Code }()
	h.waitForLock(2, waitingOn("menus"))
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if a, b := <-codes, <-codes; a != http.StatusCreated || b != http.StatusCreated {
		t.Errorf("statuses %d and %d, want both 201", a, b)
	}
	if h.stock(kopi.MenuID) != 2 || h.stock(jeruk.MenuID) != 2 {
		t.Errorf("stock: kopi %d, jeruk %d, want 2 each", h.stock(kopi.MenuID), h.stock(jeruk.MenuID))
	}
}

func TestOutletCodeLocksWithTheFirstOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	owner := principals["owner"]
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, ptr(5))
	tl := h.till("o1", "Kasir")
	edit := func(code string) error {
		_, err := h.outlets.Update(ctx, owner, "", "o1", outlet.Input{
			Code: code, Name: "Bandung", Address: ptr("Jl. Braga 1"), Phone: ptr("022-123"),
			Timezone: "Asia/Jakarta", Locale: "id", TaxPct: 10, ServicePct: 5,
		})
		return err
	}
	// No order yet: the code may change, and back.
	if err := edit("BDG09"); err != nil {
		t.Fatalf("before any order: %v", err)
	}
	if err := edit("BDG01"); err != nil {
		t.Fatal(err)
	}

	// A sale under way holds the outlet: the change waits for it, then finds
	// its order. Hold the menu so the sale stops there, with the outlet held.
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM menus WHERE menu_id = $1 FOR UPDATE`, kopi.MenuID); err != nil {
		t.Fatal(err)
	}
	sale := make(chan int, 1)
	go func() {
		sale <- h.do(http.MethodPost, ordersPath, "cashier", tl.token, h.order(tl, item(kopi, 1))).Code
	}()
	h.waitForLock(1, waitingOn("menus"))
	changed := make(chan error, 1)
	go func() { changed <- edit("BDG02") }()
	deadline := time.Now().Add(10 * time.Second)
	for waiting := 0; waiting < 1; {
		select {
		case err := <-changed:
			t.Fatalf("the code changed while a sale was under way: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the change didn't wait for the sale")
		}
		time.Sleep(10 * time.Millisecond)
		h.scalar(&waiting, waitingOn("outlets"))
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if code := <-sale; code != http.StatusCreated {
		t.Errorf("sale = %d", code)
	}
	if err := <-changed; !errors.Is(err, outlet.ErrCodeLocked) {
		t.Errorf("the change after the sale: %v, want ErrCodeLocked", err)
	}
	// From then on only the code is locked.
	if err := edit("BDG03"); !errors.Is(err, outlet.ErrCodeLocked) {
		t.Errorf("a later change: %v, want ErrCodeLocked", err)
	}
	if _, err := h.outlets.Update(ctx, owner, "", "o1", outlet.Input{
		Code: "bdg01", Name: "Bandung Braga", Timezone: "Asia/Jakarta", Locale: "id", TaxPct: 10, ServicePct: 5,
	}); err != nil {
		t.Errorf("changing the rest: %v", err)
	}
	var code string
	h.scalar(&code, `SELECT code FROM outlets WHERE outlet_id = 'o1'`)
	if code != "BDG01" {
		t.Errorf("code = %s", code)
	}
}
