package order

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/payment"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/platform/middleware"
	"github.com/kervzent/maki-be/internal/table"
	"github.com/kervzent/maki-be/internal/user"
	"github.com/kervzent/maki-be/internal/voucher"
)

// These tests drive the endpoints through the real handlers, middleware and
// services, the other modules' included, into a PostgreSQL schema of their
// own. The X-Test-As header names who is calling, with the permissions
// Authenticate would have loaded.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var principals = map[string]authn.Principal{
	"owner": {UserID: "owner", IsOwner: true, Permissions: authn.NewPermissions("dashboard.view", "menu.view", "log.view")},
	"manager": {UserID: "m1", IsManager: true, OutletID: "o1",
		Permissions: authn.NewPermissions("checkout.create", "order.void", "menu.manage", "stock.adjust")},
	"cashier":  {UserID: "k1", OutletID: "o1", Permissions: authn.NewPermissions("checkout.create")},
	"cashier2": {UserID: "k2", OutletID: "o1", Permissions: authn.NewPermissions("checkout.create")},
	"voider":   {UserID: "v1", OutletID: "o1", Permissions: authn.NewPermissions("order.void")},
	"staff":    {UserID: "s1", OutletID: "o1", Permissions: authn.NewPermissions("menu.view", "menu.manage")},
	"manager2": {UserID: "m2", IsManager: true, OutletID: "o2", Permissions: authn.NewPermissions("checkout.create", "order.void")},
	"other":    {UserID: "k3", OutletID: "o2", Permissions: authn.NewPermissions("checkout.create", "order.void")},
}

func actAs() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p, ok := principals[c.GetHeader("X-Test-As")]; ok {
			c.Request = c.Request.WithContext(authn.WithPrincipal(c.Request.Context(), p))
		}
		c.Next()
	}
}

// dbOutlets answers OutletScope from the table, as the outlet module does.
type dbOutlets struct{ pool *pgxpool.Pool }

func (d dbOutlets) OutletExists(ctx context.Context, outletID string) (bool, error) {
	var ok bool
	err := d.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outlets WHERE outlet_id = $1 AND deleted_at IS NULL)`, outletID).Scan(&ok)
	return ok, err
}

// jakarta is o1's time zone.
var jakarta = mustLoad("Asia/Jakarta")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// o1's rates, in basis points.
const (
	o1Service = 500  // 5%
	o1Tax     = 1000 // 10%
)

type harness struct {
	t       *testing.T
	pool    *pgxpool.Pool
	svc     *Service
	menus   *menu.Service
	devices *device.Service
	outlets *outlet.Service
	// vouchers keeps the real clock: its vouchers run from now to a year
	// on, so they are on whatever the harness's clock says.
	vouchers *voucher.Service
	// tables keeps the real clock too: a sale is timed by the database's,
	// and seats its customers from then on.
	tables *table.Service
	router *gin.Engine
	// clock is the server's time: mid-morning in Bandung, so a day's orders
	// never cross midnight while a test runs.
	clock time.Time
	// drinks is a category of o1.
	drinks string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	users := user.NewService(pool, nil, audit.Recorder{}, nil)
	payments := payment.NewService(pool)
	outlets := outlet.NewService(pool, users, payments, Records{}, audit.Recorder{})
	menus := menu.NewService(pool, users, audit.Recorder{})
	devices := device.NewService(pool, users, audit.Recorder{})
	vouchers := voucher.NewService(pool, outlets, devices, Records{}, audit.Recorder{})
	tables := table.NewService(pool, outlets, Records{}, users, audit.Recorder{})
	svc := NewService(pool, menus, devices, payments, outlets, users, vouchers, tables, audit.Recorder{})

	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Recovery(logger))
	protected := r.Group("/api/v1", actAs())
	scoped := protected.Group("/outlets/:oid", middleware.OutletScope(dbOutlets{pool}, logger))
	NewHandler(svc, logger, true).Routes(scoped)

	h := &harness{
		t: t, pool: pool, svc: svc, menus: menus, devices: devices, outlets: outlets, vouchers: vouchers, tables: tables,
		router: r,
		clock:  time.Date(2026, 10, 1, 10, 0, 0, 0, jakarta),
	}
	svc.now = func() time.Time { return h.clock }
	h.exec(`INSERT INTO outlets (outlet_id, code, name, address, phone, timezone, locale, tax_bps, service_bps) VALUES
		('o1', 'BDG01', 'Bandung', 'Jl. Braga 1', '022-123', 'Asia/Jakarta', 'id', $1, $2),
		('o2', 'JKT01', 'Jakarta', NULL, NULL, 'Asia/Jakarta', 'en', 0, 0)`, o1Tax, o1Service)
	h.exec(`INSERT INTO outlet_payment_methods (outlet_id, method, is_enabled) VALUES
		('o1', 'cash', true), ('o1', 'qris', false), ('o1', 'debit', false), ('o2', 'cash', true)`)
	h.exec(`INSERT INTO users (user_id, outlet_id, name, username, email, password_hash, is_owner, is_manager) VALUES
		('owner', NULL, 'Faiz', 'owner', 'owner@maki.test', 'x', true, false),
		('m1', 'o1', 'Rina', 'rina', NULL, 'x', false, true),
		('k1', 'o1', 'Budi', 'budi', NULL, 'x', false, false),
		('k2', 'o1', 'Sari', 'sari', NULL, 'x', false, false),
		('v1', 'o1', 'Andi', 'andi', NULL, 'x', false, false),
		('s1', 'o1', 'Tono', 'tono', NULL, 'x', false, false),
		('m2', 'o2', 'Dewi', 'dewi', NULL, 'x', false, true),
		('k3', 'o2', 'Dodi', 'dodi', NULL, 'x', false, false)`)
	h.drinks = h.category("o1", "Minuman")
	return h
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.WithoutCancel(h.t.Context()), sql, args...); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

func (h *harness) scalar(dst any, sql string, args ...any) {
	h.t.Helper()
	if err := h.pool.QueryRow(h.t.Context(), sql, args...).Scan(dst); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

// manager is the Manager of the outlet, who changes its menus.
func manager(outletID string) authn.Principal {
	if outletID == "o2" {
		return principals["manager2"]
	}
	return principals["manager"]
}

// category adds a category to the outlet and returns its ID.
func (h *harness) category(outletID, name string) string {
	h.t.Helper()
	c, err := h.menus.CreateCategory(h.t.Context(), manager(outletID), "", outletID, menu.CategoryInput{Name: name})
	if err != nil {
		h.t.Fatal(err)
	}
	return c.CategoryID
}

// menu adds a menu to the category of the outlet, at the price; tracked from
// stock when it isn't nil.
func (h *harness) menu(outletID, categoryID, name string, price int64, stock *int) menu.Menu {
	h.t.Helper()
	m, err := h.menus.CreateMenu(h.t.Context(), manager(outletID), "", outletID, menu.MenuInput{
		Name: name, CategoryID: categoryID, Price: &price, TrackStock: stock != nil, InitialStock: stock,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

// till is one of an outlet's devices, as a test sells from it: its token,
// and the receipt numbers it makes.
type till struct {
	device.Device
	token  string
	prefix string
	next   int // the count of its next receipt number today
}

// till activates a device of the outlet, in a browser.
func (h *harness) till(outletID, name string) *till {
	h.t.Helper()
	d, token, err := h.devices.Activate(h.t.Context(), manager(outletID), "", outletID, "", name, device.PlatformWeb)
	if err != nil {
		h.t.Fatal(err)
	}
	code := map[string]string{"o1": "BDG01", "o2": "JKT01"}[outletID]
	return &till{Device: d, token: token, prefix: code + "-" + d.Code, next: 1}
}

// item is a line of an order: qty of the menu, at its price.
func item(m menu.Menu, qty int) map[string]any {
	return map[string]any{"menu_id": m.MenuID, "qty": qty, "price": m.Price}
}

// order is the body of an order from the till, made at the harness's clock,
// of the lines, with the total o1 charges for them and cash enough. The
// till's next receipt number goes with it.
func (h *harness) order(tl *till, lines ...map[string]any) map[string]any {
	h.t.Helper()
	var ls []Line
	for _, l := range lines {
		ls = append(ls, Line{Price: l["price"].(int64), Qty: l["qty"].(int)})
	}
	total := ComputeTotals(ls, 0, o1Service, o1Tax).Total
	if tl.OutletID == "o2" {
		total = ComputeTotals(ls, 0, 0, 0).Total
	}
	no := fmt.Sprintf("%s-%s-%04d", tl.prefix, h.clock.In(jakarta).Format("20060102"), tl.next)
	tl.next++
	return map[string]any{
		"order_id": id.New(), "order_no": no, "type": Takeaway, "client_created_at": h.clock,
		"items": lines, "total": total,
		"payment": map[string]any{"method": "cash", "received": (total/50_000 + 1) * 50_000},
	}
}

type response struct {
	*httptest.ResponseRecorder
	t *testing.T
}

// do sends the request as who, one of principals, or "" for nobody. token,
// when set, is the device token the request carries, in the browser's
// cookie.
func (h *harness) do(method, path, who, token string, body any) response {
	h.t.Helper()
	return h.request(method, path, who, body, func(req *http.Request) {
		if token != "" {
			// A request's cookie has only its name and value; the
			// attributes are the Set-Cookie's.
			req.AddCookie(&http.Cookie{Name: authn.DeviceCookie, Value: token}) //nolint:gosec // as above
		}
	})
}

func (h *harness) request(method, path, who string, body any, edit func(*http.Request)) response {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			h.t.Error(err)
		}
	}
	req := httptest.NewRequestWithContext(h.t.Context(), method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if who != "" {
		req.Header.Set("X-Test-As", who)
	}
	edit(req)
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return response{ResponseRecorder: w, t: h.t}
}

// sell sends the order from the till as the cashier, and returns the order
// taken.
func (h *harness) sell(tl *till, who string, body map[string]any) orderResponse {
	h.t.Helper()
	var o orderResponse
	h.do(http.MethodPost, "/api/v1/outlets/"+tl.OutletID+"/orders", who, tl.token, body).
		status(http.StatusCreated, "").decode(&o)
	return o
}

func (r response) status(want int, code string) response {
	r.t.Helper()
	if r.Code != want {
		r.t.Fatalf("status = %d, want %d: %s", r.Code, want, r.Body)
	}
	if code != "" && r.envelope().Error.Code != code {
		r.t.Fatalf("code = %q, want %q: %s", r.envelope().Error.Code, code, r.Body)
	}
	return r
}

type errorEnvelope struct {
	Error struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func (r response) envelope() errorEnvelope {
	var e errorEnvelope
	_ = json.Unmarshal(r.Body.Bytes(), &e)
	return e
}

func (r response) fields() map[string]any {
	f, _ := r.envelope().Error.Details["fields"].(map[string]any)
	return f
}

func (r response) decode(dst any) {
	r.t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), dst); err != nil {
		r.t.Fatalf("decode: %v: %s", err, r.Body)
	}
}

// stock returns the menu's count, after checking it is its history's: the
// last movement's stock_after, and every change added up.
func (h *harness) stock(menuID string) int {
	h.t.Helper()
	var qty, last, sum int
	h.scalar(&qty, `SELECT stock_qty FROM menus WHERE menu_id = $1`, menuID)
	h.scalar(&last, `SELECT stock_after FROM stock_movements WHERE menu_id = $1
		ORDER BY created_at DESC, stock_movement_id DESC LIMIT 1`, menuID)
	h.scalar(&sum, `SELECT sum(qty_change) FROM stock_movements WHERE menu_id = $1`, menuID)
	if qty != last || qty != sum {
		h.t.Errorf("menu %s: stock_qty %d, last movement %d, movements add up to %d", menuID, qty, last, sum)
	}
	return qty
}

// count runs a count(*) query.
func (h *harness) count(sql string, args ...any) int {
	h.t.Helper()
	var n int
	h.scalar(&n, sql, args...)
	return n
}

// actions returns the Log Action's actions on the target, oldest first.
func (h *harness) actions(targetID string) []string {
	h.t.Helper()
	rows, err := h.pool.Query(h.t.Context(),
		`SELECT action FROM action_logs WHERE target_id = $1 ORDER BY occurred_at, action_log_id`, targetID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

// waitForLock waits until n sessions wait for the lock the query counts.
func (h *harness) waitForLock(n int, query string, args ...any) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for waiting := 0; waiting < n; {
		if time.Now().After(deadline) {
			h.t.Fatalf("%d of %d sessions waiting after 10s", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
		h.scalar(&waiting, query, args...)
	}
}

// waitingOn counts the sessions that wait for a row of the table.
func waitingOn(table string) string {
	return `SELECT count(DISTINCT pid) FROM pg_locks WHERE locktype = 'tuple' AND relation = '` + table + `'::regclass`
}
