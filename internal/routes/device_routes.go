package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// RegisterDeviceRoutes registers an outlet's devices on scoped, the group
// under /outlets/:oid that OutletScope guards. The fixed roles open them,
// not a permission: the outlet's Manager activates a device, and the Owner,
// who looks into every outlet, may see and revoke them too.
func RegisterDeviceRoutes(scoped gin.IRouter, ctl *controllers.DeviceController) {
	scoped.POST("/devices", middlewares.RequireManager(), ctl.Activate)
	scoped.GET("/devices", middlewares.RequireManagerOrOwner(), ctl.List)
	scoped.DELETE("/devices/:did", middlewares.RequireManagerOrOwner(), ctl.Revoke)
}
