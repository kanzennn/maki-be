package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/cookie"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// VoucherController serves the Owner's vouchers, an outlet Manager's list of
// them, and the cashier's check of a code.
type VoucherController struct {
	vouchers *services.VoucherService
}

// NewVoucherController returns the vouchers' controller.
func NewVoucherController(vouchers *services.VoucherService) *VoucherController {
	return &VoucherController{vouchers: vouchers}
}

// List handles GET /vouchers?q=&status=&page=&page_size=.
func (ctl *VoucherController) List(c *gin.Context) {
	f, page, err := bindVoucherList(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	items, total, err := ctl.vouchers.List(c.Request.Context(), f, page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, response.NewList(models.NewVoucherResponses(items), page, total))
}

// ListForOutlet handles GET /outlets/{oid}/vouchers?q=&status=&page=&page_size=.
func (ctl *VoucherController) ListForOutlet(c *gin.Context) {
	f, page, err := bindVoucherList(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	items, total, err := ctl.vouchers.ListForOutlet(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, response.NewList(models.NewVoucherResponses(items), page, total))
}

// bindVoucherList reads a voucher list's query: its page, then its filters.
func bindVoucherList(c *gin.Context) (models.VoucherFilter, pagination.Page, error) {
	page, err := validations.BindPage(c)
	if err != nil {
		return models.VoucherFilter{}, pagination.Page{}, err
	}
	f, err := validations.CheckVoucherFilter(models.VoucherFilter{Search: c.Query("q"), Status: c.Query("status")})
	if err != nil {
		return models.VoucherFilter{}, pagination.Page{}, err
	}
	return f, page, nil
}

// Get handles GET /vouchers/{vid}.
func (ctl *VoucherController) Get(c *gin.Context) {
	v, err := ctl.vouchers.Get(c.Request.Context(), c.Param("vid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewVoucherResponse(v))
}

// Create handles POST /vouchers.
func (ctl *VoucherController) Create(c *gin.Context) {
	var req validations.VoucherRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	v, err := ctl.vouchers.Create(c.Request.Context(), principal(c), c.ClientIP(), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewVoucherResponse(v))
}

// Update handles PUT /vouchers/{vid}.
func (ctl *VoucherController) Update(c *gin.Context) {
	var req validations.VoucherRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	v, err := ctl.vouchers.Update(c.Request.Context(), principal(c), c.ClientIP(), c.Param("vid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewVoucherResponse(v))
}

// SetStatus handles PATCH /vouchers/{vid}/status.
func (ctl *VoucherController) SetStatus(c *gin.Context) {
	var req validations.ActiveRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	v, err := ctl.vouchers.SetActive(c.Request.Context(), principal(c), c.ClientIP(), c.Param("vid"), *req.IsActive)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewVoucherResponse(v))
}

// Delete handles DELETE /vouchers/{vid}.
func (ctl *VoucherController) Delete(c *gin.Context) {
	if err := ctl.vouchers.Delete(c.Request.Context(), principal(c), c.ClientIP(), c.Param("vid")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Redemptions handles GET /vouchers/{vid}/redemptions?page=&page_size=.
func (ctl *VoucherController) Redemptions(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	uses, total, err := ctl.vouchers.Uses(c.Request.Context(), c.Param("vid"), page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.VoucherUseResponse, len(uses))
	for i, u := range uses {
		out[i] = models.NewVoucherUseResponse(u)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// Validate handles POST /outlets/{oid}/vouchers/validate.
func (ctl *VoucherController) Validate(c *gin.Context) {
	var req validations.CheckVoucherRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	q, err := ctl.vouchers.Check(c.Request.Context(), c.Param("oid"), cookie.DeviceToken(c.Request), req.Code, req.Subtotal)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewVoucherQuoteResponse(q))
}
