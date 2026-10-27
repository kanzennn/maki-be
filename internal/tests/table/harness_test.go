package table

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
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
	"github.com/kervzent/maki-be/internal/utils/response"
)

// These tests drive the table endpoints through the real routes,
// middleware, controllers, services and repositories, the outlet service's
// included, into a PostgreSQL schema of their own. The X-Test-As header
// names who is calling, with the permissions Authenticate would have loaded.
// The sales that seat customers are the checkout's, which the order tests
// take through it; here they are fakeOrders, read as the real query reads
// them.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var principals = map[string]models.Principal{
	"owner": {UserID: "owner", IsOwner: true, Permissions: models.NewPermissions("menu.view", "table.view", "log.view")},
	"manager": {UserID: "m1", IsManager: true, OutletID: "o1", Permissions: models.NewPermissions(
		"table.view", "table.manage", "checkout.create", "menu.manage")},
	"manager2": {UserID: "m2", IsManager: true, OutletID: "o2", Permissions: models.NewPermissions(
		"table.view", "table.manage", "checkout.create")},
	"cashier": {UserID: "k1", OutletID: "o1", Permissions: models.NewPermissions("checkout.create")},
	"viewer":  {UserID: "v1", OutletID: "o1", Permissions: models.NewPermissions("table.view")},
	"staff":   {UserID: "s1", OutletID: "o1", Permissions: models.NewPermissions("menu.view", "order.void")},
}

func actAs() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p, ok := principals[c.GetHeader("X-Test-As")]; ok {
			c.Request = c.Request.WithContext(models.WithPrincipal(c.Request.Context(), p))
		}
		c.Next()
	}
}

// fakeOrders is the orders' record of the paid dine-in sales at each table,
// by outlet: when each was written.
type fakeOrders struct {
	mu    sync.Mutex
	sales map[string]map[string][]time.Time // outlet ID → table ID → sales
}

func (f *fakeOrders) TableSales(_ context.Context, outletID string, since time.Time) (map[string][]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string][]time.Time{}
	for tableID, sales := range f.sales[outletID] {
		for _, s := range sales {
			// As the query reads them: written after since.
			if s.After(since) {
				out[tableID] = append(out[tableID], s)
			}
		}
	}
	return out, nil
}

// sell records a paid dine-in sale at the table at the moment at.
func (f *fakeOrders) sell(outletID, tableID string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sales[outletID] == nil {
		f.sales[outletID] = map[string][]time.Time{}
	}
	f.sales[outletID][tableID] = append(f.sales[outletID][tableID], at)
}

// void takes the sale at the moment at off the record, as its void does.
func (f *fakeOrders) void(outletID, tableID string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sales[outletID][tableID] = slices.DeleteFunc(f.sales[outletID][tableID], at.Equal)
}

// jakarta is the outlets' time zone.
var jakarta = mustLoad("Asia/Jakarta")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	db     *config.Database
	tables *services.TableService
	modes  *services.DineInModeService
	orders *fakeOrders
	router *gin.Engine
	// clock is the server's time.
	clock time.Time
}

// newHarness sets up three outlets in Jakarta's time zone: o1, open 10:00
// to 22:00, o2 without hours, and o3; o1's Manager, cashier, a user who
// may only look at tables, and one who may not; o2's Manager; and the
// Owner. The clock is 1 October 2026, 12:00 in Jakarta.
func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	db := config.NewDatabase(pool)
	audit := services.NewAuditService(repositories.NewAuditRepository(db))
	users := services.NewUserService(repositories.NewUserRepository(db), nil)
	outlets := services.NewOutletService(db, repositories.NewOutletRepository(db), users, nil, nil, nil, audit)
	orders := &fakeOrders{sales: map[string]map[string][]time.Time{}}
	h := &harness{
		t: t, pool: pool, db: db, orders: orders,
		clock: time.Date(2026, 10, 1, 12, 0, 0, 0, jakarta),
	}
	now := func() time.Time { return h.clock }
	areas, tables := repositories.NewAreaRepository(db), repositories.NewTableRepository(db)
	h.modes = services.NewDineInModeService(db, repositories.NewDineInModeRepository(db), audit)
	h.tables = services.NewTableService(db, tables, areas, h.modes, outlets, orders, audit, now)

	r := gin.New()
	r.Use(middlewares.RequestID(), middlewares.Recovery(logger), middlewares.ErrorHandler(logger))
	protected := r.Group("/api/v1", actAs())
	scoped := protected.Group("/outlets/:oid", middlewares.OutletScope(outlets, logger))
	routes.RegisterTableRoutes(scoped, routes.TableControllers{
		Areas:     controllers.NewAreaController(services.NewAreaService(db, areas, audit)),
		Layouts:   controllers.NewLayoutController(services.NewLayoutService(db, areas, tables, h.tables, audit)),
		Tables:    controllers.NewTableController(h.tables),
		Schedules: controllers.NewScheduleController(services.NewScheduleService(db, repositories.NewScheduleRepository(db), tables, users, audit, now)),
		Modes:     controllers.NewDineInModeController(h.modes),
	})
	h.router = r
	h.exec(`INSERT INTO outlets (outlet_id, code, name, timezone, locale, open_time, close_time) VALUES
		('o1', 'BDG01', 'Bandung', 'Asia/Jakarta', 'id', '10:00', '22:00'),
		('o2', 'JKT01', 'Jakarta', 'Asia/Jakarta', 'en', NULL, NULL),
		('o3', 'ACH01', 'Aceh', 'Asia/Jakarta', 'id', NULL, NULL)`)
	h.exec(`INSERT INTO users (user_id, outlet_id, name, username, email, password_hash, is_owner, is_manager) VALUES
		('owner', NULL, 'Faiz', 'owner', 'owner@maki.test', 'x', true, false),
		('m1', 'o1', 'Rina', 'rina', NULL, 'x', false, true),
		('m2', 'o2', 'Dewi', 'dewi', NULL, 'x', false, true),
		('k1', 'o1', 'Budi', 'budi', NULL, 'x', false, false),
		('v1', 'o1', 'Wati', 'wati', NULL, 'x', false, false),
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

// waitBlockedBy waits until a session waits for a lock the session pid
// holds.
func (h *harness) waitBlockedBy(pid int) {
	h.t.Helper()
	h.waitForLock(1, `SELECT count(*) FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid))`, pid)
}

// at is a time on the harness's day, days later, in Jakarta.
func (h *harness) at(days int, hhmm string) time.Time {
	h.t.Helper()
	t, err := time.ParseInLocation("15:04", hhmm, jakarta)
	if err != nil {
		h.t.Fatal(err)
	}
	y, m, d := h.clock.In(jakarta).Date()
	return time.Date(y, m, d+days, t.Hour(), t.Minute(), 0, 0, jakarta)
}

// area has o1's Manager create an area, and returns it.
func (h *harness) area(name string) models.AreaResponse {
	h.t.Helper()
	var a models.AreaResponse
	h.do(http.MethodPost, "/api/v1/outlets/o1/areas", "manager", map[string]any{"name": name}).
		status(http.StatusCreated, "").decode(&a)
	return a
}

// table has o1's Manager create a table in the area, and returns it.
func (h *harness) table(areaID, name string, fields ...any) models.TableResponse {
	h.t.Helper()
	body := map[string]any{"name": name, "area_id": areaID}
	for i := 0; i+1 < len(fields); i += 2 {
		body[fields[i].(string)] = fields[i+1]
	}
	var t models.TableResponse
	h.do(http.MethodPost, "/api/v1/outlets/o1/tables", "manager", body).status(http.StatusCreated, "").decode(&t)
	return t
}

// get returns o1's table as its Manager sees it.
func (h *harness) get(tableID string) models.TableResponse {
	h.t.Helper()
	var t models.TableResponse
	h.do(http.MethodGet, "/api/v1/outlets/o1/tables/"+tableID, "manager", nil).status(http.StatusOK, "").decode(&t)
	return t
}

// layout returns the area's layout in o1.
func (h *harness) layout(areaID string) models.LayoutResponse {
	h.t.Helper()
	var l models.LayoutResponse
	h.do(http.MethodGet, "/api/v1/outlets/o1/areas/"+areaID+"/layout", "manager", nil).
		status(http.StatusOK, "").decode(&l)
	return l
}

// placing is a table's place in a layout body.
func placing(tableID string, x, y, w, h int) map[string]any {
	return map[string]any{"table_id": tableID, "x": x, "y": y, "w": w, "h": h, "rotation": 0, "shape": models.ShapeSquare}
}

// deco is a decoration in a layout body.
func deco(kind string, x, y, w, h int) map[string]any {
	return map[string]any{"kind": kind, "x": x, "y": y, "w": w, "h": h, "rotation": 0}
}

// saveLayout has o1's Manager save the area's layout on version.
func (h *harness) saveLayout(areaID string, version int, tables, decorations []map[string]any) reply {
	h.t.Helper()
	if tables == nil {
		tables = []map[string]any{}
	}
	if decorations == nil {
		decorations = []map[string]any{}
	}
	return h.do(http.MethodPut, "/api/v1/outlets/o1/areas/"+areaID+"/layout", "manager", map[string]any{
		"version": version, "tables": tables, "decorations": decorations,
	})
}

type reply struct {
	*httptest.ResponseRecorder
	t *testing.T
}

// do sends the request as who, one of principals, or "" for nobody.
func (h *harness) do(method, path, who string, body any) reply {
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

// tables decodes a list of tables.
func (r reply) tables() response.List[models.TableResponse] {
	r.t.Helper()
	var l response.List[models.TableResponse]
	r.decode(&l)
	return l
}

// names are the tables' names, in order.
func names(ts []models.TableResponse) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
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
