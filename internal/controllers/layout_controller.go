package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/validations"
)

// LayoutController serves the canvases of an outlet's areas.
type LayoutController struct {
	layouts *services.LayoutService
}

// NewLayoutController returns the layouts' controller.
func NewLayoutController(layouts *services.LayoutService) *LayoutController {
	return &LayoutController{layouts: layouts}
}

// GetLayout handles GET /outlets/{oid}/areas/{aid}/layout.
func (ctl *LayoutController) GetLayout(c *gin.Context) {
	l, err := ctl.layouts.Layout(c.Request.Context(), c.Param("oid"), c.Param("aid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewLayoutResponse(l))
}

// SaveLayout handles PUT /outlets/{oid}/areas/{aid}/layout.
func (ctl *LayoutController) SaveLayout(c *gin.Context) {
	var req validations.LayoutRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	l, err := ctl.layouts.SaveLayout(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("aid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewLayoutResponse(l))
}
