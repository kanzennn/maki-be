package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// StaffController serves an outlet's Manager and staff accounts.
type StaffController struct {
	staff *services.StaffService
}

// NewStaffController returns the staff accounts' controller.
func NewStaffController(staff *services.StaffService) *StaffController {
	return &StaffController{staff: staff}
}

// ListStaff handles GET /outlets/{oid}/users?q=&status=&role_id=.
func (ctl *StaffController) ListStaff(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	f, err := validations.CheckStaffFilter(models.StaffFilter{Search: c.Query("q"), Status: c.Query("status"), RoleID: c.Query("role_id")})
	if err != nil {
		_ = c.Error(err)
		return
	}
	staff, total, err := ctl.staff.ListStaff(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.StaffResponse, len(staff))
	for i, s := range staff {
		out[i] = models.NewStaffResponse(s)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// GetStaff handles GET /outlets/{oid}/users/{uid}.
func (ctl *StaffController) GetStaff(c *gin.Context) {
	s, err := ctl.staff.Staff(c.Request.Context(), c.Param("oid"), c.Param("uid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewStaffResponse(s))
}

// CreateStaff handles POST /outlets/{oid}/users.
func (ctl *StaffController) CreateStaff(c *gin.Context) {
	var req validations.CreateStaffRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	s, err := ctl.staff.CreateStaff(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), models.StaffInput(req))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewStaffResponse(s))
}

// UpdateStaff handles PUT /outlets/{oid}/users/{uid}.
func (ctl *StaffController) UpdateStaff(c *gin.Context) {
	var req validations.UpdateStaffRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	in := models.StaffInput{Name: req.Name, Email: req.Email, Phone: req.Phone, RoleID: req.RoleID, IsManager: req.IsManager}
	s, err := ctl.staff.UpdateStaff(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("uid"), in)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewStaffResponse(s))
}

// SetStaffStatus handles PATCH /outlets/{oid}/users/{uid}/status.
func (ctl *StaffController) SetStaffStatus(c *gin.Context) {
	var req validations.StaffStatusRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	s, err := ctl.staff.SetStaffStatus(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("uid"), req.Status == "active")
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewStaffResponse(s))
}

// ResetStaffPassword handles POST /outlets/{oid}/users/{uid}/reset-password.
func (ctl *StaffController) ResetStaffPassword(c *gin.Context) {
	var req validations.ResetStaffPasswordRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	if err := ctl.staff.ResetStaffPassword(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("uid"), req.Password); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteStaff handles DELETE /outlets/{oid}/users/{uid}.
func (ctl *StaffController) DeleteStaff(c *gin.Context) {
	if err := ctl.staff.DeleteStaff(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("uid")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
