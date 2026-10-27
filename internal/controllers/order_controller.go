package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// OrderController serves an outlet's orders, for whoever sells or voids
// them.
type OrderController struct {
	orders *services.OrderService
}

// NewOrderController returns the orders' controller.
func NewOrderController(orders *services.OrderService) *OrderController {
	return &OrderController{orders: orders}
}

// List handles GET /outlets/{oid}/orders?q=&page=&page_size=: today's.
func (ctl *OrderController) List(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	items, total, err := ctl.orders.List(c.Request.Context(), c.Param("oid"), c.Query("q"), page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.OrderSummaryResponse, len(items))
	for i, item := range items {
		out[i] = models.NewOrderSummaryResponse(item)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}

// Get handles GET /outlets/{oid}/orders/{orid}.
func (ctl *OrderController) Get(c *gin.Context) {
	o, err := ctl.orders.Order(c.Request.Context(), c.Param("oid"), c.Param("orid"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewOrderResponse(o))
}

// Void handles POST /outlets/{oid}/orders/{orid}/void.
func (ctl *OrderController) Void(c *gin.Context) {
	var req validations.VoidOrderRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	o, err := ctl.orders.Void(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("orid"), req.Reason)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewOrderResponse(o))
}
