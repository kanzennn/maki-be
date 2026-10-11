package menu

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
)

// Handler serves an outlet's categories, menus and stock.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandler returns the menu module's handler.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// Routes registers the endpoints on scoped, the group under /outlets/:oid
// that OutletScope guards. Each route names the permission it needs.
func (h *Handler) Routes(scoped *gin.RouterGroup) {
	view, manage := middleware.RequirePermission("menu.view"), middleware.RequirePermission("menu.manage")
	// The alert is a field of the menu form too, so either may set it.
	adjust, alert := middleware.RequirePermission("stock.adjust"), middleware.RequireAnyPermission("stock.adjust", "menu.manage")
	scoped.GET("/categories", view, h.ListCategories)
	scoped.POST("/categories", manage, h.CreateCategory)
	scoped.PUT("/categories/order", manage, h.ReorderCategories)
	scoped.GET("/categories/:cid", view, h.GetCategory)
	scoped.PUT("/categories/:cid", manage, h.UpdateCategory)
	scoped.PATCH("/categories/:cid/status", manage, h.SetCategoryStatus)
	scoped.DELETE("/categories/:cid", manage, h.DeleteCategory)

	scoped.GET("/menus", view, h.ListMenus)
	scoped.POST("/menus", manage, h.CreateMenu)
	scoped.GET("/menus/:mid", view, h.GetMenu)
	scoped.PUT("/menus/:mid", manage, h.UpdateMenu)
	scoped.PATCH("/menus/:mid/status", manage, h.SetMenuStatus)
	scoped.DELETE("/menus/:mid", manage, h.DeleteMenu)

	scoped.POST("/menus/:mid/stock-adjustments", adjust, h.AdjustStock)
	scoped.PUT("/menus/:mid/stock-alert", alert, h.SetStockAlert)
	scoped.GET("/menus/:mid/stock-movements", view, h.ListStockMovements)
}

// ListCategories handles GET /outlets/{oid}/categories?page=&page_size=.
func (h *Handler) ListCategories(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	items, total, err := h.svc.ListCategories(c.Request.Context(), c.Param("oid"), page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]categoryResponse, len(items))
	for i, item := range items {
		out[i] = toCategoryResponse(item)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// GetCategory handles GET /outlets/{oid}/categories/{cid}.
func (h *Handler) GetCategory(c *gin.Context) {
	cat, err := h.svc.Category(c.Request.Context(), c.Param("oid"), c.Param("cid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toCategoryResponse(cat))
}

// CreateCategory handles POST /outlets/{oid}/categories.
func (h *Handler) CreateCategory(c *gin.Context) {
	var req categoryRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	cat, err := h.svc.CreateCategory(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toCategoryResponse(cat))
}

// UpdateCategory handles PUT /outlets/{oid}/categories/{cid}.
func (h *Handler) UpdateCategory(c *gin.Context) {
	var req categoryRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	cat, err := h.svc.UpdateCategory(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("cid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toCategoryResponse(cat))
}

// SetCategoryStatus handles PATCH /outlets/{oid}/categories/{cid}/status.
func (h *Handler) SetCategoryStatus(c *gin.Context) {
	var req statusRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	cat, err := h.svc.SetCategoryActive(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("cid"), *req.IsActive)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toCategoryResponse(cat))
}

// ReorderCategories handles PUT /outlets/{oid}/categories/order.
func (h *Handler) ReorderCategories(c *gin.Context) {
	var req orderRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.ReorderCategories(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.CategoryIDs); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteCategory handles DELETE /outlets/{oid}/categories/{cid}.
func (h *Handler) DeleteCategory(c *gin.Context) {
	if err := h.svc.DeleteCategory(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("cid")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListMenus handles GET /outlets/{oid}/menus?q=&category_id=&status=&stock=&page=&page_size=.
func (h *Handler) ListMenus(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	f := MenuFilter{Search: c.Query("q"), CategoryID: c.Query("category_id"), Status: c.Query("status"), Stock: c.Query("stock")}
	fields := map[string]string{}
	if f.Status != "" && f.Status != "active" && f.Status != "inactive" {
		fields["status"] = "oneof"
	}
	switch f.Stock {
	case "", StockOK, StockLow, StockOut, StockUntracked:
	default:
		fields["stock"] = "oneof"
	}
	if len(fields) > 0 {
		httpx.Fail(c, h.logger, httpx.Invalid(fields))
		return
	}
	items, total, err := h.svc.ListMenus(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]menuResponse, len(items))
	for i, item := range items {
		out[i] = toMenuResponse(item)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// GetMenu handles GET /outlets/{oid}/menus/{mid}.
func (h *Handler) GetMenu(c *gin.Context) {
	m, err := h.svc.Menu(c.Request.Context(), c.Param("oid"), c.Param("mid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toMenuResponse(m))
}

// CreateMenu handles POST /outlets/{oid}/menus.
func (h *Handler) CreateMenu(c *gin.Context) {
	var req menuRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	m, err := h.svc.CreateMenu(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toMenuResponse(m))
}

// UpdateMenu handles PUT /outlets/{oid}/menus/{mid}.
func (h *Handler) UpdateMenu(c *gin.Context) {
	var req menuRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	m, err := h.svc.UpdateMenu(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toMenuResponse(m))
}

// SetMenuStatus handles PATCH /outlets/{oid}/menus/{mid}/status.
func (h *Handler) SetMenuStatus(c *gin.Context) {
	var req statusRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	m, err := h.svc.SetMenuActive(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid"), *req.IsActive)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toMenuResponse(m))
}

// DeleteMenu handles DELETE /outlets/{oid}/menus/{mid}.
func (h *Handler) DeleteMenu(c *gin.Context) {
	if err := h.svc.DeleteMenu(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AdjustStock handles POST /outlets/{oid}/menus/{mid}/stock-adjustments. It
// answers with the menu as the adjustment left it.
func (h *Handler) AdjustStock(c *gin.Context) {
	var req adjustmentRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	m, err := h.svc.AdjustStock(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toMenuResponse(m))
}

// SetStockAlert handles PUT /outlets/{oid}/menus/{mid}/stock-alert.
func (h *Handler) SetStockAlert(c *gin.Context) {
	var req stockAlertRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	m, err := h.svc.SetStockAlert(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid"), req.StockAlert)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toMenuResponse(m))
}

// ListStockMovements handles GET /outlets/{oid}/menus/{mid}/stock-movements?page=&page_size=.
func (h *Handler) ListStockMovements(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	items, total, err := h.svc.StockMovements(c.Request.Context(), c.Param("oid"), c.Param("mid"), page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]movementResponse, len(items))
	for i, item := range items {
		out[i] = toMovementResponse(item)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// principal is the caller, whom Authenticate has put on every route here.
func principal(c *gin.Context) authn.Principal {
	p, _ := authn.PrincipalFrom(c.Request.Context())
	return p
}
