package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// RegisterLogRoutes registers the Log Action: every outlet's on owner, which
// RequireOwner guards, and one outlet's on scoped.
func RegisterLogRoutes(owner, scoped gin.IRouter, ctl *controllers.LogController) {
	view := middlewares.RequirePermission("log.view")
	owner.GET("/owner/logs", view, ctl.ListOwner)
	scoped.GET("/logs", view, ctl.ListOutlet)
}
