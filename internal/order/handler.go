package order

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
)

// Handler serves the cashier's endpoints.
type Handler struct {
	svc    *Service
	logger *slog.Logger
	// secureCookies marks the device cookie Secure. Only development over
	// plain HTTP turns it off.
	secureCookies bool
}

// NewHandler returns the order module's handler.
func NewHandler(svc *Service, logger *slog.Logger, secureCookies bool) *Handler {
	return &Handler{svc: svc, logger: logger, secureCookies: secureCookies}
}

// Routes registers the endpoints on scoped, the group under /outlets/:oid
// that OutletScope guards. Selling needs checkout.create, and an order is
// taken only from one of the outlet's devices too. The orders are for
// whoever sells or voids them; voiding needs order.void.
func (h *Handler) Routes(scoped *gin.RouterGroup) {
	sell := middleware.RequirePermission("checkout.create")
	read := middleware.RequireAnyPermission("checkout.create", "order.void")
	scoped.GET("/pos/catalog", sell, h.Catalog)
	scoped.GET("/pos/device", sell, h.Device)
	scoped.POST("/orders", sell, h.Create)
	scoped.GET("/orders", read, h.List)
	scoped.GET("/orders/:orid", read, h.Get)
	scoped.POST("/orders/:orid/void", middleware.RequirePermission("order.void"), h.Void)
}

// Catalog handles GET /outlets/{oid}/pos/catalog.
func (h *Handler) Catalog(c *gin.Context) {
	cat, err := h.svc.Catalog(c.Request.Context(), c.Param("oid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toCatalogResponse(cat))
}

// Device handles GET /outlets/{oid}/pos/device, which the cashier sends as
// it starts. A browser's device cookie starts its 400 days again each time,
// so a device in use keeps it.
func (h *Handler) Device(c *gin.Context) {
	token := authn.DeviceToken(c.Request)
	d, err := h.svc.PosDevice(c.Request.Context(), c.Param("oid"), token)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if d.Device.Platform == device.PlatformWeb {
		http.SetCookie(c.Writer, authn.NewDeviceCookie(token, h.secureCookies))
	}
	c.JSON(http.StatusOK, toPosDeviceResponse(d))
}

// Create handles POST /outlets/{oid}/orders: 201 with the order taken, or
// 200 with it as it was for a resend.
func (h *Handler) Create(c *gin.Context) {
	var req createOrderRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	o, created, err := h.svc.Create(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"),
		authn.DeviceToken(c.Request), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, toOrderResponse(o))
}

// List handles GET /outlets/{oid}/orders?q=&page=&page_size=: today's.
func (h *Handler) List(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	items, total, err := h.svc.List(c.Request.Context(), c.Param("oid"), c.Query("q"), page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]orderSummaryResponse, len(items))
	for i, item := range items {
		out[i] = toOrderSummaryResponse(item)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// Get handles GET /outlets/{oid}/orders/{orid}.
func (h *Handler) Get(c *gin.Context) {
	o, err := h.svc.Order(c.Request.Context(), c.Param("oid"), c.Param("orid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(o))
}

// Void handles POST /outlets/{oid}/orders/{orid}/void.
func (h *Handler) Void(c *gin.Context) {
	var req voidRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	o, err := h.svc.Void(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("orid"), req.Reason)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(o))
}

// principal is the caller, whom Authenticate has put on every route here.
func principal(c *gin.Context) authn.Principal {
	p, _ := authn.PrincipalFrom(c.Request.Context())
	return p
}
