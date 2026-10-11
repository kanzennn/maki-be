package outlet

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/payment"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
	"github.com/kervzent/maki-be/internal/platform/password"
	"github.com/kervzent/maki-be/internal/user"
)

// These tests drive the endpoints through the real handlers, middleware and
// services into a PostgreSQL schema of their own. Signing in is the auth
// module's to test; here the X-Test-As header names who is calling.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var principals = map[string]authn.Principal{
	"owner":   {UserID: "owner", IsOwner: true},
	"manager": {UserID: "manager", IsManager: true, OutletID: "o-manager"},
}

// actAs puts the principal the X-Test-As header names on the request, the
// way Authenticate would.
func actAs() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p, ok := principals[c.GetHeader("X-Test-As")]; ok {
			c.Request = c.Request.WithContext(authn.WithPrincipal(c.Request.Context(), p))
		}
		c.Next()
	}
}

// sqlOrders tells whether an outlet has orders the way order.Records does,
// standing in for the order module, which imports this one.
type sqlOrders struct{}

func (sqlOrders) HasOrders(ctx context.Context, tx pgx.Tx, outletID string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM orders WHERE outlet_id = $1)`, outletID).Scan(&ok)
	return ok, err
}

type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router *gin.Engine
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := dbtest.New(t)
	logger := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelWarn}))
	users := user.NewService(pool, password.NewHasher(password.Params{Memory: 1024, Time: 1, Threads: 1}, 2), audit.Recorder{}, nil)

	r := gin.New()
	r.Use(middleware.RequestID(), middleware.Recovery(logger))
	api := r.Group("/api/v1", actAs())
	NewHandler(NewService(pool, users, payment.NewService(pool), sqlOrders{}, audit.Recorder{}), logger).Routes(api.Group("", middleware.RequireOwner()))

	h := &harness{t: t, pool: pool, router: r}
	// The Log Action names the Owner, so the account must exist.
	h.exec(`INSERT INTO users (user_id, name, username, email, password_hash, is_owner)
		VALUES ('owner', 'Faiz', 'owner', 'owner@maki.test', 'x', true)`)
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

// do sends the request as who ("owner", "manager", or "" for nobody).
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

// status fails the test unless the response has the status and, for an
// error, the code.
func (r response) status(want int, code string) response {
	r.t.Helper()
	if r.Code != want {
		r.t.Fatalf("status = %d, want %d: %s", r.Code, want, r.Body)
	}
	if code != "" && r.errorCode() != code {
		r.t.Fatalf("code = %q, want %q: %s", r.errorCode(), code, r.Body)
	}
	return r
}

type errorEnvelope struct {
	Error struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func (r response) errorCode() string {
	var e errorEnvelope
	_ = json.Unmarshal(r.Body.Bytes(), &e)
	return e.Error.Code
}

// fields returns the details.fields of a validation_failed error.
func (r response) fields() map[string]any {
	var e errorEnvelope
	_ = json.Unmarshal(r.Body.Bytes(), &e)
	f, _ := e.Error.Details["fields"].(map[string]any)
	return f
}

func (r response) outlet() outletResponse {
	r.t.Helper()
	var o outletResponse
	if err := json.Unmarshal(r.Body.Bytes(), &o); err != nil {
		r.t.Fatalf("not an outlet: %v: %s", err, r.Body)
	}
	return o
}

type listResponse struct {
	Data []outletResponse `json:"data"`
	Meta httpx.ListMeta   `json:"meta"`
}

func (r response) list() listResponse {
	r.t.Helper()
	var l listResponse
	if err := json.Unmarshal(r.Body.Bytes(), &l); err != nil {
		r.t.Fatalf("not a list: %v: %s", err, r.Body)
	}
	return l
}

// create makes an outlet as the Owner and returns it.
func (h *harness) create(body map[string]any) outletResponse {
	h.t.Helper()
	return h.do(http.MethodPost, "/api/v1/outlets", "owner", body).status(http.StatusCreated, "").outlet()
}

// actions returns the Log Action's actions for the outlet, oldest first.
func (h *harness) actions(outletID string) []string {
	h.t.Helper()
	rows, err := h.pool.Query(h.t.Context(),
		`SELECT action FROM action_logs WHERE outlet_id = $1 ORDER BY occurred_at, action_log_id`, outletID)
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
