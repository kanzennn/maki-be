package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// TableController serves an outlet's tables, and the cashier's floor.
type TableController struct {
	tables *services.TableService
}

// NewTableController returns the tables' controller.
func NewTableController(tables *services.TableService) *TableController {
	return &TableController{tables: tables}
}

// ListTables handles GET /outlets/{oid}/tables?area_id=&status=&q=&page=&page_size=.
func (ctl *TableController) ListTables(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	f, err := validations.CheckTableFilter(models.TableFilter{
		AreaID: c.Query("area_id"), Status: models.TableStatus(c.Query("status")), Search: c.Query("q"),
	})
	if err != nil {
		_ = c.Error(err)
		return
	}
	tables, total, err := ctl.tables.ListTables(c.Request.Context(), c.Param("oid"), f, page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, response.NewList(models.NewTableResponses(tables), page, total))
}

// GetTable handles GET /outlets/{oid}/tables/{tid}.
func (ctl *TableController) GetTable(c *gin.Context) {
	t, err := ctl.tables.Table(c.Request.Context(), c.Param("oid"), c.Param("tid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewTableResponse(t))
}

// CreateTable handles POST /outlets/{oid}/tables.
func (ctl *TableController) CreateTable(c *gin.Context) {
	var req validations.TableRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	t, err := ctl.tables.CreateTable(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, models.NewTableResponse(t))
}

// UpdateTable handles PUT /outlets/{oid}/tables/{tid}.
func (ctl *TableController) UpdateTable(c *gin.Context) {
	var req validations.TableRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	t, err := ctl.tables.UpdateTable(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("tid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewTableResponse(t))
}

// SetTableStatus handles PATCH /outlets/{oid}/tables/{tid}/status.
func (ctl *TableController) SetTableStatus(c *gin.Context) {
	var req validations.ActiveRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	t, err := ctl.tables.SetTableActive(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("tid"), *req.IsActive)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewTableResponse(t))
}

// DeleteTable handles DELETE /outlets/{oid}/tables/{tid}.
func (ctl *TableController) DeleteTable(c *gin.Context) {
	if err := ctl.tables.DeleteTable(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("tid")); err != nil {
		_ = c.Error(err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Release handles POST /outlets/{oid}/tables/{tid}/release.
func (ctl *TableController) Release(c *gin.Context) {
	t, err := ctl.tables.Release(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("tid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewTableResponse(t))
}

// Floor handles GET /outlets/{oid}/pos/tables: the cashier's floor.
func (ctl *TableController) Floor(c *gin.Context) {
	f, err := ctl.tables.Floor(c.Request.Context(), c.Param("oid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewFloorResponse(f))
}
