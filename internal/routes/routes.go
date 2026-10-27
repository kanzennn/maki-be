// Package routes builds the router: the middleware every request goes
// through, the probes, and the /api/v1 groups each resource's routes
// register on (<resource>_routes.go), each route naming the middleware it
// needs and the controller method that serves it.
package routes

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
	"github.com/kervzent/maki-be/internal/utils/jwt"
	"github.com/kervzent/maki-be/internal/utils/response"
)

// maxBodyBytes caps JSON request bodies. File uploads go straight to object
// storage, so no legitimate request comes close.
const maxBodyBytes = 1 << 20

// Deps are what the routes need, built in cmd/api.
type Deps struct {
	Pinger     controllers.Pinger
	Tokens     *jwt.Tokens
	Principals middlewares.PrincipalLoader
	Auth       *controllers.AuthController
	// OutletFinder is what OutletScope asks whether an outlet exists.
	OutletFinder middlewares.OutletFinder
	Outlets      *controllers.OutletController
	Roles        *controllers.RoleController
	Staff        *controllers.StaffController
	Categories   *controllers.CategoryController
	Menus        *controllers.MenuController
	Stock        *controllers.StockController
	Devices      *controllers.DeviceController
	Tables       TableControllers
	Vouchers     *controllers.VoucherController
	Orders       OrderControllers
}

// New wires the middleware chain in the order the architecture standard
// fixes, then registers every resource's routes.
func New(cfg config.Config, logger *slog.Logger, deps Deps) (*gin.Engine, error) {
	r := gin.New()
	r.HandleMethodNotAllowed = true

	// Without this Gin trusts X-Forwarded-For from anyone, so ClientIP (and
	// rate limiting keyed on it) could be spoofed.
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		return nil, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}

	r.Use(
		middlewares.RequestID(),
		middlewares.Logger(logger),
		middlewares.Recovery(logger),
		middlewares.ErrorHandler(logger),
		middlewares.SecurityHeaders(),
		middlewares.BodyLimit(maxBodyBytes),
		middlewares.CORS(cfg.CORSAllowedOrigins),
	)

	registerHealthRoutes(r, controllers.NewHealthController(deps.Pinger, logger))

	// Groups under /api/v1: public; authed, which needs a valid access
	// token; protected, which also needs no pending password change; owner,
	// only for the Owner; and scoped, an outlet's own routes under
	// /outlets/:oid, where each route names its permission. Routes register
	// on protected unless an endpoint must stay reachable before the
	// password is changed.
	public := r.Group("/api/v1")
	authed := public.Group("", middlewares.Authenticate(deps.Tokens, deps.Principals, logger))
	protected := authed.Group("", middlewares.RequirePasswordChanged())
	owner := protected.Group("", middlewares.RequireOwner())
	scoped := protected.Group("/outlets/:oid", middlewares.OutletScope(deps.OutletFinder, logger))
	RegisterAuthRoutes(public, authed, protected, deps.Auth)
	RegisterOutletRoutes(owner, deps.Outlets)
	RegisterRoleRoutes(protected, scoped, deps.Roles)
	RegisterStaffRoutes(scoped, deps.Staff)
	RegisterMenuRoutes(scoped, deps.Categories, deps.Menus, deps.Stock)
	RegisterDeviceRoutes(scoped, deps.Devices)
	RegisterTableRoutes(scoped, deps.Tables)
	RegisterVoucherRoutes(owner, scoped, deps.Vouchers)
	RegisterOrderRoutes(scoped, deps.Orders)

	r.NoRoute(func(c *gin.Context) {
		response.WriteError(c, http.StatusNotFound, "not_found")
	})
	r.NoMethod(func(c *gin.Context) {
		response.WriteError(c, http.StatusMethodNotAllowed, "method_not_allowed")
	})
	return r, nil
}
