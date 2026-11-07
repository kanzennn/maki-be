package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/validations"
)

// DashboardController serves the dashboards.
type DashboardController struct {
	dashboards *services.DashboardService
}

// NewDashboardController returns the dashboards' controller.
func NewDashboardController(dashboards *services.DashboardService) *DashboardController {
	return &DashboardController{dashboards: dashboards}
}

// Owner handles GET /owner/dashboard?from=&to=&outlet_ids=.
func (ctl *DashboardController) Owner(c *gin.Context) {
	q, err := validations.BindDashboardQuery(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	d, err := ctl.dashboards.Owner(c.Request.Context(), q)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, d)
}

// Outlet handles GET /outlets/{oid}/dashboard?from=&to=.
func (ctl *DashboardController) Outlet(c *gin.Context) {
	q, err := validations.BindDashboardQuery(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	d, err := ctl.dashboards.Outlet(c.Request.Context(), c.Param("oid"), q)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, d)
}
