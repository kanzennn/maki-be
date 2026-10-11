package menu

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// trackedMenu makes a menu of the category whose stock starts at stock.
func (h *harness) trackedMenu(categoryID, name string, stock int) menuResponse {
	h.t.Helper()
	return h.createMenu(map[string]any{
		"name": name, "category_id": categoryID, "price": 10000, "track_stock": true, "initial_stock": stock,
	})
}

func (h *harness) adjust(menuID, who string, body map[string]any) response {
	h.t.Helper()
	return h.do(http.MethodPost, menus+"/"+menuID+"/stock-adjustments", who, body)
}

func (h *harness) history(menuID, query string) httpx.List[movementResponse] {
	h.t.Helper()
	var list httpx.List[movementResponse]
	h.do(http.MethodGet, menus+"/"+menuID+"/stock-movements"+query, "manager", nil).status(http.StatusOK, "").decode(&list)
	return list
}

func (h *harness) menuStock(menuID string) int {
	h.t.Helper()
	var m menuResponse
	h.do(http.MethodGet, menus+"/"+menuID, "manager", nil).status(http.StatusOK, "").decode(&m)
	return m.StockQty
}

// checkLedger checks that the menu's count is what its history adds up to,
// and that the history, newest first, reads as one unbroken chain: each
// entry starts from the count the one before it left.
func (h *harness) checkLedger(menuID string) {
	h.t.Helper()
	stock, list := h.menuStock(menuID), h.history(menuID, "?page_size=100")
	sum := 0
	for i, m := range list.Data {
		sum += m.QtyChange
		if i+1 < len(list.Data) && m.StockAfter-m.QtyChange != list.Data[i+1].StockAfter {
			h.t.Errorf("entry %d starts from %d, but the one before it left %d", i, m.StockAfter-m.QtyChange, list.Data[i+1].StockAfter)
		}
	}
	if len(list.Data) > 0 && list.Data[0].StockAfter != stock {
		h.t.Errorf("the latest entry leaves %d, but the menu counts %d", list.Data[0].StockAfter, stock)
	}
	if sum != stock {
		h.t.Errorf("the history adds up to %d, but the menu counts %d", sum, stock)
	}
}

func TestAdjustStock(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cakes := h.createCategory("Kue", "🍰")
	cake := h.trackedMenu(cakes.CategoryID, "Bolu", 10)

	var m menuResponse
	h.adjust(cake.MenuID, "manager", map[string]any{"type": "in", "qty": 5, "reason": "restock"}).status(http.StatusOK, "").decode(&m)
	if m.StockQty != 15 || m.StockStatus != StockOK || m.MenuID != cake.MenuID {
		t.Errorf("after adding 5: %+v", m)
	}
	h.adjust(cake.MenuID, "storekeeper", map[string]any{"type": "out", "qty": 3, "reason": "damaged", "note": " Basi "}).status(http.StatusOK, "")

	// Taking more than there is leaves the count alone.
	r := h.adjust(cake.MenuID, "manager", map[string]any{"type": "out", "qty": 13, "reason": "correction"}).
		status(http.StatusConflict, "stock_insufficient")
	if got := r.envelope().Error.Details["available"]; got != float64(12) {
		t.Errorf("available = %v, want 12", got)
	}
	h.adjust(cake.MenuID, "manager", map[string]any{"type": "out", "qty": 12, "reason": "correction"}).status(http.StatusOK, "").decode(&m)
	if m.StockQty != 0 || m.StockStatus != StockOut {
		t.Errorf("after taking all: %+v", m)
	}

	// A stock take sets the count, and is recorded even when it finds the
	// count as it was.
	h.adjust(cake.MenuID, "manager", map[string]any{"type": "set", "qty": 7, "reason": "correction"}).status(http.StatusOK, "")
	h.adjust(cake.MenuID, "storekeeper", map[string]any{"type": "set", "qty": 7, "reason": "other", "note": "Cek mingguan"}).status(http.StatusOK, "")

	// The count never passes MaxStock.
	h.adjust(cake.MenuID, "manager", map[string]any{"type": "set", "qty": MaxStock, "reason": "correction"}).status(http.StatusOK, "")
	r = h.adjust(cake.MenuID, "manager", map[string]any{"type": "in", "qty": 1, "reason": "restock"}).
		status(http.StatusUnprocessableEntity, "validation_failed")
	if r.fields()["qty"] != "range" {
		t.Errorf("fields = %v, want qty: range", r.fields())
	}

	want := []string{
		"initial initial 10→10 by m1",
		"in restock 5→15 by m1",
		"out damaged -3→12 by g1",
		"out correction -12→0 by m1",
		"set correction 7→7 by m1",
		"set other 0→7 by g1",
		fmt.Sprintf("set correction %d→%d by m1", MaxStock-7, MaxStock),
	}
	if got := h.movements(cake.MenuID); !slices.Equal(got, want) {
		t.Errorf("movements =\n%v\nwant\n%v", got, want)
	}
	var note string
	h.scalar(&note, `SELECT note FROM stock_movements WHERE menu_id = $1 AND reason = 'damaged'`, cake.MenuID)
	if note != "Basi" {
		t.Errorf("note = %q, want it trimmed", note)
	}
	h.checkLedger(cake.MenuID)

	// Every adjustment is in the Log Action, and the refused ones aren't.
	adjusted := 0
	for _, a := range h.actions(cake.MenuID) {
		if a == "stock.adjusted" {
			adjusted++
		}
	}
	if adjusted != 6 {
		t.Errorf("%d stock.adjusted entries, want 6", adjusted)
	}
	action, before, after := h.lastLog(cake.MenuID)
	if action != "stock.adjusted" || before["stock_qty"] != float64(7) || after["stock_qty"] != float64(MaxStock) ||
		after["type"] != "set" || after["reason"] != "correction" || after["note"] != nil {
		t.Errorf("Log Action = %s %v → %v", action, before, after)
	}
}

func TestAdjustStockRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cakes := h.createCategory("Kue", "")
	cake := h.trackedMenu(cakes.CategoryID, "Bolu", 10)

	for name, tt := range map[string]struct {
		body        map[string]any
		field, rule string
	}{
		"no type":            {map[string]any{"qty": 1, "reason": "restock"}, "type", "required"},
		"unknown type":       {map[string]any{"type": "add", "qty": 1, "reason": "restock"}, "type", "oneof"},
		"a sale":             {map[string]any{"type": "sale", "qty": 1, "reason": "restock"}, "type", "oneof"},
		"no amount":          {map[string]any{"type": "in", "reason": "restock"}, "qty", "required"},
		"nothing added":      {map[string]any{"type": "in", "qty": 0, "reason": "restock"}, "qty", "range"},
		"nothing taken":      {map[string]any{"type": "out", "qty": 0, "reason": "damaged"}, "qty", "range"},
		"a negative amount":  {map[string]any{"type": "out", "qty": -2, "reason": "damaged"}, "qty", "range"},
		"a negative count":   {map[string]any{"type": "set", "qty": -1, "reason": "correction"}, "qty", "range"},
		"too much at once":   {map[string]any{"type": "in", "qty": MaxStock + 1, "reason": "restock"}, "qty", "range"},
		"no reason":          {map[string]any{"type": "in", "qty": 1}, "reason", "required"},
		"unknown reason":     {map[string]any{"type": "out", "qty": 1, "reason": "stolen"}, "reason", "oneof"},
		"the start's reason": {map[string]any{"type": "set", "qty": 1, "reason": "initial"}, "reason", "oneof"},
		"other, unexplained": {map[string]any{"type": "out", "qty": 1, "reason": "other", "note": "  "}, "note", "required"},
		"long note":          {map[string]any{"type": "in", "qty": 1, "reason": "restock", "note": strings.Repeat("n", 201)}, "note", "max"},
	} {
		t.Run(name, func(t *testing.T) {
			r := h.adjust(cake.MenuID, "manager", tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
			if r.fields()[tt.field] != tt.rule {
				t.Errorf("fields = %v, want %s: %s", r.fields(), tt.field, tt.rule)
			}
		})
	}

	// The edges that pass: a count of 0, a note of 200 characters (an emoji
	// is one), an amount of MaxStock at once.
	h.adjust(cake.MenuID, "manager", map[string]any{
		"type": "set", "qty": 0, "reason": "correction", "note": strings.Repeat("🍰", 200),
	}).status(http.StatusOK, "")
	h.adjust(cake.MenuID, "manager", map[string]any{"type": "in", "qty": MaxStock, "reason": "other", "note": "Pesanan besar"}).
		status(http.StatusOK, "")
	if got := h.movements(cake.MenuID); len(got) != 3 {
		t.Errorf("movements = %v, want the start and the two that passed", got)
	}

	// An untracked menu has no count to change; a deleted menu, or another
	// outlet's, isn't there.
	juice := h.createMenu(map[string]any{"name": "Es Jeruk", "category_id": cakes.CategoryID, "price": 8000})
	add := map[string]any{"type": "in", "qty": 1, "reason": "restock"}
	h.adjust(juice.MenuID, "manager", add).status(http.StatusConflict, "stock_not_tracked")
	if got := h.movements(juice.MenuID); len(got) != 0 {
		t.Errorf("movements = %v, want none", got)
	}
	gone := h.trackedMenu(cakes.CategoryID, "Brownies", 4)
	h.do(http.MethodDelete, menus+"/"+gone.MenuID, "manager", nil).status(http.StatusNoContent, "")
	h.adjust(gone.MenuID, "manager", add).status(http.StatusNotFound, "menu_not_found")
	h.adjust("nope", "manager", add).status(http.StatusNotFound, "menu_not_found")
	var theirs menuResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/menus", "manager2", map[string]any{
		"name": "Bolu", "category_id": h.otherOutletCategory(), "price": 1, "track_stock": true, "initial_stock": 3,
	}).status(http.StatusCreated, "").decode(&theirs)
	h.adjust(theirs.MenuID, "manager", add).status(http.StatusNotFound, "menu_not_found")
	if got := h.movements(theirs.MenuID); len(got) != 1 {
		t.Errorf("the other outlet's movements = %v, want only its start", got)
	}
}

func TestStockAlert(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cakes := h.createCategory("Kue", "")
	cake := h.trackedMenu(cakes.CategoryID, "Bolu", 10)
	path := menus + "/" + cake.MenuID + "/stock-alert"

	var m menuResponse
	h.do(http.MethodPut, path, "storekeeper", map[string]any{"stock_alert": 5}).status(http.StatusOK, "").decode(&m)
	if *m.StockAlert != 5 || m.StockStatus != StockOK || m.StockQty != 10 {
		t.Errorf("alert at 5: %+v", m)
	}
	action, before, after := h.lastLog(cake.MenuID)
	if action != "menu.updated" || before["stock_alert"] != nil || after["stock_alert"] != float64(5) || len(after) != 1 {
		t.Errorf("Log Action = %s %v → %v", action, before, after)
	}
	h.do(http.MethodPut, path, "storekeeper", map[string]any{"stock_alert": 5}).status(http.StatusOK, "")
	if n := len(h.actions(cake.MenuID)); n != 2 {
		t.Errorf("%d entries, want no new one for no change", n)
	}

	h.do(http.MethodPut, path, "storekeeper", map[string]any{"stock_alert": 10}).status(http.StatusOK, "").decode(&m)
	if m.StockStatus != StockLow {
		t.Errorf("stock 10 with the alert at 10 is %s, want low", m.StockStatus)
	}
	h.do(http.MethodPut, path, "storekeeper", map[string]any{"stock_alert": nil}).status(http.StatusOK, "").decode(&m)
	if m.StockAlert != nil || m.StockStatus != StockOK {
		t.Errorf("alert off: %+v", m)
	}
	if got := h.movements(cake.MenuID); len(got) != 1 {
		t.Errorf("movements = %v, want the alert to leave the count alone", got)
	}

	for _, alert := range []int{-1, MaxStock + 1} {
		r := h.do(http.MethodPut, path, "storekeeper", map[string]any{"stock_alert": alert}).
			status(http.StatusUnprocessableEntity, "validation_failed")
		if r.fields()["stock_alert"] != "range" {
			t.Errorf("alert %d: fields = %v, want stock_alert: range", alert, r.fields())
		}
	}
	juice := h.createMenu(map[string]any{"name": "Es Jeruk", "category_id": cakes.CategoryID, "price": 8000})
	h.do(http.MethodPut, menus+"/"+juice.MenuID+"/stock-alert", "storekeeper", map[string]any{"stock_alert": 3}).
		status(http.StatusConflict, "stock_not_tracked")
	h.do(http.MethodPut, menus+"/nope/stock-alert", "storekeeper", map[string]any{"stock_alert": 3}).
		status(http.StatusNotFound, "menu_not_found")
}

func TestStockHistory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cakes := h.createCategory("Kue", "")
	cake := h.trackedMenu(cakes.CategoryID, "Bolu", 10)
	h.adjust(cake.MenuID, "storekeeper", map[string]any{"type": "in", "qty": 5, "reason": "restock"}).status(http.StatusOK, "")
	h.adjust(cake.MenuID, "manager", map[string]any{"type": "out", "qty": 2, "reason": "damaged", "note": "Jatuh"}).status(http.StatusOK, "")
	h.adjust(cake.MenuID, "storekeeper", map[string]any{"type": "set", "qty": 12, "reason": "correction"}).status(http.StatusOK, "")
	// The storekeeper leaves; the history still names them.
	h.exec(`UPDATE users SET deleted_at = now() WHERE user_id = 'g1'`)

	describe := func(ms []movementResponse) []string {
		out := make([]string, len(ms))
		for i, m := range ms {
			out[i] = fmt.Sprintf("%s %s %d→%d by %s", m.Type, *m.Reason, m.QtyChange, m.StockAfter, m.User.Name)
		}
		return out
	}
	list := h.history(cake.MenuID, "")
	want := []string{
		"set correction -1→12 by Gita",
		"out damaged -2→13 by Rina",
		"in restock 5→15 by Gita",
		"initial initial 10→10 by Rina",
	}
	if got := describe(list.Data); !slices.Equal(got, want) {
		t.Errorf("history =\n%v\nwant, newest first,\n%v", got, want)
	}
	if list.Meta.Total != 4 || *list.Data[1].Note != "Jatuh" || list.Data[0].Note != nil ||
		list.Data[0].User.UserID != "g1" || list.Data[0].OrderID != nil || list.Data[0].CreatedAt.IsZero() {
		t.Errorf("history = %+v", list)
	}
	h.checkLedger(cake.MenuID)

	page := h.history(cake.MenuID, "?page=2&page_size=3")
	if got := describe(page.Data); !slices.Equal(got, want[3:]) || page.Meta.Total != 4 || page.Meta.Page != 2 {
		t.Errorf("page 2 of 3 = %v %+v, want the start", got, page.Meta)
	}
	h.do(http.MethodGet, menus+"/"+cake.MenuID+"/stock-movements?page_size=0", "manager", nil).
		status(http.StatusUnprocessableEntity, "validation_failed")

	// A menu no longer tracked keeps its history; an untracked one has none.
	h.do(http.MethodPut, menus+"/"+cake.MenuID, "manager", map[string]any{
		"name": "Bolu", "category_id": cakes.CategoryID, "price": 10000, "track_stock": false,
	}).status(http.StatusOK, "")
	if l := h.history(cake.MenuID, ""); l.Meta.Total != 4 {
		t.Errorf("%d entries once untracked, want the 4 kept", l.Meta.Total)
	}
	juice := h.createMenu(map[string]any{"name": "Es Jeruk", "category_id": cakes.CategoryID, "price": 8000})
	if l := h.history(juice.MenuID, ""); len(l.Data) != 0 || l.Data == nil {
		t.Errorf("history = %#v, want an empty list", l.Data)
	}

	// Another outlet's menu, or a deleted one, isn't there.
	var theirs menuResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/menus", "manager2", map[string]any{
		"name": "Bolu", "category_id": h.otherOutletCategory(), "price": 1, "track_stock": true, "initial_stock": 3,
	}).status(http.StatusCreated, "").decode(&theirs)
	h.do(http.MethodGet, menus+"/"+theirs.MenuID+"/stock-movements", "manager", nil).status(http.StatusNotFound, "menu_not_found")
	h.do(http.MethodDelete, menus+"/"+cake.MenuID, "manager", nil).status(http.StatusNoContent, "")
	h.do(http.MethodGet, menus+"/"+cake.MenuID+"/stock-movements", "manager", nil).status(http.StatusNotFound, "menu_not_found")
}

// Adjustments that arrive together wait for the menu in turn, each from
// the count the one before it left: none is lost. The test holds the menu,
// as a long adjustment would, until every request is waiting for it, so
// that without the lock they would all read the same count.
func TestConcurrentAdjustments(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cakes := h.createCategory("Kue", "")
	cake := h.trackedMenu(cakes.CategoryID, "Bolu", 100)
	// One connection holds the menu; each request needs one of its own.
	n := min(8, int(h.pool.Config().MaxConns)-2)
	if n < 2 {
		t.Skipf("the pool allows %d connections; the test needs 4", h.pool.Config().MaxConns)
	}

	ctx := context.WithoutCancel(t.Context())
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM menus WHERE menu_id = $1 FOR UPDATE`, cake.MenuID); err != nil {
		t.Fatal(err)
	}

	codes := make([]int, n)
	var wg sync.WaitGroup
	want := 100
	for i := range n {
		body := map[string]any{"type": "in", "qty": 3, "reason": "restock"}
		want += 3
		if i%2 == 1 {
			body = map[string]any{"type": "out", "qty": 2, "reason": "damaged"}
			want -= 5
		}
		wg.Go(func() {
			codes[i] = h.adjust(cake.MenuID, []string{"manager", "storekeeper"}[i%2], body).Code
		})
	}

	// Every request waits on the menu's row: the first holds its tuple
	// lock, the others queue for it.
	deadline := time.Now().Add(10 * time.Second)
	for waiting := 0; waiting < n; {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d requests waiting for the menu after 10s", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
		h.scalar(&waiting, `SELECT count(DISTINCT pid) FROM pg_locks WHERE locktype = 'tuple' AND relation = 'menus'::regclass`)
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("request %d: status %d", i, code)
		}
	}
	if got := h.menuStock(cake.MenuID); got != want {
		t.Errorf("stock = %d, want %d: an adjustment was lost", got, want)
	}
	if got := h.movements(cake.MenuID); len(got) != n+1 {
		t.Errorf("%d movements, want %d", len(got), n+1)
	}
	h.checkLedger(cake.MenuID)
}

func TestStockAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cakes := h.createCategory("Kue", "")
	cake := h.trackedMenu(cakes.CategoryID, "Bolu", 10)
	adjust := func(who string) response {
		return h.adjust(cake.MenuID, who, map[string]any{"type": "in", "qty": 1, "reason": "restock"})
	}
	alert := func(who string) response {
		return h.do(http.MethodPut, menus+"/"+cake.MenuID+"/stock-alert", who, map[string]any{"stock_alert": 2})
	}
	history := func(who string) response {
		return h.do(http.MethodGet, menus+"/"+cake.MenuID+"/stock-movements", who, nil)
	}

	// The Owner only looks.
	history("owner").status(http.StatusOK, "")
	adjust("owner").status(http.StatusForbidden, "forbidden")
	alert("owner").status(http.StatusForbidden, "forbidden")
	// A chef sets the alert, a field of the menu form, but counts nothing.
	adjust("chef").status(http.StatusForbidden, "forbidden")
	alert("chef").status(http.StatusOK, "")
	history("chef").status(http.StatusOK, "")
	// A storekeeper counts, and sets the alert.
	adjust("storekeeper").status(http.StatusOK, "")
	alert("storekeeper").status(http.StatusOK, "")
	// A cashier sees none of it, another outlet's Manager can't reach it,
	// and nobody signed in is turned away.
	history("cashier").status(http.StatusForbidden, "forbidden")
	adjust("cashier").status(http.StatusForbidden, "forbidden")
	adjust("manager2").status(http.StatusNotFound, "outlet_not_found")
	history("manager2").status(http.StatusNotFound, "outlet_not_found")
	adjust("").status(http.StatusUnauthorized, "unauthorized")

	if got := h.movements(cake.MenuID); len(got) != 2 {
		t.Errorf("movements = %v, want the start and the storekeeper's", got)
	}
}
