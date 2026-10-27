package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// TableControllers are the controllers of an outlet's tables: its areas,
// their layouts, the tables themselves, their schedules, and its dine-in
// mode.
type TableControllers struct {
	Areas     *controllers.AreaController
	Layouts   *controllers.LayoutController
	Tables    *controllers.TableController
	Schedules *controllers.ScheduleController
	Modes     *controllers.DineInModeController
}

// RegisterTableRoutes registers an outlet's tables on scoped, the group
// under /outlets/:oid that OutletScope guards: table.view reads the areas,
// the tables, their layouts and the schedules, which the Owner does too,
// and table.manage changes them. The cashier (checkout.create) reads the
// floor and releases tables.
func RegisterTableRoutes(scoped gin.IRouter, ctl TableControllers) {
	view, manage := middlewares.RequirePermission("table.view"), middlewares.RequirePermission("table.manage")
	sell := middlewares.RequirePermission("checkout.create")
	scoped.GET("/areas", view, ctl.Areas.ListAreas)
	scoped.POST("/areas", manage, ctl.Areas.CreateArea)
	scoped.PUT("/areas/order", manage, ctl.Areas.ReorderAreas)
	scoped.GET("/areas/:aid", view, ctl.Areas.GetArea)
	scoped.PUT("/areas/:aid", manage, ctl.Areas.UpdateArea)
	scoped.DELETE("/areas/:aid", manage, ctl.Areas.DeleteArea)
	scoped.GET("/areas/:aid/layout", view, ctl.Layouts.GetLayout)
	scoped.PUT("/areas/:aid/layout", manage, ctl.Layouts.SaveLayout)

	scoped.GET("/tables", view, ctl.Tables.ListTables)
	scoped.POST("/tables", manage, ctl.Tables.CreateTable)
	scoped.GET("/tables/:tid", view, ctl.Tables.GetTable)
	scoped.PUT("/tables/:tid", manage, ctl.Tables.UpdateTable)
	scoped.PATCH("/tables/:tid/status", manage, ctl.Tables.SetTableStatus)
	scoped.DELETE("/tables/:tid", manage, ctl.Tables.DeleteTable)
	scoped.POST("/tables/:tid/release", sell, ctl.Tables.Release)
	scoped.GET("/pos/tables", sell, ctl.Tables.Floor)

	scoped.GET("/dine-in-mode", view, ctl.Modes.GetDineInMode)
	scoped.PUT("/dine-in-mode", manage, ctl.Modes.SetDineInMode)

	scoped.GET("/table-schedules", view, ctl.Schedules.ListSchedules)
	scoped.POST("/table-schedules", manage, ctl.Schedules.CreateSchedule)
	scoped.GET("/table-schedules/:sid", view, ctl.Schedules.GetSchedule)
	scoped.PUT("/table-schedules/:sid", manage, ctl.Schedules.UpdateSchedule)
	scoped.DELETE("/table-schedules/:sid", manage, ctl.Schedules.CancelSchedule)
}
