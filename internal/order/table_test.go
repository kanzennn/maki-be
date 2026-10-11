package order

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/table"
	"github.com/kervzent/maki-be/internal/voucher"
)

// newTable makes a table of the outlet, in an area of its own, through the
// table module, and returns its ID.
func (h *harness) newTable(outletID, name string) string {
	h.t.Helper()
	a, err := h.tables.CreateArea(h.t.Context(), manager(outletID), "", outletID, table.AreaInput{Name: "Area " + name})
	if err != nil {
		h.t.Fatal(err)
	}
	tb, err := h.tables.CreateTable(h.t.Context(), manager(outletID), "", outletID, table.TableInput{Name: name, AreaID: a.AreaID})
	if err != nil {
		h.t.Fatal(err)
	}
	return tb.TableID
}

// table returns o1's table as the table module tells it.
func (h *harness) table(tableID string) table.Table {
	h.t.Helper()
	tb, err := h.tables.Table(h.t.Context(), "o1", tableID)
	if err != nil {
		h.t.Fatal(err)
	}
	return tb
}

// atTable seats the order at the table: dine-in.
func atTable(body map[string]any, tableID string) map[string]any {
	body["type"] = DineIn
	body["table_id"] = tableID
	return body
}

func TestSaleAtTable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	a1 := h.newTable("o1", "A1")

	first := h.sell(tl, "cashier", atTable(h.order(tl, item(kopi, 2)), a1))
	if first.Type != DineIn || first.TableID == nil || *first.TableID != a1 || first.TableName == nil || *first.TableName != "A1" {
		t.Fatalf("sold at A1 = %+v", first)
	}
	if tb := h.table(a1); tb.Status != table.Occupied || tb.Seated == nil || tb.Seated.Orders != 1 {
		t.Fatalf("A1 after the sale = %+v", tb)
	}
	// Another order at it is another sale there.
	second := h.sell(tl, "cashier", atTable(h.order(tl, item(kopi, 1)), a1))
	if tb := h.table(a1); tb.Seated == nil || tb.Seated.Orders != 2 {
		t.Errorf("A1 after two sales = %+v", tb.Seated)
	}

	// Renamed since: the receipts keep the name they were sold with.
	areaID := h.table(a1).AreaID
	if _, err := h.tables.UpdateTable(h.t.Context(), manager("o1"), "", "o1", a1, table.TableInput{Name: "VIP 1", AreaID: areaID}); err != nil {
		t.Fatal(err)
	}
	var got orderResponse
	h.do(http.MethodGet, "/api/v1/outlets/o1/orders/"+first.OrderID, "cashier", "", nil).status(http.StatusOK, "").decode(&got)
	if got.TableName == nil || *got.TableName != "A1" {
		t.Errorf("the receipt after a rename = %v", got.TableName)
	}
	var list struct {
		Data []orderSummaryResponse `json:"data"`
	}
	h.do(http.MethodGet, "/api/v1/outlets/o1/orders", "cashier", "", nil).status(http.StatusOK, "").decode(&list)
	if len(list.Data) != 2 || list.Data[0].TableName == nil || *list.Data[0].TableName != "A1" {
		t.Errorf("today's orders = %+v", list.Data)
	}
	var logged string
	h.scalar(&logged, `SELECT after_json->>'table' FROM action_logs WHERE target_id = $1 AND action = 'order.created'`, first.OrderID)
	if logged != "A1" {
		t.Errorf("order.created logged the table as %q", logged)
	}

	// Voided, each: nobody is seated any more.
	for _, o := range []orderResponse{first, second} {
		h.do(http.MethodPost, "/api/v1/outlets/o1/orders/"+o.OrderID+"/void", "manager", "", map[string]any{"reason": "Salah"}).
			status(http.StatusOK, "")
	}
	if tb := h.table(a1); tb.Status != table.Available {
		t.Errorf("A1 after both voids = %s", tb.Status)
	}
}

func TestSaleAtTableRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	five := 5
	cake := h.menu("o1", h.drinks, "Bolu", 25000, &five)
	tl := h.till("o1", "Kasir")
	h.newVoucher("MEJA10", nil)
	off := h.newTable("o1", "Off")
	if _, err := h.tables.SetTableActive(h.t.Context(), manager("o1"), "", "o1", off, false); err != nil {
		t.Fatal(err)
	}
	scheduled := h.newTable("o1", "Acara")
	h.exec(`INSERT INTO table_schedules (schedule_id, outlet_id, title, start_at, end_at, created_by)
		VALUES ('s1', 'o1', 'Acara', now() - interval '1 hour', now() + interval '1 hour', 'm1')`)
	h.exec(`INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('s1', $1, 'o1')`, scheduled)
	gone := h.newTable("o1", "Gone")
	if err := h.tables.DeleteTable(h.t.Context(), manager("o1"), "", "o1", gone); err != nil {
		t.Fatal(err)
	}
	theirs := h.newTable("o2", "B1")

	// A takeaway has no table.
	r := h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, func() map[string]any {
		body := h.order(tl, item(cake, 1))
		body["table_id"] = off
		return body
	}())
	r.status(http.StatusUnprocessableEntity, "validation_failed")
	if r.fields()["table_id"] != "dine_in" {
		t.Errorf("a takeaway at a table: %v", r.fields())
	}
	for _, tt := range []struct {
		name    string
		tableID string
		status  string
	}{
		{"turned off", off, "inactive"},
		{"scheduled off", scheduled, "scheduled"},
		{"deleted", gone, "missing"},
		{"another outlet's", theirs, "missing"},
		{"not there", "nope", "missing"},
	} {
		body := withVoucher(tl, atTable(h.order(tl, item(cake, 1)), tt.tableID), "MEJA10", 2500)
		r := h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, body)
		r.status(http.StatusConflict, "table_unavailable")
		if r.envelope().Error.Details["status"] != tt.status {
			t.Errorf("%s: details = %v, want %s", tt.name, r.envelope().Error.Details, tt.status)
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
	h.scalar(&used, `SELECT used_count FROM vouchers WHERE code = 'MEJA10'`)
	if used != 0 {
		t.Errorf("voucher used %d times", used)
	}
}

func TestResendAtTable(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	a1, a2 := h.newTable("o1", "A1"), h.newTable("o1", "A2")
	body := atTable(h.order(tl, item(kopi, 1)), a1)
	first := h.sell(tl, "cashier", body)

	var again orderResponse
	h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, body).status(http.StatusOK, "").decode(&again)
	if again.OrderID != first.OrderID {
		t.Errorf("resent = %s, want %s", again.OrderID, first.OrderID)
	}
	if tb := h.table(a1); tb.Seated == nil || tb.Seated.Orders != 1 {
		t.Errorf("A1 after the resend = %+v", tb.Seated)
	}
	// Its ID at another table is another order.
	body["table_id"] = a2
	h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, body).status(http.StatusConflict, "order_id_conflict")
}

func TestTableInTheHash(t *testing.T) {
	t.Parallel()
	c := checked{
		orderID: "o0", orderNo: "BDG01-K01-20261001-0001", kind: DineIn,
		createdAt: time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC),
		lines:     []line{{menuID: "m1", qty: 2, price: 18000}},
		total:     41_580, method: "cash", received: ptr[int64](50_000),
	}
	without, err := c.hash()
	if err != nil {
		t.Fatal(err)
	}
	c.tableID = "t1"
	if with, _ := c.hash(); with == without {
		t.Error("the table isn't in the hash")
	}
}

// A release, and a sale after it.
func TestReleaseThenSell(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	a1 := h.newTable("o1", "A1")
	h.sell(tl, "cashier", atTable(h.order(tl, item(kopi, 1)), a1))
	if _, err := h.tables.Release(h.t.Context(), principals["cashier"], "", "o1", a1); err != nil {
		t.Fatal(err)
	}
	if got := h.table(a1).Status; got != table.Available {
		t.Errorf("released = %s", got)
	}
	h.sell(tl, "cashier", atTable(h.order(tl, item(kopi, 1)), a1))
	if tb := h.table(a1); tb.Status != table.Occupied || tb.Seated.Orders != 1 {
		t.Errorf("sold at again = %+v", tb)
	}
}

// heldSale starts the sale, and holds it once it has its table: an order
// under its ID, from another till and not committed yet, makes its insert
// wait. Rolling hold back lets it go on.
func (h *harness) heldSale(tl *till, body map[string]any) (hold pgx.Tx, done chan response) {
	h.t.Helper()
	ctx := context.WithoutCancel(h.t.Context())
	other := h.till("o1", "Kasir lain")
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = hold.Rollback(ctx) })
	if _, err := hold.Exec(ctx, `INSERT INTO orders (order_id, outlet_id, device_id, order_no, type, cashier_id, subtotal,
		service_bps, service, tax_bps, tax, total, client_created_at, request_hash)
		VALUES ($1, 'o1', $2, $3, 'takeaway', 'k1', 0, 0, 0, 0, 0, 0, now(), repeat('0', 64))`,
		body["order_id"], other.DeviceID, other.prefix+"-"+h.clock.In(jakarta).Format("20060102")+"-0001"); err != nil {
		h.t.Fatal(err)
	}
	var pid int
	if err := hold.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		h.t.Fatal(err)
	}
	done = make(chan response, 1)
	go func() { done <- h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, body) }()
	h.waitForLock(1, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid))`, pid)
	return hold, done
}

// A delete of a table waits for a sale at it under way, which counts.
func TestDeleteWaitsForTheSale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	a1 := h.newTable("o1", "A1")
	hold, sale := h.heldSale(tl, atTable(h.order(tl, item(kopi, 1)), a1))

	deleted := make(chan error, 1)
	go func() {
		deleted <- h.tables.DeleteTable(context.WithoutCancel(h.t.Context()), manager("o1"), "", "o1", a1)
	}()
	h.waitForLock(1, waitingOn("tables"))
	if err := hold.Rollback(context.WithoutCancel(t.Context())); err != nil {
		t.Fatal(err)
	}

	(<-sale).status(http.StatusCreated, "")
	err := <-deleted
	if e, ok := errors.AsType[*httpx.Error](err); !ok || e.Code() != "table_occupied" {
		t.Errorf("the delete = %v, want table_occupied", err)
	}
	if got := h.table(a1).Status; got != table.Occupied {
		t.Errorf("A1 = %s", got)
	}
}

// A release waits for a sale at the table under way, and releases its
// customers too.
func TestReleaseWaitsForTheSale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	a1 := h.newTable("o1", "A1")
	hold, sale := h.heldSale(tl, atTable(h.order(tl, item(kopi, 1)), a1))

	released := make(chan error, 1)
	go func() {
		_, err := h.tables.Release(context.WithoutCancel(h.t.Context()), principals["cashier"], "", "o1", a1)
		released <- err
	}()
	h.waitForLock(1, waitingOn("tables"))
	if err := hold.Rollback(context.WithoutCancel(t.Context())); err != nil {
		t.Fatal(err)
	}

	(<-sale).status(http.StatusCreated, "")
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if got := h.table(a1).Status; got != table.Available {
		t.Errorf("A1 = %s, want the sale's customers released too", got)
	}
	if n := h.count(`SELECT count(*) FROM action_logs WHERE target_id = $1 AND action = 'table.released'`, a1); n != 1 {
		t.Errorf("releases logged = %d", n)
	}
}

// A sale that waited for its voucher while its table was released counts
// after the release: a sale is timed when its row is written, after it
// took the table, not when its transaction began.
func TestSaleAfterAReleaseItWaitedThrough(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	a1 := h.newTable("o1", "A1")
	v := h.newVoucher("TUNGGU", func(in *voucher.Input) { in.Quota = ptr(5) })
	h.sell(tl, "cashier", atTable(h.order(tl, item(kopi, 1)), a1))

	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM vouchers WHERE voucher_id = $1 FOR UPDATE`, v.VoucherID); err != nil {
		t.Fatal(err)
	}
	sale := make(chan response, 1)
	body := withVoucher(tl, atTable(h.order(tl, item(kopi, 1)), a1), "TUNGGU", 1800)
	go func() { sale <- h.do(http.MethodPost, "/api/v1/outlets/o1/orders", "cashier", tl.token, body) }()
	h.waitForLock(1, waitingOn("vouchers"))
	// The table released while the sale waits, then the sale goes on.
	if _, err := h.tables.Release(ctx, principals["cashier"], "", "o1", a1); err != nil {
		t.Fatal(err)
	}
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	(<-sale).status(http.StatusCreated, "")
	if tb := h.table(a1); tb.Status != table.Occupied || tb.Seated.Orders != 1 {
		t.Errorf("A1 = %+v, want the waiting sale's customers seated", tb)
	}
}

// A release that waited for a sale at the table releases its customers
// too, however long after taking the table the sale wrote its order: a
// release is timed when it is written, not when its transaction began.
func TestReleaseTimedWhenWritten(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	tl := h.till("o1", "Kasir")
	a1 := h.newTable("o1", "A1")

	sale, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sale.Rollback(ctx) }()
	if _, err := h.tables.HoldForSale(ctx, sale, "o1", a1); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		_, err := h.tables.Release(ctx, principals["cashier"], "", "o1", a1)
		released <- err
	}()
	h.waitForLock(1, waitingOn("tables"))
	// The sale writes its order only now, after the release began.
	if _, err := sale.Exec(ctx, `INSERT INTO orders (order_id, outlet_id, device_id, order_no, type, table_id, table_name,
		cashier_id, subtotal, service_bps, service, tax_bps, tax, total, client_created_at, request_hash)
		VALUES ('late', 'o1', $1, $2, 'dine_in', $3, 'A1', 'k1', 0, 0, 0, 0, 0, 0, now(), repeat('0', 64))`,
		tl.DeviceID, tl.prefix+"-20261001-0001", a1); err != nil {
		t.Fatal(err)
	}
	if err := sale.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if got := h.table(a1).Status; got != table.Available {
		t.Errorf("A1 = %s, want the sale the release waited for released too", got)
	}
}
