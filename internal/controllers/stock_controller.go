package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/response"
	"github.com/kervzent/maki-be/internal/validations"
)

// StockController serves the stock of an outlet's menus.
type StockController struct {
	stock *services.StockService
}

// NewStockController returns the stock's controller.
func NewStockController(stock *services.StockService) *StockController {
	return &StockController{stock: stock}
}

// AdjustStock handles POST /outlets/{oid}/menus/{mid}/stock-adjustments. It
// answers with the menu as the adjustment left it.
func (ctl *StockController) AdjustStock(c *gin.Context) {
	var req validations.StockAdjustmentRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	m, err := ctl.stock.AdjustStock(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid"), req.Input())
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewMenuResponse(m))
}

// SetStockAlert handles PUT /outlets/{oid}/menus/{mid}/stock-alert.
func (ctl *StockController) SetStockAlert(c *gin.Context) {
	var req validations.StockAlertRequest
	if err := validations.BindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	m, err := ctl.stock.SetStockAlert(c.Request.Context(), principal(c), c.ClientIP(), c.Param("oid"), c.Param("mid"), req.StockAlert)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, models.NewMenuResponse(m))
}

// ListStockMovements handles GET /outlets/{oid}/menus/{mid}/stock-movements?page=&page_size=.
func (ctl *StockController) ListStockMovements(c *gin.Context) {
	page, err := validations.BindPage(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	items, total, err := ctl.stock.StockMovements(c.Request.Context(), c.Param("oid"), c.Param("mid"), page)
	if err != nil {
		_ = c.Error(err)
		return
	}
	out := make([]models.StockMovementResponse, len(items))
	for i, item := range items {
		out[i] = models.NewStockMovementResponse(item)
	}
	c.JSON(http.StatusOK, response.NewList(out, page, total))
}
