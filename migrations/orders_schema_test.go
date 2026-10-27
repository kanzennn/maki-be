package migrations_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/kervzent/maki-be/internal/tests/dbtest"
	"github.com/kervzent/maki-be/migrations"
)

// insertRow inserts the row into table: defaults, with the fields in with
// replacing them. A nil value is NULL.
func insertRow(t *testing.T, pool *pgxpool.Pool, table string, defaults, with map[string]any) error {
	t.Helper()
	row := maps.Clone(defaults)
	maps.Copy(row, with)
	columns := slices.Sorted(maps.Keys(row))
	marks := make([]string, len(columns))
	args := make([]any, len(columns))
	for i, c := range columns {
		marks[i] = fmt.Sprintf("$%d", i+1)
		args[i] = row[c]
	}
	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(columns, ", "), strings.Join(marks, ", "))
	return exec(t, pool, sql, args...)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// insertDevice inserts a web device of the outlet, activated by u1.
func insertDevice(t *testing.T, pool *pgxpool.Pool, id, outletID, code string, with map[string]any) error {
	t.Helper()
	return insertRow(t, pool, "devices", map[string]any{
		"device_id": id, "outlet_id": outletID, "code": code, "name": "Kasir depan", "platform": "web",
		"token_hash": sha256Hex(id), "activated_by": "u1",
	}, with)
}

var orderCount atomic.Int32

// insertOrder inserts a paid takeaway order of o1, made on d1 by u1, with a
// number of its own: 100.000 with 5% service and 10% tax, 115.500 in all.
func insertOrder(t *testing.T, pool *pgxpool.Pool, id string, with map[string]any) error {
	t.Helper()
	return insertRow(t, pool, "orders", map[string]any{
		"order_id": id, "outlet_id": "o1", "device_id": "d1",
		"order_no": fmt.Sprintf("O1-K01-20261001-%04d", orderCount.Add(1)),
		"type":     "takeaway", "cashier_id": "u1",
		"subtotal": 100000, "service_bps": 500, "service": 5000, "tax_bps": 1000, "tax": 10500, "total": 115500,
		"client_created_at": time.Now(), "request_hash": sha256Hex(id),
	}, with)
}

// cashier sets up two outlets (O1 and O2), a user u1 in o1, and a device of
// each outlet, both K01: d1 in o1 and d2 in o2.
func cashier(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, err := range []error{
		insertOutlet(t, pool, "o1", "O1"),
		insertOutlet(t, pool, "o2", "O2"),
		insertUser(t, pool, "u1", "o1", "kasir1", "", false),
		insertDevice(t, pool, "d1", "o1", "K01", nil),
		insertDevice(t, pool, "d2", "o2", "K01", nil),
	} {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
}

func TestDevicesRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	if err := insertDevice(t, pool, "d3", "o1", "K02", map[string]any{"platform": "android", "revoked_at": time.Now(), "revoked_by": "u1"}); err != nil {
		t.Fatalf("a revoked Android device: %v", err)
	}

	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"a code that isn't K and digits", insertDevice(t, pool, "x1", "o1", "A01", nil), "devices_code_check"},
		{"a code with one digit", insertDevice(t, pool, "x2", "o1", "K1", nil), "devices_code_check"},
		{"a code twice in an outlet", insertDevice(t, pool, "x3", "o1", "K01", nil), "devices_outlet_code_key"},
		{"no name", insertDevice(t, pool, "x4", "o1", "K05", map[string]any{"name": ""}), "devices_name_check"},
		{"an unknown platform", insertDevice(t, pool, "x5", "o1", "K06", map[string]any{"platform": "ios"}), "devices_platform_check"},
		{"a token that isn't hashed", insertDevice(t, pool, "x6", "o1", "K07", map[string]any{"token_hash": "secret"}), "devices_token_hash_check"},
		{"another device's token", insertDevice(t, pool, "x7", "o1", "K08", map[string]any{"token_hash": sha256Hex("d1")}), "devices_token_hash_key"},
		{"revoked by nobody", insertDevice(t, pool, "x8", "o1", "K09", map[string]any{"revoked_at": time.Now()}), "devices_revoked_check"},
		{"a revoker without a revoke", insertDevice(t, pool, "x9", "o1", "K10", map[string]any{"revoked_by": "u1"}), "devices_revoked_check"},
		{"an outlet that doesn't exist", insertDevice(t, pool, "x10", "o9", "K01", nil), "devices_outlet_id_fkey"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestPaymentMethodsRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	if err := insertOutlet(t, pool, "o1", "O1"); err != nil {
		t.Fatal(err)
	}
	method := func(m string) error {
		return exec(t, pool, `INSERT INTO outlet_payment_methods (outlet_id, method, is_enabled) VALUES ('o1', $1, true)`, m)
	}
	if err := method("cash"); err != nil {
		t.Fatalf("cash: %v", err)
	}
	violates(t, method("gopay"), "outlet_payment_methods_method_check")
	violates(t, method("cash"), "outlet_payment_methods_pkey")
}

// The outlets there already when payment methods arrive start with cash on,
// as a new outlet does.
func TestExistingOutletsStartWithCash(t *testing.T) {
	t.Parallel()
	pool := dbtest.Empty(t)
	ctx := t.Context()
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	// The last migration before payment methods, then an outlet, then the rest.
	if _, err := provider.UpTo(ctx, 20260930203304); err != nil {
		t.Fatal(err)
	}
	if err := insertOutlet(t, pool, "o1", "O1"); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `SELECT method || ' ' || is_enabled FROM outlet_payment_methods WHERE outlet_id = 'o1' ORDER BY method`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if want := []string{"cash true", "debit false", "qris false"}; !slices.Equal(got, want) {
		t.Errorf("methods = %v, want %v", got, want)
	}
}

func TestOrdersRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	for _, err := range []error{
		insertVoucher(t, pool, "v1", "MERDEKA17", nil),
		insertOrder(t, pool, "ok1", map[string]any{"order_no": "O1-K01-20261001-0999"}),
		// The same number in another outlet is another order's.
		insertOrder(t, pool, "ok2", map[string]any{"outlet_id": "o2", "device_id": "d2", "order_no": "O1-K01-20261001-0999"}),
		// Half a rupiah rounds up: 10% of 10.005 is 1.000,5, so 1.001.
		insertOrder(t, pool, "ok3", map[string]any{"subtotal": 10005, "service_bps": 1000, "service": 1001, "tax_bps": 0, "tax": 0, "total": 11006}),
		// A voucher's discount comes off before the service and tax.
		insertOrder(t, pool, "ok4", map[string]any{
			"voucher_id": "v1", "voucher_code": "MERDEKA17",
			"voucher_discount": 20000, "service": 4000, "tax": 8400, "total": 92400,
		}),
		insertOrder(t, pool, "ok5", map[string]any{"status": "void", "voided_at": time.Now(), "voided_by": "u1", "void_reason": "Salah input"}),
	} {
		if err != nil {
			t.Fatalf("valid orders: %v", err)
		}
	}

	noTax := map[string]any{"service_bps": 0, "service": 0, "tax_bps": 0, "tax": 0}
	with := func(base map[string]any, extra map[string]any) map[string]any {
		out := maps.Clone(base)
		maps.Copy(out, extra)
		return out
	}
	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"a number without the device's code", insertOrder(t, pool, "b1", map[string]any{"order_no": "O1-20261001-0001"}), "orders_order_no_check"},
		{"a number twice in an outlet", insertOrder(t, pool, "b2", map[string]any{"order_no": "O1-K01-20261001-0999"}), "orders_outlet_order_no_key"},
		{"an unknown type", insertOrder(t, pool, "b3", map[string]any{"type": "delivery"}), "orders_type_check"},
		{"a device of another outlet", insertOrder(t, pool, "b4", map[string]any{"device_id": "d2"}), "orders_device_fkey"},
		{"a subtotal past a billion", insertOrder(t, pool, "b5", with(noTax, map[string]any{"subtotal": 1000000001, "total": 1000000001})), "orders_subtotal_check"},
		{"a discount past the subtotal", insertOrder(t, pool, "b6", with(noTax, map[string]any{"voucher_id": "v1", "voucher_code": "MERDEKA17", "voucher_discount": 100001, "total": -1})), "orders_voucher_discount_check"},
		{"a service rounded down", insertOrder(t, pool, "b7", map[string]any{"subtotal": 10005, "service_bps": 1000, "service": 1000, "tax_bps": 0, "tax": 0, "total": 11005}), "orders_service_check"},
		{"a tax without the service in it", insertOrder(t, pool, "b8", map[string]any{"tax": 10000, "total": 115000}), "orders_tax_check"},
		{"a total that doesn't add up", insertOrder(t, pool, "b9", map[string]any{"total": 115000}), "orders_total_check"},
		{"a rate past 100%", insertOrder(t, pool, "b10", map[string]any{"tax_bps": 10001}), "orders_tax_bps_check"},
		{"an unknown status", insertOrder(t, pool, "b11", map[string]any{"status": "pending"}), "orders_status_check"},
		{"a void without its reason", insertOrder(t, pool, "b12", map[string]any{"status": "void", "voided_at": time.Now(), "voided_by": "u1"}), "orders_void_check"},
		{"a reason without a void", insertOrder(t, pool, "b13", map[string]any{"void_reason": "Salah input"}), "orders_void_check"},
		{"a void without who did it", insertOrder(t, pool, "b14", map[string]any{"status": "void", "voided_at": time.Now(), "void_reason": "Salah input"}), "orders_void_check"},
		{"a request that isn't hashed", insertOrder(t, pool, "b15", map[string]any{"request_hash": "abc"}), "orders_request_hash_check"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestOrderItemsAndPaymentsRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	for _, err := range []error{
		insertCategory(t, pool, "c1", "o1", "Minuman"),
		insertCategory(t, pool, "c2", "o2", "Minuman"),
		insertMenu(t, pool, "m1", "o1", "c1", "", 18000),
		insertMenu(t, pool, "m2", "o2", "c2", "", 18000),
		insertOrder(t, pool, "ord1", nil),
		insertOrder(t, pool, "ord2", map[string]any{"outlet_id": "o2", "device_id": "d2"}),
	} {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	item := func(id string, with map[string]any) error {
		return insertRow(t, pool, "order_items", map[string]any{
			"order_item_id": id, "outlet_id": "o1", "order_id": "ord1", "position": 0, "menu_id": "m1",
			"menu_name": "Es Kopi Susu", "price": 18000, "qty": 2, "line_total": 36000,
		}, with)
	}
	payment := func(id string, with map[string]any) error {
		return insertRow(t, pool, "payments", map[string]any{
			"payment_id": id, "outlet_id": "o1", "order_id": "ord1", "method": "cash", "amount": 115500,
			"received": 150000, "change": 34500, "status": "paid",
		}, with)
	}
	if err := item("i1", map[string]any{"note": "less sugar"}); err != nil {
		t.Fatalf("an item: %v", err)
	}
	if err := item("i2", map[string]any{"position": 1, "qty": 999, "line_total": 17982000}); err != nil {
		t.Fatalf("999 of an item: %v", err)
	}
	if err := payment("p1", nil); err != nil {
		t.Fatalf("a cash payment: %v", err)
	}

	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"a line total that isn't price × qty", item("b1", map[string]any{"position": 2, "line_total": 18000}), "order_items_line_total_check"},
		{"none of an item", item("b2", map[string]any{"position": 2, "qty": 0, "line_total": 0}), "order_items_qty_check"},
		{"a thousand of an item", item("b3", map[string]any{"position": 2, "qty": 1000, "line_total": 18000000}), "order_items_qty_check"},
		{"a place taken", item("b4", nil), "order_items_order_position_key"},
		{"a menu of another outlet", item("b5", map[string]any{"position": 2, "menu_id": "m2"}), "order_items_menu_fkey"},
		{"an order of another outlet", item("b6", map[string]any{"position": 2, "order_id": "ord2"}), "order_items_order_fkey"},
		{"no name", item("b7", map[string]any{"position": 2, "menu_name": ""}), "order_items_menu_name_check"},
		{"an empty note", item("b8", map[string]any{"position": 2, "note": ""}), "order_items_note_check"},
		{"a second payment", payment("b9", nil), "payments_order_key"},
		{"cash short of the amount", payment("b10", map[string]any{"received": 100000, "change": -15500}), "payments_cash_check"},
		{"change that isn't the rest", payment("b11", map[string]any{"change": 30000}), "payments_cash_check"},
		{"cash without what was handed over", payment("b12", map[string]any{"received": nil}), "payments_cash_check"},
		{"change for a card", payment("b13", map[string]any{"method": "debit"}), "payments_cash_check"},
		{"an unknown method", payment("b14", map[string]any{"method": "gopay", "received": nil, "change": nil}), "payments_method_check"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestStockSalesNameTheirOrder(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	for _, err := range []error{
		insertCategory(t, pool, "c1", "o1", "Minuman"),
		insertMenu(t, pool, "m1", "o1", "c1", "", 18000),
		insertOrder(t, pool, "ord1", nil),
		insertOrder(t, pool, "ord2", map[string]any{"outlet_id": "o2", "device_id": "d2"}),
	} {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	move := func(id, typ string, change int, reason, order any) error {
		return exec(t, pool, `INSERT INTO stock_movements (stock_movement_id, outlet_id, menu_id, type, qty_change, stock_after, reason, order_id, user_id)
			VALUES ($1, 'o1', 'm1', $2, $3, 10, $4, $5, 'u1')`, id, typ, change, reason, order)
	}
	if err := move("s1", "sale", -2, nil, "ord1"); err != nil {
		t.Fatalf("a sale of its order: %v", err)
	}
	if err := move("s2", "void", 2, nil, "ord1"); err != nil {
		t.Fatalf("a void of its order: %v", err)
	}
	violates(t, move("s3", "sale", -2, nil, nil), "stock_movements_order_check")
	violates(t, move("s4", "in", 2, "restock", "ord1"), "stock_movements_order_check")
	violates(t, move("s5", "sale", -2, nil, "ord2"), "stock_movements_order_fkey")
}

// Transactions are kept as they were made. An order and its payment change
// only from paid to void, and nothing else about them changes with it.
func TestTransactionsAreKept(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	for _, err := range []error{
		insertCategory(t, pool, "c1", "o1", "Minuman"),
		insertMenu(t, pool, "m1", "o1", "c1", "", 18000),
		insertOrder(t, pool, "ord1", nil),
		exec(t, pool, `INSERT INTO order_items (order_item_id, outlet_id, order_id, position, menu_id, menu_name, price, qty, line_total)
			VALUES ('i1', 'o1', 'ord1', 0, 'm1', 'Es Kopi Susu', 18000, 1, 18000)`),
		exec(t, pool, `INSERT INTO payments (payment_id, outlet_id, order_id, method, amount, received, change, status)
			VALUES ('p1', 'o1', 'ord1', 'cash', 115500, 115500, 0, 'paid')`),
	} {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	refused := func(what string, err error) {
		t.Helper()
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
			t.Errorf("%s: error = %v, want the trigger to refuse it (42501)", what, err)
		}
	}

	refused("a changed total", exec(t, pool, `UPDATE orders SET total = 1 WHERE order_id = 'ord1'`))
	refused("a void that changes the total too", exec(t, pool, `UPDATE orders SET status = 'void', voided_at = now(), voided_by = 'u1', void_reason = 'x', total = 1 WHERE order_id = 'ord1'`))
	refused("a changed item", exec(t, pool, `UPDATE order_items SET qty = 2 WHERE order_item_id = 'i1'`))
	refused("a changed payment", exec(t, pool, `UPDATE payments SET received = 200000, change = 84500 WHERE payment_id = 'p1'`))
	for _, table := range []string{"payments", "order_items", "orders"} {
		refused("DELETE on "+table, exec(t, pool, "DELETE FROM "+table))
		refused("TRUNCATE on "+table, exec(t, pool, "TRUNCATE "+table+" CASCADE"))
	}

	if err := exec(t, pool, `UPDATE orders SET status = 'void', voided_at = now(), voided_by = 'u1', void_reason = 'Salah input' WHERE order_id = 'ord1'`); err != nil {
		t.Fatalf("a void: %v", err)
	}
	if err := exec(t, pool, `UPDATE payments SET status = 'void' WHERE payment_id = 'p1'`); err != nil {
		t.Fatalf("its payment's void: %v", err)
	}
	refused("a void undone", exec(t, pool, `UPDATE orders SET status = 'paid', voided_at = NULL, voided_by = NULL, void_reason = NULL WHERE order_id = 'ord1'`))
	refused("a void's reason changed", exec(t, pool, `UPDATE orders SET void_reason = 'Lain' WHERE order_id = 'ord1'`))
}
