package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// AreaController serves an outlet's dining areas.
type AreaController struct {
	areas *services.AreaService
}

// NewAreaController returns the areas' controller.
func NewAreaController(areas *services.AreaService) *AreaController {
	return &AreaController{areas: areas}
}

// ListAreas handles GET /outlets/{oid}/areas?page=&page_size=.
func (ctl *AreaController) ListAreas(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	areas, total, err := ctl.areas.ListAreas(c.Request.Context(), c.Param("oid"), page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.AreaResponse, len(areas))
	for i, a := range areas {
		out[i] = models.NewAreaResponse(a)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// GetArea handles GET /outlets/{oid}/areas/{aid}.
func (ctl *AreaController) GetArea(c *gin.Context) {
	a, err := ctl.areas.Area(c.Request.Context(), c.Param("oid"), c.Param("aid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewAreaResponse(a))
}

// CreateArea handles POST /outlets/{oid}/areas.
func (ctl *AreaController) CreateArea(c *gin.Context) {
	var req validations.AreaRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	a, err := ctl.areas.CreateArea(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), models.AreaInput(req))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewAreaResponse(a))
}

// UpdateArea handles PUT /outlets/{oid}/areas/{aid}.
func (ctl *AreaController) UpdateArea(c *gin.Context) {
	var req validations.AreaRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	a, err := ctl.areas.UpdateArea(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("aid"), models.AreaInput(req))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewAreaResponse(a))
}

// ReorderAreas handles PUT /outlets/{oid}/areas/order.
func (ctl *AreaController) ReorderAreas(c *gin.Context) {
	var req validations.ReorderAreasRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.areas.ReorderAreas(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.AreaIDs); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteArea handles DELETE /outlets/{oid}/areas/{aid}.
func (ctl *AreaController) DeleteArea(c *gin.Context) {
	if err := ctl.areas.DeleteArea(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("aid")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
