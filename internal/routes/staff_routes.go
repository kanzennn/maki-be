package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// RegisterStaffRoutes registers an outlet's user accounts on scoped, the
// group under /outlets/:oid that OutletScope guards.
func RegisterStaffRoutes(scoped gin.IRouter, ctl *controllers.StaffController) {
	view, manage := middlewares.RequirePermission("user.view"), middlewares.RequirePermission("user.manage")
	scoped.GET("/users", view, ctl.ListStaff)
	scoped.POST("/users", manage, ctl.CreateStaff)
	scoped.GET("/users/:uid", view, ctl.GetStaff)
	scoped.PUT("/users/:uid", manage, ctl.UpdateStaff)
	scoped.PATCH("/users/:uid/status", manage, ctl.SetStaffStatus)
	scoped.POST("/users/:uid/reset-password", manage, ctl.ResetStaffPassword)
	scoped.DELETE("/users/:uid", manage, ctl.DeleteStaff)
}
