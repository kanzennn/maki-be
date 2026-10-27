package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
)

// RegisterOutletRoutes registers the outlet endpoints on owner, the group
// RequireOwner guards.
func RegisterOutletRoutes(owner gin.IRouter, ctl *controllers.OutletController) {
	owner.GET("/outlets", ctl.List)
	owner.POST("/outlets", ctl.Create)
	owner.GET("/outlets/:oid", ctl.Get)
	owner.PUT("/outlets/:oid", ctl.Update)
	owner.PATCH("/outlets/:oid/status", ctl.SetStatus)
	owner.DELETE("/outlets/:oid", ctl.Delete)
}
