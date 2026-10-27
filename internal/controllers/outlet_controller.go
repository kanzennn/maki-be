package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// OutletController serves the outlet endpoints, which only the Owner
// reaches.
type OutletController struct {
	outlets *services.OutletService
}

// NewOutletController returns the outlets' controller.
func NewOutletController(outlets *services.OutletService) *OutletController {
	return &OutletController{outlets: outlets}
}

// List handles GET /outlets?q=&status=&page=&page_size=.
func (ctl *OutletController) List(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	f, err := validations.CheckOutletFilter(models.OutletFilter{Search: c.Query("q"), Status: c.Query("status")})
	if err != nil {
		_ = c.Error(err)
		return
	}
	items, total, err := ctl.outlets.List(c.Request.Context(), f, page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.OutletResponse, len(items))
	for i, d := range items {
		out[i] = models.NewOutletResponse(d)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// Get handles GET /outlets/{oid}.
func (ctl *OutletController) Get(c *gin.Context) {
	d, err := ctl.outlets.Detail(c.Request.Context(), c.Param("oid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewOutletResponse(d))
}

// Create handles POST /outlets.
func (ctl *OutletController) Create(c *gin.Context) {
	var req validations.OutletRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	d, err := ctl.outlets.Create(c.Request.Context(), principal(c), c.ClientIP(), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewOutletResponse(d))
}

// Update handles PUT /outlets/{oid}.
func (ctl *OutletController) Update(c *gin.Context) {
	var req validations.OutletRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	d, err := ctl.outlets.Update(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewOutletResponse(d))
}

// SetStatus handles PATCH /outlets/{oid}/status.
func (ctl *OutletController) SetStatus(c *gin.Context) {
	var req validations.OutletStatusRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	d, err := ctl.outlets.SetStatus(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.Status == "active")
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewOutletResponse(d))
}

// Delete handles DELETE /outlets/{oid}.
func (ctl *OutletController) Delete(c *gin.Context) {
	var req validations.DeleteOutletRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.outlets.Delete(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.ConfirmName); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
