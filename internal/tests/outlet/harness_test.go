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

// These tests drive the outlet endpoints through the real routes,
// middleware, controllers, services and repositories into a PostgreSQL
// schema of their own. Signing in is for the auth tests; here the X-Test-As
// header names who is calling.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var principals = map[string]models.Principal{
	"owner":   {UserID: "owner", IsOwner: true},
	"manager": {UserID: "manager", IsManager: true, OutletID: "o-manager"},
}

// actAs puts the principal the X-Test-As header names on the request, the
// way Authenticate would.
func actAs() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p, ok := principals[c.GetHeader("X-Test-As")]; ok {
			c.Request = c.Request.WithContext(models.WithPrincipal(c.Request.Context(), p))
		}
		c.Next()
	}
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
	db := config.NewDatabase(pool)
	audit := services.NewAuditService(repositories.NewAuditRepository(db))
	outlets := services.NewOutletService(db, repositories.NewOutletRepository(db),
		services.NewUserService(repositories.NewUserRepository(db), nil),
		services.NewRoleService(db, repositories.NewRoleRepository(db), audit),
		services.NewPaymentService(repositories.NewPaymentRepository(db)), repositories.NewOrderRepository(db), audit)

	r := gin.New()
	r.Use(middlewares.RequestID(), middlewares.Recovery(logger), middlewares.ErrorHandler(logger))
	api := r.Group("/api/v1", actAs())
	routes.RegisterOutletRoutes(api.Group("", middlewares.RequireOwner()), controllers.NewOutletController(outlets))

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

type reply struct {
	*httptest.ResponseRecorder
	t *testing.T
}

// do sends the request as who ("owner", "manager", or "" for nobody).
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

// status fails the test unless the response has the status and, for an
// error, the code.
func (r reply) status(want int, code string) reply {
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

func (r reply) errorCode() string {
	var e errorEnvelope
	_ = json.Unmarshal(r.Body.Bytes(), &e)
	return e.Error.Code
}

// fields returns the details.fields of a validation_failed error.
func (r reply) fields() map[string]any {
	var e errorEnvelope
	_ = json.Unmarshal(r.Body.Bytes(), &e)
	f, _ := e.Error.Details["fields"].(map[string]any)
	return f
}

func (r reply) outlet() models.OutletResponse {
	r.t.Helper()
	var o models.OutletResponse
	if err := json.Unmarshal(r.Body.Bytes(), &o); err != nil {
		r.t.Fatalf("not an outlet: %v: %s", err, r.Body)
	}
	return o
}

type listResponse struct {
	Data []models.OutletResponse `json:"data"`
	Meta response.ListMeta       `json:"meta"`
}

func (r reply) list() listResponse {
	r.t.Helper()
	var l listResponse
	if err := json.Unmarshal(r.Body.Bytes(), &l); err != nil {
		r.t.Fatalf("not a list: %v: %s", err, r.Body)
	}
	return l
}

// create makes an outlet as the Owner and returns it.
func (h *harness) create(body map[string]any) models.OutletResponse {
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
