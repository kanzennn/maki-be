package log

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
	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/routes"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/tests/dbtest"
)

// These tests drive the Log Action endpoints through the real routes, middleware,
// controllers, services and repositories into a PostgreSQL schema of their
// own. The X-Test-As header names who is calling, with the permissions
// Authenticate would have loaded.

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

var principals = map[string]models.Principal{
	"owner":    {UserID: "owner", IsOwner: true, Permissions: models.NewPermissions(models.OwnerPermissions()...)},
	"manager":  {UserID: "m1", IsManager: true, OutletID: "o1", Permissions: models.NewPermissions(models.AllPermissions()...)},
	"manager2": {UserID: "m2", IsManager: true, OutletID: "o2", Permissions: models.NewPermissions(models.AllPermissions()...)},
	"cashier":  {UserID: "k1", OutletID: "o1", Permissions: models.NewPermissions("checkout.create")},
}

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
	userRepo := repositories.NewUserRepository(db)
	users := services.NewUserService(userRepo, nil)
	outletRepo := repositories.NewOutletRepository(db)
	outlets := services.NewOutletService(db, outletRepo, users, nil, nil, repositories.NewOrderRepository(db), audit)

	r := gin.New()
	r.Use(middlewares.RequestID(), middlewares.Recovery(logger), middlewares.ErrorHandler(logger))
	protected := r.Group("/api/v1", actAs())
	owner := protected.Group("", middlewares.RequireOwner())
	scoped := protected.Group("/outlets/:oid", middlewares.OutletScope(outlets, logger))
	routes.RegisterLogRoutes(owner, scoped, controllers.NewLogController(
		services.NewLogService(repositories.NewAuditRepository(db), userRepo, repositories.NewDeviceRepository(db), outlets)))

	h := &harness{t: t, pool: pool, router: r}
	h.exec(`INSERT INTO outlets (outlet_id, code, name, timezone, locale) VALUES
		('o1', 'BDG01', 'Bandung', 'Asia/Jakarta', 'id'), ('o2', 'JYP01', 'Jayapura', 'Asia/Jayapura', 'id')`)
	h.exec(`INSERT INTO users (user_id, outlet_id, name, username, email, password_hash, is_owner, is_manager) VALUES
		('owner', NULL, 'Faiz', 'owner', 'owner@maki.test', 'x', true, false),
		('m1', 'o1', 'Rina', 'rina', NULL, 'x', false, true),
		('m2', 'o2', 'Dewi', 'dewi', NULL, 'x', false, true),
		('k1', 'o1', 'Budi', 'budi', NULL, 'x', false, false),
		('k2', 'o1', 'Sari', 'sari', NULL, 'x', false, false)`)
	return h
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.WithoutCancel(h.t.Context()), sql, args...); err != nil {
		h.t.Fatalf("%s: %v", sql, err)
	}
}

type reply struct {
	*httptest.ResponseRecorder
	t *testing.T
}

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
	if code != "" {
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &e)
		if e.Error.Code != code {
			r.t.Fatalf("code = %q, want %q: %s", e.Error.Code, code, r.Body)
		}
	}
	return r
}

func (r reply) decode(dst any) {
	r.t.Helper()
	if err := json.Unmarshal(r.Body.Bytes(), dst); err != nil {
		r.t.Fatalf("decode: %v: %s", err, r.Body)
	}
}
