package outlet

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Handler serves the outlet endpoints, which only the Owner reaches.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandler returns the outlet endpoints' handler.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// Routes registers the endpoints on owner, a group that RequireOwner guards.
func (h *Handler) Routes(owner *gin.RouterGroup) {
	owner.GET("/outlets", h.List)
	owner.POST("/outlets", h.Create)
	owner.GET("/outlets/:oid", h.Get)
	owner.PUT("/outlets/:oid", h.Update)
	owner.PATCH("/outlets/:oid/status", h.SetStatus)
	owner.DELETE("/outlets/:oid", h.Delete)
}

// List handles GET /outlets?q=&status=&page=&page_size=.
func (h *Handler) List(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	status := c.Query("status")
	if status != "" && status != "active" && status != "inactive" {
		httpx.Fail(c, h.logger, httpx.Invalid(map[string]string{"status": "oneof"}))
		return
	}
	items, total, err := h.svc.List(c.Request.Context(), Filter{Search: c.Query("q"), Status: status}, page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]outletResponse, len(items))
	for i, d := range items {
		out[i] = toResponse(d)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// Get handles GET /outlets/{oid}.
func (h *Handler) Get(c *gin.Context) {
	d, err := h.svc.Detail(c.Request.Context(), c.Param("oid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toResponse(d))
}

// Create handles POST /outlets.
func (h *Handler) Create(c *gin.Context) {
	var req outletRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	d, err := h.svc.Create(c.Request.Context(), principal(c), c.ClientIP(), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toResponse(d))
}

// Update handles PUT /outlets/{oid}.
func (h *Handler) Update(c *gin.Context) {
	var req outletRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	d, err := h.svc.Update(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toResponse(d))
}

// SetStatus handles PATCH /outlets/{oid}/status.
func (h *Handler) SetStatus(c *gin.Context) {
	var req statusRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	d, err := h.svc.SetStatus(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.Status == "active")
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toResponse(d))
}

// Delete handles DELETE /outlets/{oid}.
func (h *Handler) Delete(c *gin.Context) {
	var req deleteRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.Delete(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.ConfirmName); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// principal is the Owner, whom RequireOwner has put on every route here.
func principal(c *gin.Context) authn.Principal {
	p, _ := authn.PrincipalFrom(c.Request.Context())
	return p
}
