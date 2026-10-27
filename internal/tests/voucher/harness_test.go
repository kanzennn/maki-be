package voucher

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/routes"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/tests/dbtest"
	"github.com/kervzent/maki-be/internal/utils/cookie"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/utils/response"
)

// These tests drive the voucher endpoints through the real routes,
// middleware, controllers, services and repositories, the outlet and device
// services' included, into a PostgreSQL schema of their own. The X-Test-As
// header names who is calling, with the permissions Authenticate would have
// loaded. The sales that use vouchers are the checkout's, so the order tests
// take them through it; here a use is taken with Redeem, as checkout does.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var principals = map[string]models.Principal{
	"owner":    {UserID: "owner", IsOwner: true, Permissions: models.NewPermissions("dashboard.view", "menu.view", "log.view")},
	"manager":  {UserID: "m1", IsManager: true, OutletID: "o1", Permissions: models.NewPermissions("checkout.create", "menu.manage")},
	"manager2": {UserID: "m2", IsManager: true, OutletID: "o2", Permissions: models.NewPermissions("checkout.create")},
	"cashier":  {UserID: "k1", OutletID: "o1", Permissions: models.NewPermissions("checkout.create")},
	"cashier2": {UserID: "k2", OutletID: "o2", Permissions: models.NewPermissions("checkout.create")},
	"staff":    {UserID: "s1", OutletID: "o1", Permissions: models.NewPermissions("menu.view", "order.void")},
}

func actAs() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p, ok := principals[c.GetHeader("X-Test-As")]; ok {
			c.Request = c.Request.WithContext(models.WithPrincipal(c.Request.Context(), p))
		}
		c.Next()
	}
}

// fakeOrders is the orders' record of the sales that used each
// voucher: newest first, by voucher ID.
type fakeOrders map[string][]models.VoucherUse

func (f fakeOrders) VoucherUses(_ context.Context, voucherID string, page pagination.Page) ([]models.VoucherUse, int, error) {
	uses := f[voucherID]
	from := min(page.Offset(), len(uses))
	to := min(from+page.Size, len(uses))
	return append([]models.VoucherUse(nil), uses[from:to]...), len(uses), nil
}

// jakarta is the outlets' time zone, and the vouchers' WIB.
var jakarta = mustLoad("Asia/Jakarta")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type harness struct {
	t       *testing.T
	pool    *pgxpool.Pool
	db      *config.Database
	svc     *services.VoucherService
	devices *services.DeviceService
	orders  fakeOrders
	router  *gin.Engine
	// clock is the server's time.
	clock time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := config.NewDatabase(pool)
	audit := services.NewAuditService(repositories.NewAuditRepository(db))
	users := services.NewUserService(repositories.NewUserRepository(db), nil)
	// Vouchers only name outlets through the outlet service.
	outlets := services.NewOutletService(db, repositories.NewOutletRepository(db), users, nil, nil, nil, audit)
	devices := services.NewDeviceService(db, repositories.NewDeviceRepository(db), users, audit)
	orders := fakeOrders{}
	h := &harness{
		t: t, pool: pool, db: db, devices: devices, orders: orders,
		clock: time.Date(2026, 10, 1, 10, 0, 0, 0, jakarta),
	}
	h.svc = services.NewVoucherService(db, repositories.NewVoucherRepository(db), outlets, devices, orders, audit,
		func() time.Time { return h.clock })

	r := gin.New()
	r.Use(middlewares.RequestID(), middlewares.Recovery(logger), middlewares.ErrorHandler(logger))
	protected := r.Group("/api/v1", actAs())
	owner := protected.Group("", middlewares.RequireOwner())
	scoped := protected.Group("/outlets/:oid", middlewares.OutletScope(outlets, logger))
	routes.RegisterVoucherRoutes(owner, scoped, controllers.NewVoucherController(h.svc))
	h.router = r
	h.exec(`INSERT INTO outlets (outlet_id, code, name, timezone, locale) VALUES
		('o1', 'BDG01', 'Bandung', 'Asia/Jakarta', 'id'),
		('o2', 'JKT01', 'Jakarta', 'Asia/Jakarta', 'en'),
		('o3', 'ACH01', 'aceh', 'Asia/Jakarta', 'id')`)
	h.exec(`INSERT INTO users (user_id, outlet_id, name, username, email, password_hash, is_owner, is_manager) VALUES
		('owner', NULL, 'Faiz', 'owner', 'owner@maki.test', 'x', true, false),
		('m1', 'o1', 'Rina', 'rina', NULL, 'x', false, true),
		('m2', 'o2', 'Dewi', 'dewi', NULL, 'x', false, true),
		('k1', 'o1', 'Budi', 'budi', NULL, 'x', false, false),
		('k2', 'o2', 'Sari', 'sari', NULL, 'x', false, false),
		('s1', 'o1', 'Tono', 'tono', NULL, 'x', false, false)`)
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

// at is a time on the harness's day, in WIB.
func (h *harness) at(days int, hhmm string) time.Time {
	h.t.Helper()
	t, err := time.ParseInLocation("15:04", hhmm, jakarta)
	if err != nil {
		h.t.Fatal(err)
	}
	y, m, d := h.clock.In(jakarta).Date()
	return time.Date(y, m, d+days, t.Hour(), t.Minute(), 0, 0, jakarta)
}

// body is a voucher as the Owner fills it in: 10% off, at most Rp 20.000,
// 100 uses, for every outlet, until a month from the harness's day. edits
// change it.
func (h *harness) body(code string, edits ...func(map[string]any)) map[string]any {
	b := map[string]any{
		"code": code, "name": "Diskon " + code, "type": models.VoucherPercent, "value": 1000, "max_discount": 20_000,
		"quota": 100, "expired_at": h.at(30, "23:59"), "all_outlets": true,
	}
	for _, edit := range edits {
		edit(b)
	}
	return b
}

// set returns an edit that sets the fields of a body.
func set(kv ...any) func(map[string]any) {
	return func(b map[string]any) {
		for i := 0; i < len(kv); i += 2 {
			b[kv[i].(string)] = kv[i+1]
		}
	}
}

// without returns an edit that leaves the fields out of a body.
func without(keys ...string) func(map[string]any) {
	return func(b map[string]any) {
		for _, k := range keys {
			delete(b, k)
		}
	}
}

// create has the Owner create the voucher, and returns it.
func (h *harness) create(body map[string]any) models.VoucherResponse {
	h.t.Helper()
	var v models.VoucherResponse
	h.do(http.MethodPost, "/api/v1/vouchers", "owner", "", body).status(http.StatusCreated, "").decode(&v)
	return v
}

// get returns the voucher as the Owner sees it.
func (h *harness) get(voucherID string) models.VoucherResponse {
	h.t.Helper()
	var v models.VoucherResponse
	h.do(http.MethodGet, "/api/v1/vouchers/"+voucherID, "owner", "", nil).status(http.StatusOK, "").decode(&v)
	return v
}

// redeem takes a use of the voucher for a sale in the outlet, as checkout
// does, and returns what the sale gets or the error.
func (h *harness) redeem(outletID, code string, subtotal int64) (models.VoucherTaken, error) {
	h.t.Helper()
	var taken models.VoucherTaken
	err := h.db.InTx(h.t.Context(), func(ctx context.Context) error {
		var err error
		taken, err = h.svc.Redeem(ctx, outletID, code, subtotal)
		return err
	})
	return taken, err
}

// release gives a use of the voucher back, as a void does.
func (h *harness) release(voucherID string) {
	h.t.Helper()
	err := h.db.InTx(h.t.Context(), func(ctx context.Context) error {
		return h.svc.Release(ctx, voucherID)
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// till activates a device of the outlet and returns its token.
func (h *harness) till(outletID string) string {
	h.t.Helper()
	manager := map[string]models.Principal{"o1": principals["manager"], "o2": principals["manager2"]}[outletID]
	_, token, err := h.devices.Activate(h.t.Context(), manager, "", outletID, "", "Kasir", models.PlatformWeb)
	if err != nil {
		h.t.Fatal(err)
	}
	return token
}

type reply struct {
	*httptest.ResponseRecorder
	t *testing.T
}

// do sends the request as who, one of principals, or "" for nobody. token,
// when set, is the device token the request carries, in the browser's
// cookie.
func (h *harness) do(method, path, who, token string, body any) reply {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			h.t.Fatal(err)
		}
	}
	req := httptest.NewRequestWithContext(h.t.Context(), method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if who != "" {
		req.Header.Set("X-Test-As", who)
	}
	if token != "" {
		// A request's cookie has only its name and value; the attributes
		// are the Set-Cookie's.
		req.AddCookie(&http.Cookie{Name: cookie.DeviceCookie, Value: token}) //nolint:gosec // as above
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return reply{ResponseRecorder: w, t: h.t}
}

func (r reply) status(want int, code string) reply {
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

func (r reply) envelope() errorEnvelope {
	var e errorEnvelope
	_ = json.Unmarshal(r.Body.Bytes(), &e)
	return e
}

func (r reply) fields() map[string]any {
	f, _ := r.envelope().Error.Details["fields"].(map[string]any)
	return f
}

func (r reply) decode(dst any) {
	r.t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), dst); err != nil {
		r.t.Fatalf("decode: %v: %s", err, r.Body)
	}
}

// list decodes a list response.
func (r reply) list() response.List[models.VoucherResponse] {
	r.t.Helper()
	var l response.List[models.VoucherResponse]
	r.decode(&l)
	return l
}

// codes are the vouchers' codes, in order.
func codes(vs []models.VoucherResponse) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Code
	}
	return out
}

// logEntry is a Log Action entry, as these tests read it.
type logEntry struct {
	Action   string
	UserID   *string
	OutletID *string
	Before   map[string]any
	After    map[string]any
}

// log returns the Log Action's entries on the target, oldest first.
func (h *harness) log(targetID string) []logEntry {
	h.t.Helper()
	rows, err := h.pool.Query(h.t.Context(), `SELECT action, user_id, outlet_id, before_json, after_json
		FROM action_logs WHERE target_id = $1 ORDER BY occurred_at, action_log_id`, targetID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []logEntry
	for rows.Next() {
		var e logEntry
		var before, after []byte
		if err := rows.Scan(&e.Action, &e.UserID, &e.OutletID, &before, &after); err != nil {
			h.t.Fatal(err)
		}
		for _, j := range []struct {
			raw []byte
			dst *map[string]any
		}{{before, &e.Before}, {after, &e.After}} {
			if j.raw != nil {
				if err := json.Unmarshal(j.raw, j.dst); err != nil {
					h.t.Fatal(err)
				}
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		h.t.Fatal(err)
	}
	return out
}

// actions are the actions of the entries.
func actions(entries []logEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Action
	}
	return out
}
