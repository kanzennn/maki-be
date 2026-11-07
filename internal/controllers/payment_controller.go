package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/validations"
)

// PaymentController serves an outlet's Payment Control.
type PaymentController struct {
	payments *services.PaymentControlService
}

// NewPaymentController returns Payment Control's controller.
func NewPaymentController(payments *services.PaymentControlService) *PaymentController {
	return &PaymentController{payments: payments}
}

// List handles GET /outlets/{oid}/payment-methods.
func (ctl *PaymentController) List(c *gin.Context) {
	methods, err := ctl.payments.Methods(c.Request.Context(), c.Param("oid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewPaymentMethodsResponse(methods))
}

// Set handles PUT /outlets/{oid}/payment-methods.
func (ctl *PaymentController) Set(c *gin.Context) {
	var req validations.PaymentMethodsRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	changes, err := validations.CheckPaymentMethods(req)
	if err != nil {
		_ = c.Error(err)
		return
	}
	methods, err := ctl.payments.SetMethods(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), changes)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewPaymentMethodsResponse(methods))
}
