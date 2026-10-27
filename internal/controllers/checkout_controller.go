package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/cookie"
	"github.com/kervzent/maki-be/internal/validations"
)

// CheckoutController serves the sales the cashier takes.
type CheckoutController struct {
	checkout *services.CheckoutService
}

// NewCheckoutController returns the checkout's controller.
func NewCheckoutController(checkout *services.CheckoutService) *CheckoutController {
	return &CheckoutController{checkout: checkout}
}

// Create handles POST /outlets/{oid}/orders: 201 with the order taken, or
// 200 with it as it was for a resend.
func (ctl *CheckoutController) Create(c *gin.Context) {
	var req validations.CreateOrderRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	o, created, err := ctl.checkout.Create(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"),
		cookie.DeviceToken(c.Request), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, models.NewOrderResponse(o))
}
