package main

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/auth"
	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/health"
	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/order"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/config"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
	"github.com/kervzent/maki-be/internal/table"
	"github.com/kervzent/maki-be/internal/user"
	"github.com/kervzent/maki-be/internal/voucher"
)

// maxBodyBytes caps JSON request bodies. File uploads go straight to object
// storage, so no legitimate request comes close.
const maxBodyBytes = 1 << 20

// routerDeps are what the routes need, built in main.
type routerDeps struct {
	pinger     health.Pinger
	tokens     *authn.Tokens
	principals authn.PrincipalLoader
	auth       *auth.Handler
	// outletFinder is what OutletScope asks whether an outlet exists.
	outletFinder middleware.OutletFinder
	outlets      *outlet.Handler
	users        *user.Handler
	menus        *menu.Handler
	devices      *device.Handler
	tables       *table.Handler
	vouchers     *voucher.Handler
	orders       *order.Handler
}

// newRouter wires the middleware chain in the order the architecture
// standard fixes, then registers every module's routes.
func newRouter(cfg config.Config, logger *slog.Logger, deps routerDeps) (*gin.Engine, error) {
	r := gin.New()
	r.HandleMethodNotAllowed = true

	// Without this Gin trusts X-Forwarded-For from anyone, so ClientIP (and
	// rate limiting keyed on it) could be spoofed.
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return nil, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}

	r.Use(
		middleware.RequestID(),
		middleware.Logger(logger),
		middleware.Recovery(logger),
		middleware.SecurityHeaders(),
		middleware.BodyLimit(maxBodyBytes),
		middleware.CORS(cfg.CORSAllowedOrigins),
	)

	health.NewHandler(deps.pinger, logger).Routes(r)

	// Groups under /api/v1: public; authed, which needs a valid access
	// token; protected, which also needs no pending password change; owner,
	// only for the Owner; and scoped, an outlet's own routes under
	// /outlets/:oid, where each route names its permission. Modules register
	// on protected unless an endpoint must stay reachable before the
	// password is changed.
	public := r.Group("/api/v1")
	authed := public.Group("", middleware.Authenticate(deps.tokens, deps.principals, logger))
	protected := authed.Group("", middleware.RequirePasswordChanged())
	owner := protected.Group("", middleware.RequireOwner())
	scoped := protected.Group("/outlets/:oid", middleware.OutletScope(deps.outletFinder, logger))
	deps.auth.Routes(public, authed, protected)
	deps.outlets.Routes(owner)
	deps.users.Routes(protected, scoped)
	deps.menus.Routes(scoped)
	deps.devices.Routes(scoped)
	deps.tables.Routes(scoped)
	deps.vouchers.Routes(owner, scoped)
	deps.orders.Routes(scoped)

	r.NoRoute(func(c *gin.Context) {
		httpx.WriteError(c, http.StatusNotFound, "not_found")
	})
	r.NoMethod(func(c *gin.Context) {
		httpx.WriteError(c, http.StatusMethodNotAllowed, "method_not_allowed")
	})
	return r, nil
}
