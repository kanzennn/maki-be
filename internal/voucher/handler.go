package voucher

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
)

// Handler serves the voucher endpoints.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandler returns the voucher module's handler.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// Routes registers the Owner's endpoints on owner, which RequireOwner
// guards, and an outlet's on scoped, under /outlets/:oid: its Manager's
// list, and the cashier's check, which needs checkout.create and one of the
// outlet's devices, so codes can't be tried from anywhere.
func (h *Handler) Routes(owner, scoped *gin.RouterGroup) {
	owner.GET("/vouchers", h.List)
	owner.POST("/vouchers", h.Create)
	owner.GET("/vouchers/:vid", h.Get)
	owner.PUT("/vouchers/:vid", h.Update)
	owner.PATCH("/vouchers/:vid/status", h.SetStatus)
	owner.DELETE("/vouchers/:vid", h.Delete)
	owner.GET("/vouchers/:vid/redemptions", h.Redemptions)

	scoped.GET("/vouchers", middleware.RequireManagerOrOwner(), h.ListForOutlet)
	scoped.POST("/vouchers/validate", middleware.RequirePermission("checkout.create"), h.Validate)
}

// List handles GET /vouchers?q=&status=&page=&page_size=.
func (h *Handler) List(c *gin.Context) {
	f, page, err := bindList(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	items, total, err := h.svc.List(c.Request.Context(), f, page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewList(toResponses(items), page, total))
}

// ListForOutlet handles GET /outlets/{oid}/vouchers?q=&status=&page=&page_size=.
func (h *Handler) ListForOutlet(c *gin.Context) {
	f, page, err := bindList(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	items, total, err := h.svc.ListForOutlet(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewList(toResponses(items), page, total))
}

// bindList reads a voucher list's query.
func bindList(c *gin.Context) (Filter, httpx.Page, error) {
	page, err := httpx.BindPage(c)
	if err != nil {
		return Filter{}, httpx.Page{}, err
	}
	switch status := c.Query("status"); status {
	case "", StatusActive, StatusScheduled, StatusUsedUp, StatusExpired, StatusInactive:
		return Filter{Search: c.Query("q"), Status: status}, page, nil
	}
	return Filter{}, httpx.Page{}, httpx.Invalid(map[string]string{"status": "oneof"})
}

func toResponses(items []Voucher) []voucherResponse {
	out := make([]voucherResponse, len(items))
	for i, v := range items {
		out[i] = toResponse(v)
	}
	return out
}

// Get handles GET /vouchers/{vid}.
func (h *Handler) Get(c *gin.Context) {
	v, err := h.svc.Get(c.Request.Context(), c.Param("vid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toResponse(v))
}

// Create handles POST /vouchers.
func (h *Handler) Create(c *gin.Context) {
	var req voucherRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	v, err := h.svc.Create(c.Request.Context(), principal(c), c.ClientIP(), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toResponse(v))
}

// Update handles PUT /vouchers/{vid}.
func (h *Handler) Update(c *gin.Context) {
	var req voucherRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	v, err := h.svc.Update(c.Request.Context(), principal(c), c.ClientIP(), c.Param("vid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toResponse(v))
}

// SetStatus handles PATCH /vouchers/{vid}/status.
func (h *Handler) SetStatus(c *gin.Context) {
	var req statusRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	v, err := h.svc.SetActive(c.Request.Context(), principal(c), c.ClientIP(), c.Param("vid"), *req.IsActive)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toResponse(v))
}

// Delete handles DELETE /vouchers/{vid}.
func (h *Handler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), principal(c), c.ClientIP(), c.Param("vid")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Redemptions handles GET /vouchers/{vid}/redemptions?page=&page_size=.
func (h *Handler) Redemptions(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	uses, total, err := h.svc.Uses(c.Request.Context(), c.Param("vid"), page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]redemptionResponse, len(uses))
	for i, u := range uses {
		out[i] = toRedemptionResponse(u)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// Validate handles POST /outlets/{oid}/vouchers/validate.
func (h *Handler) Validate(c *gin.Context) {
	var req validateRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	q, err := h.svc.Check(c.Request.Context(), c.Param("oid"), authn.DeviceToken(c.Request), req.Code, req.Subtotal)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toValidateResponse(q))
}

// principal is the caller, whom Authenticate has put on every route here.
func principal(c *gin.Context) authn.Principal {
	p, _ := authn.PrincipalFrom(c.Request.Context())
	return p
}
