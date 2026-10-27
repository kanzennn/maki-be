package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// OrderControllers are the controllers of the cashier: what it starts from,
// the sales it takes, and the outlet's orders.
type OrderControllers struct {
	Pos      *controllers.PosController
	Checkout *controllers.CheckoutController
	Orders   *controllers.OrderController
}

// RegisterOrderRoutes registers the cashier's endpoints on scoped, the group
// under /outlets/:oid that OutletScope guards. Selling needs
// checkout.create, and an order is taken only from one of the outlet's
// devices too. The orders are for whoever sells or voids them; voiding needs
// order.void.
func RegisterOrderRoutes(scoped gin.IRouter, ctl OrderControllers) {
	sell := middlewares.RequirePermission("checkout.create")
	read := middlewares.RequireAnyPermission("checkout.create", "order.void")
	scoped.GET("/pos/catalog", sell, ctl.Pos.Catalog)
	scoped.GET("/pos/device", sell, ctl.Pos.Device)
	scoped.POST("/orders", sell, ctl.Checkout.Create)
	scoped.GET("/orders", read, ctl.Orders.List)
	scoped.GET("/orders/:orid", read, ctl.Orders.Get)
	scoped.POST("/orders/:orid/void", middlewares.RequirePermission("order.void"), ctl.Orders.Void)
}
