package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// ScheduleController serves the schedules that take an outlet's tables out
// of service.
type ScheduleController struct {
	schedules *services.ScheduleService
}

// NewScheduleController returns the schedules' controller.
func NewScheduleController(schedules *services.ScheduleService) *ScheduleController {
	return &ScheduleController{schedules: schedules}
}

// ListSchedules handles GET /outlets/{oid}/table-schedules?status=&page=&page_size=.
func (ctl *ScheduleController) ListSchedules(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	f, err := validations.CheckScheduleFilter(models.ScheduleFilter{Status: c.Query("status")})
	if err != nil {
		_ = c.Error(err)
		return
	}
	schedules, total, err := ctl.schedules.ListSchedules(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.ScheduleResponse, len(schedules))
	for i, s := range schedules {
		out[i] = models.NewScheduleResponse(s)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// GetSchedule handles GET /outlets/{oid}/table-schedules/{sid}.
func (ctl *ScheduleController) GetSchedule(c *gin.Context) {
	s, err := ctl.schedules.Schedule(c.Request.Context(), c.Param("oid"), c.Param("sid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewScheduleResponse(s))
}

// CreateSchedule handles POST /outlets/{oid}/table-schedules.
func (ctl *ScheduleController) CreateSchedule(c *gin.Context) {
	var req validations.ScheduleRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	s, err := ctl.schedules.CreateSchedule(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewScheduleResponse(s))
}

// UpdateSchedule handles PUT /outlets/{oid}/table-schedules/{sid}.
func (ctl *ScheduleController) UpdateSchedule(c *gin.Context) {
	var req validations.ScheduleRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	s, err := ctl.schedules.UpdateSchedule(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("sid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewScheduleResponse(s))
}

// CancelSchedule handles DELETE /outlets/{oid}/table-schedules/{sid}: the
// schedule is cancelled, not deleted, and stays as a record.
func (ctl *ScheduleController) CancelSchedule(c *gin.Context) {
	if err := ctl.schedules.CancelSchedule(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("sid")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}
