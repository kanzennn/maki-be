package order

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/response"
)

func (h *harness) list(who, query string) response.List[models.OrderSummaryResponse] {
	h.t.Helper()
	var l response.List[models.OrderSummaryResponse]
	h.do(http.MethodGet, ordersPath+query, who, "", nil).status(http.StatusOK, "").decode(&l)
	return l
}

func numbers(l response.List[models.OrderSummaryResponse]) []string {
	var out []string
	for _, o := range l.Data {
		out = append(out, o.OrderNo)
	}
	return out
}

func TestListOrders(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	jeruk := h.menu("o1", h.drinks, "Es jeruk", 8000, nil)
	front, back := h.till("o1", "Kasir depan"), h.till("o1", "Kasir belakang")

	today := h.clock
	h.clock = today.AddDate(0, 0, -1)
	h.sell(front, "cashier", h.order(front, item(kopi, 1)))
	// 06:30 in Bandung, 23:30 the evening before in UTC: today's all the same.
	h.clock = time.Date(2026, 10, 1, 6, 30, 0, 0, jakarta)
	early := h.sell(back, "cashier2", h.order(back, item(kopi, 1)))
	h.clock = today.Add(-2 * time.Hour)
	first := h.sell(front, "cashier", h.order(front, item(kopi, 2), item(jeruk, 3)))
	h.clock = today.Add(-time.Hour)
	second := h.sell(back, "cashier2", h.order(back, item(jeruk, 1)))
	h.clock = today
	elsewhere := h.till("o2", "Kasir")
	coffee := h.menu("o2", h.category("o2", "Drinks"), "Coffee", 30000, nil)
	h.sell(elsewhere, "other", h.order(elsewhere, item(coffee, 1)))

	// Today's, in the outlet's time zone, newest first; not yesterday's,
	// nor another outlet's.
	l := h.list("cashier", "")
	if got, want := numbers(l), []string{second.OrderNo, first.OrderNo, early.OrderNo}; !slices.Equal(got, want) || l.Meta.Total != 3 {
		t.Errorf("today = %v (%d), want %v", got, l.Meta.Total, want)
	}
	s := l.Data[1]
	if s.OrderID != first.OrderID || s.DeviceID != front.DeviceID || s.Type != models.OrderTakeaway || s.Status != models.OrderPaid ||
		s.Total != first.Total || s.ItemCount != 5 || s.PaymentMethod != "cash" ||
		s.Cashier != (models.PersonResponse{UserID: "k1", Name: "Budi"}) || !s.ClientCreatedAt.Equal(today.Add(-2*time.Hour)) {
		t.Errorf("summary = %+v", s)
	}
	if l.Data[0].Cashier.Name != "Sari" || l.Data[0].ItemCount != 1 {
		t.Errorf("summary = %+v", l.Data[0])
	}

	// A part of the receipt number, in any case; LIKE's wildcards are only
	// characters.
	if got := numbers(h.list("cashier", "?q=k02-")); !slices.Equal(got, []string{second.OrderNo, early.OrderNo}) {
		t.Errorf("q=k02- = %v", got)
	}
	if got := numbers(h.list("cashier", "?q="+first.OrderNo)); !slices.Equal(got, []string{first.OrderNo}) {
		t.Errorf("q=%s = %v", first.OrderNo, got)
	}
	if l := h.list("cashier", "?q=%25"); len(l.Data) != 0 || l.Meta.Total != 0 {
		t.Errorf("q=%% = %v", numbers(l))
	}
	// Pages.
	page := h.list("cashier", "?page=2&page_size=1")
	if got := numbers(page); !slices.Equal(got, []string{first.OrderNo}) || page.Meta.Total != 3 || page.Meta.Page != 2 {
		t.Errorf("page 2 = %v %+v", got, page.Meta)
	}
	h.do(http.MethodGet, ordersPath+"?page=0", "cashier", "", nil).status(http.StatusUnprocessableEntity, "validation_failed")
	// Whoever voids sees them too.
	if got := numbers(h.list("voider", "")); len(got) != 3 {
		t.Errorf("for order.void: %v", got)
	}
}

func TestGetOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	o := h.sell(tl, "cashier", h.order(tl, item(kopi, 1)))

	var got models.OrderResponse
	h.do(http.MethodGet, ordersPath+"/"+o.OrderID, "voider", "", nil).status(http.StatusOK, "").decode(&got)
	if got.OrderID != o.OrderID || got.Items[0].MenuName != "Kopi" {
		t.Errorf("order = %+v", got)
	}
	h.do(http.MethodGet, ordersPath+"/nope", "cashier", "", nil).status(http.StatusNotFound, "order_not_found")
	// Another outlet's order isn't there for o2's cashier, nor through o2.
	h.do(http.MethodGet, "/api/v1/outlets/o2/orders/"+o.OrderID, "other", "", nil).status(http.StatusNotFound, "order_not_found")
	h.do(http.MethodGet, ordersPath+"/"+o.OrderID, "other", "", nil).status(http.StatusNotFound, "outlet_not_found")
}

func TestVoid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, ptr(10))
	jeruk := h.menu("o1", h.drinks, "Es jeruk", 8000, nil)
	roti := h.menu("o1", h.drinks, "Roti", 15000, ptr(4))
	tl := h.till("o1", "Kasir")
	o := h.sell(tl, "cashier", h.order(tl, item(kopi, 2), item(jeruk, 1), item(kopi, 1), item(roti, 1)))
	// After the sale, the bread is deleted: its stock stays as it is.
	if err := h.menus.DeleteMenu(t.Context(), principals["manager"], "", "o1", roti.MenuID); err != nil {
		t.Fatal(err)
	}
	path := ordersPath + "/" + o.OrderID + "/void"

	for reason, rule := range map[string]string{"": "required", "   ": "required", strings.Repeat("x", models.MaxVoidReason+1): "max"} {
		r := h.do(http.MethodPost, path, "voider", "", map[string]any{"reason": reason}).
			status(http.StatusUnprocessableEntity, "validation_failed")
		if r.fields()["reason"] != rule {
			t.Errorf("reason %q: fields = %v, want %s", reason, r.fields(), rule)
		}
	}

	var v models.OrderResponse
	h.do(http.MethodPost, path, "voider", "", map[string]any{"reason": "  Salah input  "}).
		status(http.StatusOK, "").decode(&v)
	if v.Status != models.OrderVoid || v.Payment.Status != models.OrderVoid || v.VoidedAt == nil || v.VoidReason == nil ||
		*v.VoidReason != "Salah input" || v.VoidedBy == nil || *v.VoidedBy != (models.PersonResponse{UserID: "v1", Name: "Andi"}) {
		t.Errorf("voided = %+v", v)
	}
	// Everything else stays as it was taken.
	if v.Total != o.Total || v.OrderNo != o.OrderNo || len(v.Items) != 4 || *v.Payment.Received != *o.Payment.Received {
		t.Errorf("voided = %+v, taken %+v", v, o)
	}
	// What it sold goes back, once per menu, as a void naming the order.
	if got := h.stock(kopi.MenuID); got != 10 {
		t.Errorf("stock = %d, want 10", got)
	}
	var kind, userID, orderID string
	var change int
	h.scalar(&kind, `SELECT type FROM stock_movements WHERE menu_id = $1 ORDER BY created_at DESC LIMIT 1`, kopi.MenuID)
	h.scalar(&userID, `SELECT user_id FROM stock_movements WHERE menu_id = $1 AND type = 'void'`, kopi.MenuID)
	h.scalar(&orderID, `SELECT order_id FROM stock_movements WHERE menu_id = $1 AND type = 'void'`, kopi.MenuID)
	h.scalar(&change, `SELECT qty_change FROM stock_movements WHERE menu_id = $1 AND type = 'void'`, kopi.MenuID)
	if kind != "void" || userID != "v1" || orderID != o.OrderID || change != 3 {
		t.Errorf("movement: %s by %s for %s, %d", kind, userID, orderID, change)
	}
	if got := h.stock(roti.MenuID); got != 3 {
		t.Errorf("a deleted menu's stock = %d, want the 3 it had", got)
	}
	if got := h.actions(o.OrderID); !slices.Equal(got, []string{"order.created", "order.voided"}) {
		t.Errorf("log = %v", got)
	}

	// Once.
	h.do(http.MethodPost, path, "voider", "", map[string]any{"reason": "Lagi"}).status(http.StatusConflict, "order_already_void")
	if got := h.stock(kopi.MenuID); got != 10 {
		t.Errorf("stock = %d after a second void", got)
	}
	h.do(http.MethodPost, ordersPath+"/nope/void", "voider", "", map[string]any{"reason": "x"}).
		status(http.StatusNotFound, "order_not_found")
	// The list shows it void.
	if l := h.list("cashier", ""); l.Data[0].Status != models.OrderVoid {
		t.Errorf("listed as %s", l.Data[0].Status)
	}
}

func TestTwoVoidsAtOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, ptr(10))
	tl := h.till("o1", "Kasir")
	o := h.sell(tl, "cashier", h.order(tl, item(kopi, 2)))

	// Hold the order, so both voids wait for it: one voids, the other finds
	// it void.
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM orders WHERE order_id = $1 FOR UPDATE`, o.OrderID); err != nil {
		t.Fatal(err)
	}
	codes := make(chan int, 2)
	for _, who := range []string{"voider", "manager"} {
		go func() {
			codes <- h.do(http.MethodPost, ordersPath+"/"+o.OrderID+"/void", who, "", map[string]any{"reason": "Batal"}).Code
		}()
	}
	h.waitForLock(2, waitingOn("orders"))
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	got := []int{<-codes, <-codes}
	slices.Sort(got)
	if !slices.Equal(got, []int{http.StatusOK, http.StatusConflict}) {
		t.Errorf("statuses = %v, want one 200 and one 409", got)
	}
	if got := h.stock(kopi.MenuID); got != 10 {
		t.Errorf("stock = %d, want 10: put back once", got)
	}
}

func TestVoidAMenuNoLongerTracked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, ptr(10))
	tl := h.till("o1", "Kasir")
	o := h.sell(tl, "cashier", h.order(tl, item(kopi, 2)))
	if _, err := h.menus.UpdateMenu(t.Context(), principals["manager"], "", "o1", kopi.MenuID, menuInputUntracked(kopi)); err != nil {
		t.Fatal(err)
	}
	h.do(http.MethodPost, ordersPath+"/"+o.OrderID+"/void", "manager", "", map[string]any{"reason": "Batal"}).
		status(http.StatusOK, "")
	// Its count is kept, unused, as it was.
	if got := h.stock(kopi.MenuID); got != 8 {
		t.Errorf("stock = %d, want 8", got)
	}
}

// menuInputUntracked is the menu as its form would send it, with stock
// tracking turned off.
func menuInputUntracked(m models.Menu) models.MenuInput {
	in := menuInput(m, m.Price)
	in.TrackStock = false
	in.StockAlert = nil
	return in
}

func TestOrderAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	o := h.sell(tl, "cashier", h.order(tl, item(kopi, 1)))
	path := ordersPath + "/" + o.OrderID

	// Selling doesn't void; the Owner, and whoever only manages the menu,
	// do neither and see no orders.
	h.do(http.MethodPost, path+"/void", "cashier", "", map[string]any{"reason": "x"}).status(http.StatusForbidden, "forbidden")
	for _, who := range []string{"owner", "staff"} {
		h.do(http.MethodGet, ordersPath, who, "", nil).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodGet, path, who, "", nil).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPost, path+"/void", who, "", map[string]any{"reason": "x"}).status(http.StatusForbidden, "forbidden")
	}
	// Another outlet's can't reach it, and nobody signed in is turned away.
	h.do(http.MethodPost, path+"/void", "other", "", map[string]any{"reason": "x"}).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodPost, "/api/v1/outlets/o2/orders/"+o.OrderID+"/void", "other", "", map[string]any{"reason": "x"}).
		status(http.StatusNotFound, "order_not_found")
	h.do(http.MethodGet, ordersPath, "", "", nil).status(http.StatusUnauthorized, "unauthorized")

	var status string
	h.scalar(&status, `SELECT status FROM orders WHERE order_id = $1`, o.OrderID)
	if status != models.OrderPaid {
		t.Errorf("status = %s after the refused voids", status)
	}
}
