package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// MenuController serves an outlet's menus.
type MenuController struct {
	menus *services.MenuService
}

// NewMenuController returns the menus' controller.
func NewMenuController(menus *services.MenuService) *MenuController {
	return &MenuController{menus: menus}
}

// ListMenus handles GET /outlets/{oid}/menus?q=&category_id=&status=&stock=&page=&page_size=.
func (ctl *MenuController) ListMenus(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	f, err := validations.CheckMenuFilter(models.MenuFilter{
		Search: c.Query("q"), CategoryID: c.Query("category_id"), Status: c.Query("status"), Stock: c.Query("stock"),
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	items, total, err := ctl.menus.ListMenus(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.MenuResponse, len(items))
	for i, item := range items {
		out[i] = models.NewMenuResponse(item)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// GetMenu handles GET /outlets/{oid}/menus/{mid}.
func (ctl *MenuController) GetMenu(c *gin.Context) {
	m, err := ctl.menus.Menu(c.Request.Context(), c.Param("oid"), c.Param("mid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewMenuResponse(m))
}

// CreateMenu handles POST /outlets/{oid}/menus.
func (ctl *MenuController) CreateMenu(c *gin.Context) {
	var req validations.MenuRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	m, err := ctl.menus.CreateMenu(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewMenuResponse(m))
}

// UpdateMenu handles PUT /outlets/{oid}/menus/{mid}.
func (ctl *MenuController) UpdateMenu(c *gin.Context) {
	var req validations.MenuRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	m, err := ctl.menus.UpdateMenu(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewMenuResponse(m))
}

// SetMenuStatus handles PATCH /outlets/{oid}/menus/{mid}/status.
func (ctl *MenuController) SetMenuStatus(c *gin.Context) {
	var req validations.ActiveRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	m, err := ctl.menus.SetMenuActive(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid"), *req.IsActive)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewMenuResponse(m))
}

// DeleteMenu handles DELETE /outlets/{oid}/menus/{mid}.
func (ctl *MenuController) DeleteMenu(c *gin.Context) {
	if err := ctl.menus.DeleteMenu(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
