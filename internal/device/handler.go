package device

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
)

// Handler serves an outlet's devices.
type Handler struct {
	svc    *Service
	logger *slog.Logger
	// secureCookies marks the device cookie Secure. Only development over
	// plain HTTP turns it off.
	secureCookies bool
}

// NewHandler returns the device module's handler.
func NewHandler(svc *Service, logger *slog.Logger, secureCookies bool) *Handler {
	return &Handler{svc: svc, logger: logger, secureCookies: secureCookies}
}

// Routes registers the endpoints on scoped, the group under /outlets/:oid
// that OutletScope guards. The fixed roles open them, not a permission: the
// outlet's Manager activates a device, and the Owner, who looks into every
// outlet, may see and revoke them too.
func (h *Handler) Routes(scoped *gin.RouterGroup) {
	scoped.POST("/devices", middleware.RequireManager(), h.Activate)
	scoped.GET("/devices", middleware.RequireManagerOrOwner(), h.List)
	scoped.DELETE("/devices/:did", middleware.RequireManagerOrOwner(), h.Revoke)
}

// Activate handles POST /outlets/{oid}/devices, sent from the device itself.
// A browser gets its token as a cookie; the Android app, in the body.
func (h *Handler) Activate(c *gin.Context) {
	var req activateRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	d, token, err := h.svc.Activate(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"),
		authn.DeviceToken(c.Request), req.Name, req.Platform)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	resp := activateResponse{Device: toDeviceResponse(d)}
	if d.Platform == PlatformWeb {
		http.SetCookie(c.Writer, authn.NewDeviceCookie(token, h.secureCookies))
	} else {
		resp.Token = &token
	}
	c.JSON(http.StatusCreated, resp)
}

// List handles GET /outlets/{oid}/devices?page=&page_size=.
func (h *Handler) List(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	items, total, err := h.svc.List(c.Request.Context(), c.Param("oid"), page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]deviceResponse, len(items))
	for i, item := range items {
		out[i] = toDeviceResponse(item)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// Revoke handles DELETE /outlets/{oid}/devices/{did}.
func (h *Handler) Revoke(c *gin.Context) {
	if err := h.svc.Revoke(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("did")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// principal is the caller, whom Authenticate has put on every route here.
func principal(c *gin.Context) authn.Principal {
	p, _ := authn.PrincipalFrom(c.Request.Context())
	return p
}
