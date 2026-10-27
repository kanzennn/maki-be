package order

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/models"
)

// byStands has the outlet seat its dine-in customers by number stands.
func (h *harness) byStands(outletID string) {
	h.t.Helper()
	if _, err := h.modes.SetDineInMode(h.t.Context(), manager(outletID), "", outletID, models.ModeNumber); err != nil {
		h.t.Fatal(err)
	}
}

// atStand seats the order at the stand: dine-in.
func atStand(body map[string]any, number string) map[string]any {
	body["type"] = models.OrderDineIn
	body["stand_number"] = number
	return body
}

func TestSaleAtStand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	if got := h.catalog().DineInMode; got != "table" {
		t.Errorf("the catalog's mode = %s, want table", got)
	}
	h.byStands("o1")
	if got := h.catalog().DineInMode; got != "number" {
		t.Errorf("the catalog's mode = %s, want number", got)
	}

	// As typed, spaces and small letters aside.
	first := h.sell(tl, "cashier", atStand(h.order(tl, item(kopi, 2)), " b-3 "))
	if first.Type != models.OrderDineIn || first.StandNumber == nil || *first.StandNumber != "B-3" || first.TableID != nil {
		t.Fatalf("sold at stand B-3 = %+v", first)
	}
	// Another order at the stand is another sale; a takeaway needs none.
	h.sell(tl, "cashier", atStand(h.order(tl, item(kopi, 1)), "B-3"))
	away := h.sell(tl, "cashier", h.order(tl, item(kopi, 1)))
	if away.StandNumber != nil {
		t.Errorf("a takeaway's stand = %v", *away.StandNumber)
	}

	var got models.OrderResponse
	h.do(http.MethodGet, "/api/v1/outlets/o1/orders/"+first.OrderID, "cashier", "", nil).status(http.StatusOK, "").decode(&got)
	if got.StandNumber == nil || *got.StandNumber != "B-3" {
		t.Errorf("the receipt's stand = %v", got.StandNumber)
	}
	var list struct {
		Data []models.OrderSummaryResponse `json:"data"`
	}
	h.do(http.MethodGet, "/api/v1/outlets/o1/orders", "cashier", "", nil).status(http.StatusOK, "").decode(&list)
	stands := map[string]string{}
	for _, s := range list.Data {
		stands[s.OrderID] = "none"
		if s.StandNumber != nil {
			stands[s.OrderID] = *s.StandNumber
		}
	}
	if len(list.Data) != 3 || stands[first.OrderID] != "B-3" || stands[away.OrderID] != "none" {
		t.Errorf("today's orders' stands = %v", stands)
	}
	var logged string
	h.scalar(&logged, `SELECT after_json->>'stand_number' FROM action_logs WHERE target_id = $1 AND action = 'order.created'`,
		first.OrderID)
	if logged != "B-3" {
		t.Errorf("order.created logged the stand as %q", logged)
	}
}

func TestSaleAtStandRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	five := 5
	cake := h.menu("o1", h.drinks, "Bolu", 25000, &five)
	tl := h.till("o1", "Kasir")
	h.newVoucher("NOMOR10", nil)
	a1 := h.newTable("o1", "A1")
	send := func(body map[string]any) reply {
		return h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token,
			withVoucher(tl, body, "NOMOR10", 2500))
	}

	// What no outlet takes: a stand's number on a takeaway, with a table,
	// or not as a stand has it.
	for _, tt := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"a takeaway at a stand", func() map[string]any {
			body := h.order(tl, item(cake, 1))
			body["stand_number"] = "12"
			return body
		}(), "dine_in"},
		{"a stand and a table", atTable(atStand(h.order(tl, item(cake, 1)), "12"), a1), "with_table"},
		{"another sign", atStand(h.order(tl, item(cake, 1)), "12#"), "format"},
		{"a hyphen first", atStand(h.order(tl, item(cake, 1)), "-12"), "format"},
		{"a space inside", atStand(h.order(tl, item(cake, 1)), "A 5"), "format"},
		{"past 10 characters", atStand(h.order(tl, item(cake, 1)), "12345678901"), "format"},
	} {
		r := send(tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
		if got := r.fields()["stand_number"]; got != tt.want {
			t.Errorf("%s: stand_number = %v, want %s", tt.name, got, tt.want)
		}
	}

	// At tables, no stand.
	r := send(atStand(h.order(tl, item(cake, 1)), "12")).status(http.StatusConflict, "dine_in_mode_changed")
	if got := r.envelope().Error.Details["mode"]; got != "table" {
		t.Errorf("a stand where they sit at tables: mode = %v", got)
	}
	// By stands, no table, and none dine-in without its stand. A blank
	// number is none.
	h.byStands("o1")
	for _, tt := range []struct {
		name string
		body map[string]any
	}{
		{"at a table", atTable(h.order(tl, item(cake, 1)), a1)},
		{"without a stand", func() map[string]any {
			body := h.order(tl, item(cake, 1))
			body["type"] = models.OrderDineIn
			return body
		}()},
		{"a blank stand", atStand(h.order(tl, item(cake, 1)), "  ")},
	} {
		r := send(tt.body).status(http.StatusConflict, "dine_in_mode_changed")
		if got := r.envelope().Error.Details["mode"]; got != "number" {
			t.Errorf("%s: mode = %v, want number", tt.name, got)
		}
	}

	// Nothing was taken: no order, no stock, no use of the voucher.
	if n := h.count(`SELECT count(*) FROM orders`); n != 0 {
		t.Errorf("%d orders taken", n)
	}
	if got := h.stock(cake.MenuID); got != 5 {
		t.Errorf("stock = %d, want 5", got)
	}
	var used int
	h.scalar(&used, `SELECT used_count FROM vouchers WHERE code = 'NOMOR10'`)
	if used != 0 {
		t.Errorf("voucher used %d times", used)
	}
}

func TestResendAtStand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	h.byStands("o1")
	body := atStand(h.order(tl, item(kopi, 1)), "7")
	first := h.sell(tl, "cashier", body)

	var again models.OrderResponse
	h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, body).status(http.StatusOK, "").decode(&again)
	if again.OrderID != first.OrderID || again.StandNumber == nil || *again.StandNumber != "7" {
		t.Errorf("resent = %+v, want %s at 7", again, first.OrderID)
	}
	// Its ID at another stand is another order.
	body["stand_number"] = "8"
	h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, body).status(http.StatusConflict, "order_id_conflict")
	if n := h.count(`SELECT count(*) FROM orders`); n != 1 {
		t.Errorf("%d orders taken, want 1", n)
	}
}

// The resend of a sale at a stand typed in small letters is the same order.
func TestResendAtStandAsTyped(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	h.byStands("o1")
	body := atStand(h.order(tl, item(kopi, 1)), "a5")
	first := h.sell(tl, "cashier", body)
	body["stand_number"] = "A5"
	var again models.OrderResponse
	h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, body).status(http.StatusOK, "").decode(&again)
	if again.OrderID != first.OrderID {
		t.Errorf("resent in capitals = %s, want %s", again.OrderID, first.OrderID)
	}
}

// A change of the mode waits for a sale under way, which is saved under the
// mode it began with.
func TestModeChangeWaitsForTheSale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	h.byStands("o1")
	hold, sale := h.heldSale(tl, atStand(h.order(tl, item(kopi, 1)), "12"))

	changed := make(chan error, 1)
	go func() {
		_, err := h.modes.SetDineInMode(context.WithoutCancel(h.t.Context()), manager("o1"), "", "o1", models.ModeTable)
		changed <- err
	}()
	// The sale waits for hold, and the change for the sale.
	h.waitForLock(2, `SELECT count(*) FROM pg_stat_activity WHERE cardinality(pg_blocking_pids(pid)) > 0`)
	select {
	case err := <-changed:
		t.Fatalf("the change went first: %v", err)
	default:
	}
	if err := hold.Rollback(context.WithoutCancel(h.t.Context())); err != nil {
		t.Fatal(err)
	}
	var o models.OrderResponse
	(<-sale).status(http.StatusCreated, "").decode(&o)
	if o.StandNumber == nil || *o.StandNumber != "12" {
		t.Errorf("the sale = %+v, want at stand 12", o)
	}
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	if got := h.catalog().DineInMode; got != "table" {
		t.Errorf("after the sale, the mode = %s, want table", got)
	}
}

// An outlet's first change of mode, before it has a setting saved to lock,
// waits for the sale under way too.
func TestFirstModeChangeWaitsForTheSale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	body := h.order(tl, item(kopi, 1))
	body["type"] = models.OrderDineIn
	hold, sale := h.heldSale(tl, body)

	changed := make(chan error, 1)
	go func() {
		_, err := h.modes.SetDineInMode(context.WithoutCancel(h.t.Context()), manager("o1"), "", "o1", models.ModeNumber)
		changed <- err
	}()
	// The sale waits for hold, and the change for the sale.
	h.waitForLock(2, `SELECT count(*) FROM pg_stat_activity WHERE cardinality(pg_blocking_pids(pid)) > 0`)
	select {
	case err := <-changed:
		t.Fatalf("the change went first: %v", err)
	default:
	}
	if err := hold.Rollback(context.WithoutCancel(h.t.Context())); err != nil {
		t.Fatal(err)
	}
	var o models.OrderResponse
	(<-sale).status(http.StatusCreated, "").decode(&o)
	if o.Type != models.OrderDineIn || o.StandNumber != nil {
		t.Errorf("the sale = %+v, want dine-in without a stand", o)
	}
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	if got := h.catalog().DineInMode; got != "number" {
		t.Errorf("after the sale, the mode = %s, want number", got)
	}
}

// Sales hold the outlet's mode together: one under way keeps no other
// waiting.
func TestSalesShareTheMode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	jus := h.menu("o1", h.drinks, "Jus Jeruk", 15000, nil)
	tl := h.till("o1", "Kasir")
	h.byStands("o1")
	hold, sale := h.heldSale(tl, atStand(h.order(tl, item(kopi, 1)), "12"))

	// Another till sells another menu meanwhile, and is taken at once.
	other := h.till("o1", "Kasir dua")
	done := make(chan reply, 1)
	go func() {
		done <- h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", other.token,
			atStand(h.order(other, item(jus, 1)), "7"))
	}()
	select {
	case r := <-done:
		r.status(http.StatusCreated, "")
	case <-time.After(10 * time.Second):
		t.Fatal("a sale waited for another under way")
	}
	if err := hold.Rollback(context.WithoutCancel(h.t.Context())); err != nil {
		t.Fatal(err)
	}
	(<-sale).status(http.StatusCreated, "")
}
