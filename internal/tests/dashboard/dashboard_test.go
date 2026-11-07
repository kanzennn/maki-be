package dashboard

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/models"
)

// seedMenus gives each outlet a device and menus.
func (h *harness) seedMenus() {
	h.t.Helper()
	h.exec(`INSERT INTO devices (device_id, outlet_id, code, name, platform, token_hash, activated_by) VALUES
		('d1', 'o1', 'K01', 'Depan', 'web', repeat('a', 64), 'm1'), ('d2', 'o2', 'K01', 'Depan', 'web', repeat('b', 64), 'm2')`)
	h.exec(`INSERT INTO menu_categories (category_id, outlet_id, name) VALUES ('c1', 'o1', 'Minuman'), ('c2', 'o2', 'Minuman')`)
	h.exec(`INSERT INTO menus (menu_id, outlet_id, category_id, name, price, track_stock, stock_qty, stock_alert) VALUES
		('kopi', 'o1', 'c1', 'Kopi', 5000, false, 0, NULL),
		('susu2', 'o1', 'c1', 'Susu Dua', 20000, true, 2, 3),
		('roti', 'o1', 'c1', 'Roti', 7000, true, 0, 3),
		('susu', 'o1', 'c1', 'Susu', 3000, true, 50, 3),
		('air', 'o2', 'c2', 'Air', 9000, true, 1, 2)`)
}

// sale writes a paid order, or a void one, at the instant: its rates are 0, so
// its total is its lines'.
func (h *harness) sale(id, outlet, cashier, menu, name string, price int64, qty int, at, status string) {
	h.t.Helper()
	device, code := "d1", "BDG01"
	if outlet == "o2" {
		device, code = "d2", "JYP01"
	}
	total := price * int64(qty)
	no := fmt.Sprintf("%s-K01-20261001-%04d", code, sequence(id))
	voidedAt, voidedBy, reason := any(nil), any(nil), any(nil)
	if status == "void" {
		voidedAt, voidedBy, reason = at, "m1", "Salah input"
	}
	h.exec(`INSERT INTO orders (order_id, outlet_id, device_id, order_no, type, cashier_id, subtotal, service_bps, service,
			tax_bps, tax, total, status, client_created_at, request_hash, voided_at, voided_by, void_reason)
		VALUES ($1, $2, $3, $4, 'takeaway', $5, $6, 0, 0, 0, 0, $6, $7, $8::timestamptz, repeat('0', 64), $9::timestamptz, $10, $11)`,
		id, outlet, device, no, cashier, total, status, at, voidedAt, voidedBy, reason)
	h.exec(`INSERT INTO order_items (order_item_id, outlet_id, order_id, position, menu_id, menu_name, price, qty, line_total)
		VALUES ($1, $2, $3, 0, $4, $5, $6, $7, $8)`, id+"i", outlet, id, menu, name, price, qty, total)
	paymentStatus := "paid"
	if status == "void" {
		paymentStatus = "void"
	}
	h.exec(`INSERT INTO payments (payment_id, outlet_id, order_id, method, amount, received, change, status)
		VALUES ($1, $2, $3, 'cash', $4, $4, 0, $5)`, id+"p", outlet, id, total, paymentStatus)
}

func sequence(id string) int {
	n := 0
	for _, c := range id {
		n = n*31 + int(c)
	}
	return n%9000 + 1
}

// seedSales writes the sales the tests add up, in UTC. Bandung is UTC+7 and
// Jayapura UTC+9, so an instant can be two different days.
func (h *harness) seedSales() {
	h.t.Helper()
	h.seedMenus()
	// Bandung, 1 October: 10.00 and 23.30.
	h.sale("s1", "o1", "k1", "kopi", "Kopi", 5000, 2, "2026-10-01T03:00:00Z", "paid")
	h.sale("s2", "o1", "k2", "susu2", "Susu Dua", 20000, 1, "2026-10-01T16:30:00Z", "paid")
	// Bandung, 00.30 on 2 October, though still the 1st in UTC.
	h.sale("s3", "o1", "k1", "roti", "Roti", 7000, 1, "2026-10-01T17:30:00Z", "paid")
	// Voided on the 1st: apart from the sales.
	h.sale("s4", "o1", "k1", "susu", "Susu", 3000, 1, "2026-10-01T05:00:00Z", "void")
	// Bandung, 30 September, the period before.
	h.sale("s5", "o1", "k1", "kopi", "Kopi", 4000, 1, "2026-09-30T05:00:00Z", "paid")
	// Jayapura, 01.00 on 1 October, though the 30th in UTC.
	h.sale("s7", "o2", "m2", "air", "Air", 9000, 1, "2026-09-30T16:00:00Z", "paid")
	// Jayapura, 01.30 on 2 October, though the 1st in UTC.
	h.sale("s6", "o2", "m2", "air", "Air", 6000, 1, "2026-10-01T16:30:00Z", "paid")
}

func (h *harness) owner(query string) models.DashboardResponse {
	h.t.Helper()
	var d models.DashboardResponse
	h.do(http.MethodGet, "/api/v1/owner/dashboard?"+query, "owner", nil).status(http.StatusOK, "").decode(&d)
	return d
}

func TestOwnerAddsTheOutletsUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedSales()
	d := h.owner("from=2026-10-01&to=2026-10-01")
	want := models.DashboardSummaryResponse{
		Sales: 39000, Transactions: 3, AverageTransaction: 13000, ItemsSold: 4, VoidCount: 1, VoidTotal: 3000,
		PreviousSales: 4000, PreviousTransactions: 1,
	}
	if d.Summary != want {
		t.Errorf("summary = %+v, want %+v", d.Summary, want)
	}
	if d.PreviousFrom != "2026-09-30" || d.PreviousTo != "2026-09-30" {
		t.Errorf("previous = %s to %s", d.PreviousFrom, d.PreviousTo)
	}
	if len(d.Outlets) != 2 || d.Outlets[0].OutletID != "o1" || d.Outlets[0].Sales != 30000 || d.Outlets[0].PreviousSales != 4000 ||
		d.Outlets[1].OutletID != "o2" || d.Outlets[1].Sales != 9000 || d.Outlets[1].Transactions != 1 {
		t.Errorf("ranking = %+v", d.Outlets)
	}
	if len(d.PaymentMethods) != 1 || d.PaymentMethods[0].Method != "cash" || d.PaymentMethods[0].Amount != 39000 || d.PaymentMethods[0].Transactions != 3 {
		t.Errorf("methods = %+v", d.PaymentMethods)
	}
	if len(d.TopMenus) != 3 || d.TopMenus[0].MenuID != "kopi" || d.TopMenus[0].Qty != 2 || d.TopMenus[1].MenuID != "susu2" {
		t.Errorf("top = %+v", d.TopMenus)
	}
	if len(d.Hours) != 0 || len(d.Cashiers) != 0 || len(d.Tables) != 0 {
		t.Errorf("an outlet's parts on the Owner's: %+v %+v %+v", d.Hours, d.Cashiers, d.Tables)
	}
	// The emptiest first, in every outlet; Susu has plenty, Kopi isn't tracked.
	if len(d.LowStock) != 3 || d.LowStock[0].MenuID != "roti" || d.LowStock[0].StockStatus != "out" ||
		d.LowStock[1].MenuID != "air" || d.LowStock[1].OutletName != "Jayapura" || d.LowStock[2].MenuID != "susu2" {
		t.Errorf("low stock = %+v", d.LowStock)
	}
}

func TestDaysAreEachOutletsOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedSales()
	d := h.owner("from=2026-09-30&to=2026-10-03")
	want := []models.DashboardDayResponse{
		{Date: "2026-09-30", Sales: 4000, Transactions: 1},
		{Date: "2026-10-01", Sales: 39000, Transactions: 3},
		{Date: "2026-10-02", Sales: 13000, Transactions: 2},
		{Date: "2026-10-03"},
	}
	if len(d.Days) != len(want) {
		t.Fatalf("days = %+v", d.Days)
	}
	for i := range want {
		if d.Days[i] != want[i] {
			t.Errorf("day %d = %+v, want %+v", i, d.Days[i], want[i])
		}
	}
	// Four days: the period before ends where this one starts.
	if d.PreviousFrom != "2026-09-26" || d.PreviousTo != "2026-09-29" {
		t.Errorf("previous = %s to %s", d.PreviousFrom, d.PreviousTo)
	}
}

func TestOwnerPicksOutlets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedSales()
	d := h.owner("from=2026-10-01&to=2026-10-01&outlet_ids=o2")
	if d.Summary.Sales != 9000 || len(d.Outlets) != 1 || d.Outlets[0].OutletID != "o2" {
		t.Errorf("o2 = %+v, %+v", d.Summary, d.Outlets)
	}
	if len(d.LowStock) != 1 || d.LowStock[0].MenuID != "air" {
		t.Errorf("low stock = %+v", d.LowStock)
	}
	if none := h.owner("from=2026-10-01&to=2026-10-01&outlet_ids=nope"); none.Summary.Sales != 0 || len(none.Outlets) != 0 {
		t.Errorf("no such outlet = %+v", none.Summary)
	}
}

func TestOutletDashboard(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedSales()
	var d models.DashboardResponse
	h.do(http.MethodGet, "/api/v1/outlets/o1/dashboard?from=2026-10-01&to=2026-10-01", "manager", nil).status(http.StatusOK, "").decode(&d)
	if d.Summary.Sales != 30000 || d.Summary.Transactions != 2 || d.Summary.ItemsSold != 3 || d.Summary.VoidCount != 1 {
		t.Errorf("summary = %+v", d.Summary)
	}
	if len(d.Outlets) != 0 {
		t.Errorf("outlets = %+v", d.Outlets)
	}
	if len(d.Hours) != 24 || d.Hours[10].Sales != 10000 || d.Hours[23].Sales != 20000 || d.Hours[11].Sales != 0 {
		t.Errorf("hours = %+v", d.Hours)
	}
	if len(d.Cashiers) != 2 || d.Cashiers[0].Name != "Sari" || d.Cashiers[0].Sales != 20000 || d.Cashiers[1].Name != "Budi" {
		t.Errorf("cashiers = %+v", d.Cashiers)
	}
	if len(d.LowStock) != 2 {
		t.Errorf("low stock = %+v", d.LowStock)
	}
}

func TestSeatedTables(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seedMenus()
	h.exec(`INSERT INTO table_areas (area_id, outlet_id, name) VALUES ('a1', 'o1', 'Indoor')`)
	h.exec(`INSERT INTO tables (table_id, outlet_id, area_id, name) VALUES ('t1', 'o1', 'a1', 'A1'), ('t2', 'o1', 'a1', 'A2')`)
	h.exec(`INSERT INTO orders (order_id, outlet_id, device_id, order_no, type, table_id, table_name, cashier_id, subtotal,
			service_bps, service, tax_bps, tax, total, client_created_at, request_hash)
		VALUES ('n1', 'o1', 'd1', 'BDG01-K01-20261001-0001', 'dine_in', 't1', 'A1', 'k1', 5000, 0, 0, 0, 0, 5000, now(), repeat('0', 64))`)
	today := time.Now().UTC().Format(time.DateOnly)
	var d models.DashboardResponse
	h.do(http.MethodGet, "/api/v1/outlets/o1/dashboard?from="+today+"&to="+today, "manager", nil).status(http.StatusOK, "").decode(&d)
	if len(d.Tables) != 1 || d.Tables[0].Name != "A1" || d.Tables[0].AreaName != "Indoor" || d.Tables[0].Orders != 1 {
		t.Errorf("tables = %+v", d.Tables)
	}
}

func TestAccessAndPeriod(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	const q = "?from=2026-10-01&to=2026-10-01"
	h.do(http.MethodGet, "/api/v1/owner/dashboard"+q, "manager", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o1/dashboard"+q, "cashier", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o1/dashboard"+q, "manager2", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, "/api/v1/outlets/o1/dashboard"+q, "owner", nil).status(http.StatusOK, "")
	for _, bad := range []string{"", "?from=2026-10-01", "?from=1-10-2026&to=2026-10-01", "?from=2026-10-02&to=2026-10-01", "?from=2025-01-01&to=2026-10-01"} {
		h.do(http.MethodGet, "/api/v1/owner/dashboard"+bad, "owner", nil).status(http.StatusUnprocessableEntity, "validation_failed")
	}
}
