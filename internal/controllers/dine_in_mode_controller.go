package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/validations"
)

// DineInModeController serves how an outlet seats its dine-in customers.
type DineInModeController struct {
	modes *services.DineInModeService
}

// NewDineInModeController returns the dine-in mode's controller.
func NewDineInModeController(modes *services.DineInModeService) *DineInModeController {
	return &DineInModeController{modes: modes}
}

// GetDineInMode handles GET /outlets/{oid}/dine-in-mode.
func (ctl *DineInModeController) GetDineInMode(c *gin.Context) {
	m, err := ctl.modes.DineInMode(c.Request.Context(), c.Param("oid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.DineInModeResponse{Mode: string(m)})
}

// SetDineInMode handles PUT /outlets/{oid}/dine-in-mode.
func (ctl *DineInModeController) SetDineInMode(c *gin.Context) {
	var req validations.DineInModeRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	m, err := ctl.modes.SetDineInMode(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), models.DineInMode(req.Mode))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.DineInModeResponse{Mode: string(m)})
}
