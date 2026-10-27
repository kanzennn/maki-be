package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// RegisterRoleRoutes registers the permission catalog on protected, and an
// outlet's roles on scoped, the group under /outlets/:oid that OutletScope
// guards. Each route names the permission it needs.
func RegisterRoleRoutes(protected, scoped gin.IRouter, ctl *controllers.RoleController) {
	protected.GET("/permissions", ctl.Permissions)

	view, manage := middlewares.RequirePermission("role.view"), middlewares.RequirePermission("role.manage")
	// Whoever sees the outlet's users sees their roles' names, and whoever
	// manages them picks one: the list opens with user.view too.
	scoped.GET("/roles", middlewares.RequireAnyPermission("role.view", "user.view"), ctl.ListRoles)
	scoped.POST("/roles", manage, ctl.CreateRole)
	scoped.GET("/roles/:rid", view, ctl.GetRole)
	scoped.PUT("/roles/:rid", manage, ctl.UpdateRole)
	scoped.DELETE("/roles/:rid", manage, ctl.DeleteRole)
}
