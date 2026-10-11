package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
)

// These tests check the rules the schema itself enforces, so they hold even
// for a bug in the application or a hand-written query.

const hash = "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$a2V5"

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) error {
	t.Helper()
	_, err := pool.Exec(context.WithoutCancel(t.Context()), sql, args...)
	return err
}

// violates asserts that err is PostgreSQL rejecting the statement because of
// the named constraint.
func violates(t *testing.T, err error, constraint string) {
	t.Helper()
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		t.Errorf("error = %v, want %s to reject it", err, constraint)
		return
	}
	if pgErr.ConstraintName != constraint {
		t.Errorf("rejected by %q (%s), want %q", pgErr.ConstraintName, pgErr.Message, constraint)
	}
}

func insertUser(t *testing.T, pool *pgxpool.Pool, id, outletID, username, email string, owner bool) error {
	t.Helper()
	var outlet, mail any
	if outletID != "" {
		outlet = outletID
	}
	if email != "" {
		mail = email
	}
	return exec(t, pool, `INSERT INTO users (user_id, outlet_id, name, username, email, password_hash, is_owner)
		VALUES ($1, $2, 'Test', $3, $4, $5, $6)`, id, outlet, username, mail, hash, owner)
}

func insertOutlet(t *testing.T, pool *pgxpool.Pool, id, code string) error {
	t.Helper()
	return exec(t, pool, `INSERT INTO outlets (outlet_id, code, name, timezone, locale)
		VALUES ($1, $2, 'Outlet', 'Asia/Jakarta', 'id')`, id, code)
}

func TestUsersHaveExactlyOneOwner(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	if err := insertUser(t, pool, "u1", "", "owner", "owner@maki.test", true); err != nil {
		t.Fatalf("first owner: %v", err)
	}
	violates(t, insertUser(t, pool, "u2", "", "owner2", "owner2@maki.test", true), "users_single_owner_key")
}

func TestUsersRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	if err := insertOutlet(t, pool, "o1", "O1"); err != nil {
		t.Fatal(err)
	}
	if err := insertUser(t, pool, "staff", "o1", "kasir.satu", "", false); err != nil {
		t.Fatalf("a valid staff member: %v", err)
	}

	tests := []struct {
		name       string
		err        error
		constraint string
	}{
		{"owner without email", insertUser(t, pool, "a", "", "owner", "", true), "users_owner_email_check"},
		{"owner in an outlet", insertUser(t, pool, "b", "o1", "owner", "o@maki.test", true), "users_outlet_check"},
		{"staff without outlet", insertUser(t, pool, "c", "", "kasir2", "", false), "users_outlet_check"},
		{"same username", insertUser(t, pool, "d", "o1", "kasir.satu", "", false), "users_username_key"},
		{"uppercase username", insertUser(t, pool, "e", "o1", "Kasir3", "", false), "users_username_check"},
		{"too short", insertUser(t, pool, "f", "o1", "ab", "", false), "users_username_check"},
		{"spaces", insertUser(t, pool, "g", "o1", "kasir tiga", "", false), "users_username_check"},
		{"uppercase email", insertUser(t, pool, "h", "o1", "kasir4", "Kasir@maki.test", false), "users_email_check"},
		{"unknown status", exec(t, pool, `UPDATE users SET status = 'banned' WHERE user_id = 'staff'`), "users_status_check"},
		{"owner and manager", exec(t, pool, `INSERT INTO users (user_id, name, username, email, password_hash, is_owner, is_manager)
			VALUES ('i', 'X', 'bosmanager', 'b@maki.test', $1, true, true)`, hash), "users_owner_manager_check"},
		{"unknown outlet", insertUser(t, pool, "j", "nowhere", "kasir5", "", false), "users_outlet_id_fkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestOwnerCannotBeDeactivatedOrDeleted(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	if err := insertUser(t, pool, "u1", "", "owner", "owner@maki.test", true); err != nil {
		t.Fatal(err)
	}
	violates(t, exec(t, pool, `UPDATE users SET status = 'inactive' WHERE user_id = 'u1'`), "users_owner_active_check")
	violates(t, exec(t, pool, `UPDATE users SET deleted_at = now() WHERE user_id = 'u1'`), "users_owner_active_check")
}

func TestTokenRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	if err := insertOutlet(t, pool, "o1", "O1"); err != nil {
		t.Fatal(err)
	}
	if err := insertUser(t, pool, "u1", "o1", "kasir1", "", false); err != nil {
		t.Fatal(err)
	}

	violates(t, exec(t, pool, `INSERT INTO auth_tokens (auth_token_id, user_id, purpose, token_hash, expires_at)
		VALUES ('a1', 'u1', 'email_verify', 'h1', now())`), "auth_tokens_email_check")
	violates(t, exec(t, pool, `INSERT INTO auth_tokens (auth_token_id, user_id, purpose, email, token_hash, expires_at)
		VALUES ('a2', 'u1', 'password_reset', 'x@maki.test', 'h2', now())`), "auth_tokens_email_check")
	violates(t, exec(t, pool, `INSERT INTO auth_tokens (auth_token_id, user_id, purpose, token_hash, expires_at)
		VALUES ('a3', 'u1', 'magic_link', 'h3', now())`), "auth_tokens_purpose_check")

	if err := exec(t, pool, `INSERT INTO refresh_tokens (refresh_token_id, user_id, family_id, token_hash, expires_at)
		VALUES ('r1', 'u1', 'f1', 'h4', now())`); err != nil {
		t.Fatal(err)
	}
	violates(t, exec(t, pool, `INSERT INTO refresh_tokens (refresh_token_id, user_id, family_id, token_hash, expires_at)
		VALUES ('r2', 'u1', 'f1', 'h4', now())`), "refresh_tokens_token_hash_key")
	violates(t, exec(t, pool, `UPDATE refresh_tokens SET replaced_by = 'r1' WHERE refresh_token_id = 'r1'`), "refresh_tokens_replaced_check")
	violates(t, exec(t, pool, `INSERT INTO refresh_tokens (refresh_token_id, user_id, family_id, token_hash, expires_at)
		VALUES ('r3', 'nobody', 'f2', 'h5', now())`), "refresh_tokens_user_id_fkey")
}

func TestActionLogsAreAppendOnly(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	if err := exec(t, pool, `INSERT INTO action_logs (action_log_id, action, ip, occurred_at)
		VALUES ('l1', 'auth.login_failed', '203.0.113.7', now())`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	for name, sql := range map[string]string{
		"update":   `UPDATE action_logs SET action = 'auth.login' WHERE action_log_id = 'l1'`,
		"delete":   `DELETE FROM action_logs WHERE action_log_id = 'l1'`,
		"truncate": `TRUNCATE action_logs`,
	} {
		err := exec(t, pool, sql)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
			t.Errorf("%s: error = %v, want the append-only trigger to refuse it (42501)", name, err)
		}
	}

	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM action_logs`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d rows (%v), want the one row untouched", n, err)
	}
}

func TestOutletsRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	if err := insertOutlet(t, pool, "o1", "BDG01"); err != nil {
		t.Fatalf("a valid outlet: %v", err)
	}
	if err := exec(t, pool, `UPDATE outlets SET open_time = '22:00', close_time = '02:00', tax_bps = 1100, service_bps = 10000
		WHERE outlet_id = 'o1'`); err != nil {
		t.Errorf("hours past midnight and a 100%% service charge: %v", err)
	}
	if err := exec(t, pool, `UPDATE outlets SET status = 'inactive', deleted_at = now() WHERE outlet_id = 'o1'`); err != nil {
		t.Fatalf("deleting an inactive outlet: %v", err)
	}
	// The field rules are checked on an outlet that is still open.
	if err := insertOutlet(t, pool, "live", "SBY01"); err != nil {
		t.Fatal(err)
	}
	if err := exec(t, pool, `UPDATE outlets SET open_time = '08:00', close_time = '22:00' WHERE outlet_id = 'live'`); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		err        error
		constraint string
	}{
		{"code of a deleted outlet", insertOutlet(t, pool, "o2", "BDG01"), "outlets_code_key"},
		{"lowercase code", insertOutlet(t, pool, "o3", "bdg02"), "outlets_code_check"},
		{"one-letter code", insertOutlet(t, pool, "o4", "B"), "outlets_code_check"},
		{"code with a dash", insertOutlet(t, pool, "o5", "BDG-02"), "outlets_code_check"},
		{"long code", insertOutlet(t, pool, "o6", "BANDUNG0001"), "outlets_code_check"},
		{"empty name", exec(t, pool, `INSERT INTO outlets (outlet_id, code, name, timezone, locale)
			VALUES ('o7', 'JKT01', '', 'Asia/Jakarta', 'id')`), "outlets_name_check"},
		{"negative tax", exec(t, pool, `UPDATE outlets SET tax_bps = -1 WHERE outlet_id = 'live'`), "outlets_tax_check"},
		{"service over 100%", exec(t, pool, `UPDATE outlets SET service_bps = 10001 WHERE outlet_id = 'live'`), "outlets_service_check"},
		{"unknown status", exec(t, pool, `UPDATE outlets SET status = 'closed' WHERE outlet_id = 'live'`), "outlets_status_check"},
		{"opening without closing", exec(t, pool, `UPDATE outlets SET close_time = NULL WHERE outlet_id = 'live'`), "outlets_hours_check"},
		{"closing without opening", exec(t, pool, `UPDATE outlets SET open_time = NULL WHERE outlet_id = 'live'`), "outlets_hours_check"},
		{"same hours", exec(t, pool, `UPDATE outlets SET close_time = open_time WHERE outlet_id = 'live'`), "outlets_hours_check"},
		{"reactivated while deleted", exec(t, pool, `UPDATE outlets SET status = 'active' WHERE outlet_id = 'o1'`), "outlets_deleted_check"},
		{"log in an unknown outlet", exec(t, pool, `INSERT INTO action_logs (action_log_id, outlet_id, action, occurred_at)
			VALUES ('l1', 'nowhere', 'outlet.updated', now())`), "action_logs_outlet_id_fkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func insertRole(t *testing.T, pool *pgxpool.Pool, id, outletID, name string) error {
	t.Helper()
	return exec(t, pool, `INSERT INTO roles (role_id, outlet_id, name) VALUES ($1, $2, $3)`, id, outletID, name)
}

func TestRolesRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	for _, err := range []error{
		insertOutlet(t, pool, "o1", "O1"),
		insertOutlet(t, pool, "o2", "O2"),
		insertRole(t, pool, "r1", "o1", "Kasir"),
		insertRole(t, pool, "r2", "o2", "Kasir"),
		exec(t, pool, `INSERT INTO role_permissions (role_id, permission_code) VALUES ('r1', 'checkout.create')`),
	} {
		if err != nil {
			t.Fatalf("valid roles: %v", err)
		}
	}
	if err := exec(t, pool, `UPDATE roles SET deleted_at = now() WHERE role_id = 'r2'`); err != nil {
		t.Fatal(err)
	}
	if err := insertRole(t, pool, "r3", "o2", "kasir"); err != nil {
		t.Errorf("the name of a deleted role again: %v", err)
	}

	tests := []struct {
		name       string
		err        error
		constraint string
	}{
		{"same name, other case", insertRole(t, pool, "r4", "o1", "KASIR"), "roles_outlet_name_key"},
		{"empty name", insertRole(t, pool, "r5", "o1", ""), "roles_name_check"},
		{"unknown outlet", insertRole(t, pool, "r6", "nowhere", "Pelayan"), "roles_outlet_id_fkey"},
		{"permission twice", exec(t, pool, `INSERT INTO role_permissions (role_id, permission_code)
			VALUES ('r1', 'checkout.create')`), "role_permissions_pkey"},
		{"malformed permission", exec(t, pool, `INSERT INTO role_permissions (role_id, permission_code)
			VALUES ('r1', 'Checkout Create')`), "role_permissions_code_check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestUserRolesRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	for _, err := range []error{
		insertOutlet(t, pool, "o1", "O1"),
		insertOutlet(t, pool, "o2", "O2"),
		insertRole(t, pool, "r1", "o1", "Kasir"),
		insertRole(t, pool, "r2", "o1", "Pelayan"),
		insertRole(t, pool, "r3", "o2", "Kasir"),
		insertUser(t, pool, "u1", "o1", "kasir1", "", false),
		insertUser(t, pool, "owner", "", "owner", "owner@maki.test", true),
	} {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	assign := func(userID, roleID, outletID string) error {
		return exec(t, pool, `INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ($1, $2, $3)`, userID, roleID, outletID)
	}
	if err := assign("u1", "r1", "o1"); err != nil {
		t.Fatalf("a role of the user's outlet: %v", err)
	}

	tests := []struct {
		name       string
		err        error
		constraint string
	}{
		{"a second role", assign("u1", "r2", "o1"), "user_roles_user_key"},
		{"role of another outlet", exec(t, pool, `UPDATE user_roles SET role_id = 'r3', outlet_id = 'o2' WHERE user_id = 'u1'`), "user_roles_user_fkey"},
		{"outlet the role isn't in", exec(t, pool, `UPDATE user_roles SET role_id = 'r3' WHERE user_id = 'u1'`), "user_roles_role_fkey"},
		{"the Owner", assign("owner", "r1", "o1"), "user_roles_user_fkey"},
		{"moving the user away from their role", exec(t, pool, `UPDATE users SET outlet_id = 'o2' WHERE user_id = 'u1'`), "user_roles_user_fkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func insertCategory(t *testing.T, pool *pgxpool.Pool, id, outletID, name string) error {
	t.Helper()
	return exec(t, pool, `INSERT INTO menu_categories (category_id, outlet_id, name) VALUES ($1, $2, $3)`, id, outletID, name)
}

func insertMenu(t *testing.T, pool *pgxpool.Pool, id, outletID, categoryID, sku string, price int64) error {
	t.Helper()
	var s any
	if sku != "" {
		s = sku
	}
	return exec(t, pool, `INSERT INTO menus (menu_id, outlet_id, category_id, name, sku, price)
		VALUES ($1, $2, $3, 'Es Kopi', $4, $5)`, id, outletID, categoryID, s, price)
}

func TestMenuCategoriesRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	for _, err := range []error{
		insertOutlet(t, pool, "o1", "O1"),
		insertCategory(t, pool, "c1", "o1", "Minuman"),
		insertCategory(t, pool, "c2", "o1", "Makanan"),
		exec(t, pool, `UPDATE menu_categories SET icon = '🧑‍🍳', sort_order = 1 WHERE category_id = 'c1'`),
		exec(t, pool, `UPDATE menu_categories SET deleted_at = now() WHERE category_id = 'c2'`),
	} {
		if err != nil {
			t.Fatalf("valid categories: %v", err)
		}
	}
	if err := insertCategory(t, pool, "c3", "o1", "makanan"); err != nil {
		t.Errorf("the name of a deleted category again: %v", err)
	}

	tests := []struct {
		name       string
		err        error
		constraint string
	}{
		{"same name, other case", insertCategory(t, pool, "c4", "o1", "MINUMAN"), "menu_categories_outlet_name_key"},
		{"empty name", insertCategory(t, pool, "c5", "o1", ""), "menu_categories_name_check"},
		{"unknown outlet", insertCategory(t, pool, "c6", "nowhere", "Snack"), "menu_categories_outlet_id_fkey"},
		{"icon too long", exec(t, pool, `UPDATE menu_categories SET icon = 'kopi susu' WHERE category_id = 'c1'`), "menu_categories_icon_check"},
		{"empty icon", exec(t, pool, `UPDATE menu_categories SET icon = '' WHERE category_id = 'c1'`), "menu_categories_icon_check"},
		{"negative order", exec(t, pool, `UPDATE menu_categories SET sort_order = -1 WHERE category_id = 'c1'`), "menu_categories_sort_order_check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestMenusRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	for _, err := range []error{
		insertOutlet(t, pool, "o1", "O1"),
		insertOutlet(t, pool, "o2", "O2"),
		insertCategory(t, pool, "c1", "o1", "Minuman"),
		insertCategory(t, pool, "c2", "o2", "Minuman"),
		insertMenu(t, pool, "m1", "o1", "c1", "KOPI-01", 18000),
		insertMenu(t, pool, "m2", "o1", "c1", "", 0),
		insertMenu(t, pool, "m3", "o2", "c2", "KOPI-01", 100000000),
		exec(t, pool, `UPDATE menus SET track_stock = true, stock_qty = 5, stock_alert = 0 WHERE menu_id = 'm1'`),
		insertMenu(t, pool, "m4", "o1", "c1", "JERUK-01", 9000),
		exec(t, pool, `UPDATE menus SET deleted_at = now() WHERE menu_id = 'm4'`),
	} {
		if err != nil {
			t.Fatalf("valid menus (a free one, the top price, a SKU in two outlets): %v", err)
		}
	}
	if err := insertMenu(t, pool, "m5", "o1", "c1", "jeruk-01", 9000); err != nil {
		t.Errorf("the SKU of a deleted menu again: %v", err)
	}

	tests := []struct {
		name       string
		err        error
		constraint string
	}{
		{"SKU twice, other case", insertMenu(t, pool, "m6", "o1", "c1", "kopi-01", 18000), "menus_outlet_sku_key"},
		{"SKU with a space", insertMenu(t, pool, "m7", "o1", "c1", "KOPI 01", 18000), "menus_sku_check"},
		{"category of another outlet", insertMenu(t, pool, "m8", "o1", "c2", "", 18000), "menus_category_fkey"},
		{"negative price", insertMenu(t, pool, "m9", "o1", "c1", "", -1), "menus_price_check"},
		{"price over 100 million", insertMenu(t, pool, "m10", "o1", "c1", "", 100000001), "menus_price_check"},
		{"empty name", exec(t, pool, `UPDATE menus SET name = '' WHERE menu_id = 'm1'`), "menus_name_check"},
		{"negative alert", exec(t, pool, `UPDATE menus SET stock_alert = -1 WHERE menu_id = 'm1'`), "menus_stock_alert_check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestStockMovementsRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	for _, err := range []error{
		insertOutlet(t, pool, "o1", "O1"),
		insertOutlet(t, pool, "o2", "O2"),
		insertUser(t, pool, "u1", "o1", "kasir1", "", false),
		insertCategory(t, pool, "c1", "o1", "Minuman"),
		insertMenu(t, pool, "m1", "o1", "c1", "", 18000),
		insertDevice(t, pool, "d1", "o1", "K01", nil),
		insertOrder(t, pool, "ord1", nil),
	} {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	// A sale or void names its order (TestStockSalesNameTheirOrder).
	move := func(id, outletID, typ string, change, after int, reason any) error {
		var order any
		if typ == "sale" || typ == "void" {
			order = "ord1"
		}
		return exec(t, pool, `INSERT INTO stock_movements (stock_movement_id, outlet_id, menu_id, type, qty_change, stock_after, reason, order_id, user_id)
			VALUES ($1, $2, 'm1', $3, $4, $5, $6, $7, 'u1')`, id, outletID, typ, change, after, reason, order)
	}
	for _, err := range []error{
		move("s1", "o1", "initial", 10, 10, "initial"),
		move("s2", "o1", "out", -3, 7, "damaged"),
		move("s3", "o1", "set", 5, 12, "correction"),
		move("s4", "o1", "sale", -13, -1, nil),
		move("s5", "o1", "void", 1, 0, nil),
	} {
		if err != nil {
			t.Fatalf("valid movements, an offline sale below zero included: %v", err)
		}
	}

	tests := []struct {
		name       string
		err        error
		constraint string
	}{
		{"an adjustment without a reason", move("s6", "o1", "in", 5, 5, nil), "stock_movements_reason_check"},
		{"an unknown reason", move("s7", "o1", "in", 5, 5, "found"), "stock_movements_reason_check"},
		{"a start that isn't one", move("s8", "o1", "initial", 5, 5, "restock"), "stock_movements_reason_check"},
		{"a start without its reason", move("s16", "o1", "initial", 5, 5, nil), "stock_movements_reason_check"},
		{"a sale with a reason", move("s9", "o1", "sale", -1, 4, "other"), "stock_movements_reason_check"},
		{"stock in that goes down", move("s10", "o1", "in", -5, 0, "restock"), "stock_movements_direction_check"},
		{"stock out that goes up", move("s11", "o1", "out", 2, 7, "damaged"), "stock_movements_direction_check"},
		{"an adjustment below zero", move("s12", "o1", "out", -6, -1, "damaged"), "stock_movements_stock_after_check"},
		{"an unknown type", move("s13", "o1", "gift", 1, 1, "other"), "stock_movements_type_check"},
		{"a menu of another outlet", move("s14", "o2", "in", 1, 1, "restock"), "stock_movements_menu_fkey"},
		{"a long note", exec(t, pool, `INSERT INTO stock_movements (stock_movement_id, outlet_id, menu_id, type, qty_change, stock_after, reason, note, user_id)
			VALUES ('s15', 'o1', 'm1', 'in', 1, 1, 'other', repeat('x', 201), 'u1')`), "stock_movements_note_check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}

	for name, sql := range map[string]string{
		"update":   `UPDATE stock_movements SET qty_change = 100 WHERE stock_movement_id = 's1'`,
		"delete":   `DELETE FROM stock_movements WHERE stock_movement_id = 's1'`,
		"truncate": `TRUNCATE stock_movements`,
	} {
		err := exec(t, pool, sql)
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "42501" {
			t.Errorf("%s: error = %v, want the append-only trigger to refuse it (42501)", name, err)
		}
	}

	// A row is stamped when it is written, not when its transaction began:
	// an adjustment that waited for the menu's lock sorts after the one it
	// waited for.
	ctx := context.WithoutCancel(t.Context())
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var later bool
	if _, err := tx.Exec(ctx, `SELECT pg_sleep(0.01)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO stock_movements (stock_movement_id, outlet_id, menu_id, type, qty_change, stock_after, reason, user_id)
		VALUES ('s17', 'o1', 'm1', 'set', 0, 0, 'correction', 'u1') RETURNING created_at > now()`).Scan(&later); err != nil {
		t.Fatal(err)
	}
	if !later {
		t.Error("created_at is when the transaction began, want when the row was written")
	}
}
