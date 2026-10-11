package table

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/middleware"
)

// Handler serves the table endpoints.
type Handler struct {
	svc    *Service
	logger *slog.Logger
}

// NewHandler returns the table module's handler.
func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// Routes registers the endpoints on scoped, under /outlets/:oid:
// table.view reads the areas, the tables, their layouts and the schedules,
// which the Owner does too, and table.manage changes them. The cashier
// (checkout.create) reads the floor and releases tables.
func (h *Handler) Routes(scoped *gin.RouterGroup) {
	view, manage := middleware.RequirePermission("table.view"), middleware.RequirePermission("table.manage")
	sell := middleware.RequirePermission("checkout.create")
	scoped.GET("/areas", view, h.ListAreas)
	scoped.POST("/areas", manage, h.CreateArea)
	scoped.PUT("/areas/order", manage, h.ReorderAreas)
	scoped.GET("/areas/:aid", view, h.GetArea)
	scoped.PUT("/areas/:aid", manage, h.UpdateArea)
	scoped.DELETE("/areas/:aid", manage, h.DeleteArea)
	scoped.GET("/areas/:aid/layout", view, h.GetLayout)
	scoped.PUT("/areas/:aid/layout", manage, h.SaveLayout)

	scoped.GET("/tables", view, h.ListTables)
	scoped.POST("/tables", manage, h.CreateTable)
	scoped.GET("/tables/:tid", view, h.GetTable)
	scoped.PUT("/tables/:tid", manage, h.UpdateTable)
	scoped.PATCH("/tables/:tid/status", manage, h.SetTableStatus)
	scoped.DELETE("/tables/:tid", manage, h.DeleteTable)
	scoped.POST("/tables/:tid/release", sell, h.Release)
	scoped.GET("/pos/tables", sell, h.Floor)

	scoped.GET("/dine-in-mode", view, h.GetDineInMode)
	scoped.PUT("/dine-in-mode", manage, h.SetDineInMode)

	scoped.GET("/table-schedules", view, h.ListSchedules)
	scoped.POST("/table-schedules", manage, h.CreateSchedule)
	scoped.GET("/table-schedules/:sid", view, h.GetSchedule)
	scoped.PUT("/table-schedules/:sid", manage, h.UpdateSchedule)
	scoped.DELETE("/table-schedules/:sid", manage, h.CancelSchedule)
}

// ListAreas handles GET /outlets/{oid}/areas?page=&page_size=.
func (h *Handler) ListAreas(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	areas, total, err := h.svc.ListAreas(c.Request.Context(), c.Param("oid"), page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]areaResponse, len(areas))
	for i, a := range areas {
		out[i] = toAreaResponse(a)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// GetArea handles GET /outlets/{oid}/areas/{aid}.
func (h *Handler) GetArea(c *gin.Context) {
	a, err := h.svc.Area(c.Request.Context(), c.Param("oid"), c.Param("aid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toAreaResponse(a))
}

// CreateArea handles POST /outlets/{oid}/areas.
func (h *Handler) CreateArea(c *gin.Context) {
	var req areaRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	a, err := h.svc.CreateArea(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), AreaInput(req))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toAreaResponse(a))
}

// UpdateArea handles PUT /outlets/{oid}/areas/{aid}.
func (h *Handler) UpdateArea(c *gin.Context) {
	var req areaRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	a, err := h.svc.UpdateArea(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("aid"), AreaInput(req))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toAreaResponse(a))
}

// ReorderAreas handles PUT /outlets/{oid}/areas/order.
func (h *Handler) ReorderAreas(c *gin.Context) {
	var req areaOrderRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	if err := h.svc.ReorderAreas(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.AreaIDs); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteArea handles DELETE /outlets/{oid}/areas/{aid}.
func (h *Handler) DeleteArea(c *gin.Context) {
	if err := h.svc.DeleteArea(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("aid")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetLayout handles GET /outlets/{oid}/areas/{aid}/layout.
func (h *Handler) GetLayout(c *gin.Context) {
	l, err := h.svc.Layout(c.Request.Context(), c.Param("oid"), c.Param("aid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toLayoutResponse(l))
}

// SaveLayout handles PUT /outlets/{oid}/areas/{aid}/layout.
func (h *Handler) SaveLayout(c *gin.Context) {
	var req layoutRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	l, err := h.svc.SaveLayout(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("aid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toLayoutResponse(l))
}

// ListTables handles GET /outlets/{oid}/tables?area_id=&status=&q=&page=&page_size=.
func (h *Handler) ListTables(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	f := Filter{AreaID: c.Query("area_id"), Status: Status(c.Query("status")), Search: c.Query("q")}
	switch f.Status {
	case "", Available, Occupied, Inactive, Scheduled:
	default:
		httpx.Fail(c, h.logger, httpx.Invalid(map[string]string{"status": "oneof"}))
		return
	}
	tables, total, err := h.svc.ListTables(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewList(toTableResponses(tables), page, total))
}

// GetTable handles GET /outlets/{oid}/tables/{tid}.
func (h *Handler) GetTable(c *gin.Context) {
	t, err := h.svc.Table(c.Request.Context(), c.Param("oid"), c.Param("tid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toTableResponse(t))
}

// CreateTable handles POST /outlets/{oid}/tables.
func (h *Handler) CreateTable(c *gin.Context) {
	var req tableRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	t, err := h.svc.CreateTable(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toTableResponse(t))
}

// UpdateTable handles PUT /outlets/{oid}/tables/{tid}.
func (h *Handler) UpdateTable(c *gin.Context) {
	var req tableRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	t, err := h.svc.UpdateTable(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("tid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toTableResponse(t))
}

// SetTableStatus handles PATCH /outlets/{oid}/tables/{tid}/status.
func (h *Handler) SetTableStatus(c *gin.Context) {
	var req statusRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	t, err := h.svc.SetTableActive(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("tid"), *req.IsActive)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toTableResponse(t))
}

// DeleteTable handles DELETE /outlets/{oid}/tables/{tid}.
func (h *Handler) DeleteTable(c *gin.Context) {
	if err := h.svc.DeleteTable(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("tid")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// principal is the caller, whom Authenticate has put on every route here.
func principal(c *gin.Context) authn.Principal {
	p, _ := authn.PrincipalFrom(c.Request.Context())
	return p
}

// ListSchedules handles GET /outlets/{oid}/table-schedules?status=&page=&page_size=.
func (h *Handler) ListSchedules(c *gin.Context) {
	page, err := httpx.BindPage(c)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	f := ScheduleFilter{Status: c.Query("status")}
	switch f.Status {
	case "", ScheduleUpcoming, ScheduleOngoing, ScheduleFinished, ScheduleCancelled:
	default:
		httpx.Fail(c, h.logger, httpx.Invalid(map[string]string{"status": "oneof"}))
		return
	}
	schedules, total, err := h.svc.ListSchedules(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	out := make([]scheduleResponse, len(schedules))
	for i, s := range schedules {
		out[i] = toScheduleResponse(s)
	}
	c.JSON(http.StatusOK, httpx.NewList(out, page, total))
}

// GetSchedule handles GET /outlets/{oid}/table-schedules/{sid}.
func (h *Handler) GetSchedule(c *gin.Context) {
	s, err := h.svc.Schedule(c.Request.Context(), c.Param("oid"), c.Param("sid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toScheduleResponse(s))
}

// CreateSchedule handles POST /outlets/{oid}/table-schedules.
func (h *Handler) CreateSchedule(c *gin.Context) {
	var req scheduleRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	s, err := h.svc.CreateSchedule(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusCreated, toScheduleResponse(s))
}

// UpdateSchedule handles PUT /outlets/{oid}/table-schedules/{sid}.
func (h *Handler) UpdateSchedule(c *gin.Context) {
	var req scheduleRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	s, err := h.svc.UpdateSchedule(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("sid"), req.input())
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toScheduleResponse(s))
}

// CancelSchedule handles DELETE /outlets/{oid}/table-schedules/{sid}: the
// schedule is cancelled, not deleted, and stays as a record.
func (h *Handler) CancelSchedule(c *gin.Context) {
	if err := h.svc.CancelSchedule(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("sid")); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Floor handles GET /outlets/{oid}/pos/tables: the cashier's floor.
func (h *Handler) Floor(c *gin.Context) {
	f, err := h.svc.Floor(c.Request.Context(), c.Param("oid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toFloorResponse(f))
}

// GetDineInMode handles GET /outlets/{oid}/dine-in-mode.
func (h *Handler) GetDineInMode(c *gin.Context) {
	m, err := h.svc.DineInMode(c.Request.Context(), c.Param("oid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, dineInModeResponse{Mode: string(m)})
}

// SetDineInMode handles PUT /outlets/{oid}/dine-in-mode.
func (h *Handler) SetDineInMode(c *gin.Context) {
	var req dineInModeRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	m, err := h.svc.SetDineInMode(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), Mode(req.Mode))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, dineInModeResponse{Mode: string(m)})
}

// Release handles POST /outlets/{oid}/tables/{tid}/release.
func (h *Handler) Release(c *gin.Context) {
	t, err := h.svc.Release(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("tid"))
	if err != nil {
		httpx.Fail(c, h.logger, err)
		return
	}
	c.JSON(http.StatusOK, toTableResponse(t))
}
