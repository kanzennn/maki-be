package migrations_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/kervzent/maki-be/internal/tests/dbtest"
	"github.com/kervzent/maki-be/migrations"
)

// insertVoucher inserts an active voucher of 10% for every outlet, none of
// its 100 uses taken, until a year from now.
func insertVoucher(t *testing.T, pool *pgxpool.Pool, id, code string, with map[string]any) error {
	t.Helper()
	return insertRow(t, pool, "vouchers", map[string]any{
		"voucher_id": id, "code": code, "type": "percent", "value": 1000, "quota": 100,
		"expired_at": time.Now().Add(365 * 24 * time.Hour), "all_outlets": true,
	}, with)
}

func TestVouchersRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	now := time.Now()
	for _, err := range []error{
		insertVoucher(t, pool, "ok1", "MERDEKA17", nil),
		insertVoucher(t, pool, "ok2", "HEMAT10K", map[string]any{
			"name": "Hemat sepuluh ribu", "type": "amount", "value": 10000, "min_spend": 50000,
		}),
		insertVoucher(t, pool, "ok3", "DISKON50", map[string]any{
			"value": 5000, "max_discount": 25000, "start_at": now, "expired_at": now.Add(time.Hour),
		}),
		// Every use taken.
		insertVoucher(t, pool, "ok4", "HABIS", map[string]any{"quota": 1, "used_count": 1, "first_used_at": now}),
		// A deleted voucher was never used: its code is free again.
		insertVoucher(t, pool, "ok5", "ULANG", map[string]any{"deleted_at": now}),
		insertVoucher(t, pool, "ok6", "ULANG", nil),
		// Used once, then voided: it stays used.
		insertVoucher(t, pool, "ok7", "PERNAH", map[string]any{"first_used_at": now}),
	} {
		if err != nil {
			t.Fatalf("valid vouchers: %v", err)
		}
	}

	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"a code in lower case", insertVoucher(t, pool, "b1", "merdeka", nil), "vouchers_code_check"},
		{"a code too short", insertVoucher(t, pool, "b2", "AB", nil), "vouchers_code_check"},
		{"a code with a dash", insertVoucher(t, pool, "b3", "HUT-RI", nil), "vouchers_code_check"},
		{"an empty name", insertVoucher(t, pool, "b4", "BAD4", map[string]any{"name": ""}), "vouchers_name_check"},
		{"an unknown type", insertVoucher(t, pool, "b5", "BAD5", map[string]any{"type": "gift"}), "vouchers_type_check"},
		{"no value", insertVoucher(t, pool, "b6", "BAD6", map[string]any{"value": 0}), "vouchers_value_check"},
		{"more than 100%", insertVoucher(t, pool, "b7", "BAD7", map[string]any{"value": 10001}), "vouchers_value_check"},
		{"a cap on an amount", insertVoucher(t, pool, "b8", "BAD8", map[string]any{"type": "amount", "value": 5000, "max_discount": 5000}), "vouchers_max_discount_check"},
		{"a cap of nothing", insertVoucher(t, pool, "b9", "BAD9", map[string]any{"max_discount": 0}), "vouchers_max_discount_check"},
		{"a minimum of nothing", insertVoucher(t, pool, "b10", "BAD10", map[string]any{"min_spend": 0}), "vouchers_min_spend_check"},
		{"no uses", insertVoucher(t, pool, "b11", "BAD11", map[string]any{"quota": 0}), "vouchers_quota_check"},
		{"more uses than the quota", insertVoucher(t, pool, "b12", "BAD12", map[string]any{"quota": 1, "used_count": 2, "first_used_at": now}), "vouchers_used_count_check"},
		{"uses below none", insertVoucher(t, pool, "b13", "BAD13", map[string]any{"used_count": -1, "first_used_at": now}), "vouchers_used_count_check"},
		{"an end before the start", insertVoucher(t, pool, "b14", "BAD14", map[string]any{"start_at": now, "expired_at": now}), "vouchers_period_check"},
		{"used without its first use", insertVoucher(t, pool, "b15", "BAD15", map[string]any{"used_count": 1}), "vouchers_first_used_check"},
		{"deleted once used", insertVoucher(t, pool, "b16", "BAD16", map[string]any{"first_used_at": now, "deleted_at": now}), "vouchers_deleted_check"},
		{"deleted with uses taken", insertVoucher(t, pool, "b17", "BAD17", map[string]any{"deleted_at": now, "used_count": 1}), "vouchers_first_used_check"},
		{"a code twice", insertVoucher(t, pool, "b18", "MERDEKA17", nil), "vouchers_code_key"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestVoucherOutletsRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	for _, err := range []error{
		insertOutlet(t, pool, "o1", "O1"),
		insertVoucher(t, pool, "v1", "BDG", map[string]any{"all_outlets": false}),
		exec(t, pool, `INSERT INTO voucher_outlets (voucher_id, outlet_id) VALUES ('v1', 'o1')`),
	} {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	violates(t, exec(t, pool, `INSERT INTO voucher_outlets (voucher_id, outlet_id) VALUES ('v1', 'o1')`), "voucher_outlets_pkey")
	violates(t, exec(t, pool, `INSERT INTO voucher_outlets (voucher_id, outlet_id) VALUES ('v1', 'nope')`), "voucher_outlets_outlet_id_fkey")
	violates(t, exec(t, pool, `INSERT INTO voucher_outlets (voucher_id, outlet_id) VALUES ('nope', 'o1')`), "voucher_outlets_voucher_id_fkey")
}

func TestOrdersNameTheirVoucher(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	if err := insertVoucher(t, pool, "v1", "MERDEKA17", nil); err != nil {
		t.Fatal(err)
	}
	// 100.000 less 20.000, 5% service and 10% tax: 92.400.
	discounted := map[string]any{"voucher_discount": 20000, "service": 4000, "tax": 8400, "total": 92400}
	withVoucher := func(extra map[string]any) map[string]any {
		row := map[string]any{"voucher_id": "v1", "voucher_code": "MERDEKA17"}
		for k, v := range extra {
			row[k] = v
		}
		return row
	}
	for _, err := range []error{
		insertOrder(t, pool, "ok1", withVoucher(discounted)),
		// A percentage of a small subtotal can round to nothing.
		insertOrder(t, pool, "ok2", withVoucher(nil)),
	} {
		if err != nil {
			t.Fatalf("valid orders: %v", err)
		}
	}
	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"a discount without a voucher", insertOrder(t, pool, "b1", discounted), "orders_voucher_check"},
		{"a voucher without its code", insertOrder(t, pool, "b2", map[string]any{"voucher_id": "v1"}), "orders_voucher_check"},
		{"a code without its voucher", insertOrder(t, pool, "b3", map[string]any{"voucher_code": "MERDEKA17"}), "orders_voucher_check"},
		{"a voucher that doesn't exist", insertOrder(t, pool, "b4", map[string]any{"voucher_id": "nope", "voucher_code": "NOPE"}), "orders_voucher_fkey"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

// Every migration goes down and up again, on a schema of its own.
func TestMigrationsGoDownAndUp(t *testing.T) {
	t.Parallel()
	pool := dbtest.Empty(t)
	ctx := t.Context()
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
