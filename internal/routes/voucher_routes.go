package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// RegisterVoucherRoutes registers the Owner's vouchers on owner, which
// RequireOwner guards, and an outlet's on scoped, under /outlets/:oid: its
// Manager's list, and the cashier's check, which needs checkout.create and
// one of the outlet's devices, so codes can't be tried from anywhere.
func RegisterVoucherRoutes(owner, scoped gin.IRouter, ctl *controllers.VoucherController) {
	owner.GET("/vouchers", ctl.List)
	owner.POST("/vouchers", ctl.Create)
	owner.GET("/vouchers/:vid", ctl.Get)
	owner.PUT("/vouchers/:vid", ctl.Update)
	owner.PATCH("/vouchers/:vid/status", ctl.SetStatus)
	owner.DELETE("/vouchers/:vid", ctl.Delete)
	owner.GET("/vouchers/:vid/redemptions", ctl.Redemptions)

	scoped.GET("/vouchers", middlewares.RequireManagerOrOwner(), ctl.ListForOutlet)
	scoped.POST("/vouchers/validate", middlewares.RequirePermission("checkout.create"), ctl.Validate)
}
