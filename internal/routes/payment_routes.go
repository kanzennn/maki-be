package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// RegisterPaymentRoutes registers an outlet's Payment Control on scoped.
func RegisterPaymentRoutes(scoped gin.IRouter, ctl *controllers.PaymentController) {
	manage := middlewares.RequirePermission("payment.manage")
	scoped.GET("/payment-methods", manage, ctl.List)
	scoped.PUT("/payment-methods", manage, ctl.Set)
}
