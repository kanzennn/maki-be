package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// RegisterDashboardRoutes registers the dashboards: every outlet's on owner,
// which RequireOwner guards, and one outlet's on scoped.
func RegisterDashboardRoutes(owner, scoped gin.IRouter, ctl *controllers.DashboardController) {
	view := middlewares.RequirePermission("dashboard.view")
	owner.GET("/owner/dashboard", view, ctl.Owner)
	scoped.GET("/dashboard", view, ctl.Outlet)
}
