package menu

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
	"github.com/kervzent/maki-be/internal/platform/middleware"
	"github.com/kervzent/maki-be/internal/user"
)

// These tests drive the endpoints through the real handlers, middleware and
// service into a PostgreSQL schema of their own. The X-Test-As header names
// who is calling, with the permissions Authenticate would have loaded.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var principals = map[string]authn.Principal{
	// The Owner looks into every outlet's menu, and changes none.
	"owner": {UserID: "owner", IsOwner: true, Permissions: authn.NewPermissions("menu.view", "user.view")},
	"manager": {UserID: "m1", IsManager: true, OutletID: "o1", Permissions: authn.NewPermissions(
		"menu.view", "menu.manage", "stock.adjust", "checkout.create")},
	"manager2": {UserID: "m2", IsManager: true, OutletID: "o2", Permissions: authn.NewPermissions(
		"menu.view", "menu.manage", "stock.adjust")},
	// A chef edits the menu but doesn't count stock.
	"chef": {UserID: "c1", OutletID: "o1", Permissions: authn.NewPermissions("menu.view", "menu.manage")},
	// A storekeeper counts stock but doesn't edit the menu.
	"storekeeper": {UserID: "g1", OutletID: "o1", Permissions: authn.NewPermissions("menu.view", "stock.adjust")},
	"cashier":     {UserID: "k1", OutletID: "o1", Permissions: authn.NewPermissions("checkout.create")},
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

type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	svc    *Service
	router *gin.Engine
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	// The user module names who changed the stock; nothing else of it is used.
	svc := NewService(pool, user.NewService(pool, nil, audit.Recorder{}, nil), audit.Recorder{})

	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Recovery(logger))
	protected := r.Group("/api/v1", actAs())
	scoped := protected.Group("/outlets/:oid", middleware.OutletScope(dbOutlets{pool}, logger))
	NewHandler(svc, logger).Routes(scoped)

	h := &harness{t: t, pool: pool, svc: svc, router: r}
	h.exec(`INSERT INTO outlets (outlet_id, code, name, timezone, locale) VALUES
		('o1', 'O1', 'Bandung', 'Asia/Jakarta', 'id'), ('o2', 'O2', 'Jakarta', 'Asia/Jakarta', 'id')`)
	// The accounts the principals act for, which the Log Action and the
	// stock history name.
	h.exec(`INSERT INTO users (user_id, outlet_id, name, username, email, password_hash, is_owner, is_manager) VALUES
		('owner', NULL, 'Faiz', 'owner', 'owner@maki.test', 'x', true, false),
		('m1', 'o1', 'Rina', 'rina', NULL, 'x', false, true),
		('m2', 'o2', 'Dewi', 'dewi', NULL, 'x', false, true),
		('c1', 'o1', 'Chandra', 'chandra', NULL, 'x', false, false),
		('g1', 'o1', 'Gita', 'gita', NULL, 'x', false, false),
		('k1', 'o1', 'Budi', 'budi', NULL, 'x', false, false)`)
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

type response struct {
	*httptest.ResponseRecorder
	t *testing.T
}

// do sends the request as who, one of principals, or "" for nobody.
func (h *harness) do(method, path, who string, body any) response {
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
	return response{ResponseRecorder: w, t: h.t}
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

// lastLog returns the Log Action's latest entry on the target: its action,
// and what it recorded before and after.
func (h *harness) lastLog(targetID string) (action string, before, after map[string]any) {
	h.t.Helper()
	var b, a []byte
	h.scalar(&action, `SELECT action FROM action_logs WHERE target_id = $1 ORDER BY occurred_at DESC, action_log_id DESC LIMIT 1`, targetID)
	if err := h.pool.QueryRow(h.t.Context(),
		`SELECT before_json, after_json FROM action_logs WHERE target_id = $1 ORDER BY occurred_at DESC, action_log_id DESC LIMIT 1`,
		targetID).Scan(&b, &a); err != nil {
		h.t.Fatal(err)
	}
	_ = json.Unmarshal(b, &before)
	_ = json.Unmarshal(a, &after)
	return action, before, after
}
