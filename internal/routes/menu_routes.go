package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/kervzent/maki-be/internal/controllers"
	"github.com/kervzent/maki-be/internal/middlewares"
)

// RegisterMenuRoutes registers an outlet's menu on scoped, the group under
// /outlets/:oid that OutletScope guards: its categories, its menus, and
// their stock. Each route names the permission it needs.
func RegisterMenuRoutes(scoped gin.IRouter, categories *controllers.CategoryController, menus *controllers.MenuController,
	stock *controllers.StockController,
) {
	view, manage := middlewares.RequirePermission("menu.view"), middlewares.RequirePermission("menu.manage")
	scoped.GET("/categories", view, categories.ListCategories)
	scoped.POST("/categories", manage, categories.CreateCategory)
	scoped.PUT("/categories/order", manage, categories.ReorderCategories)
	scoped.GET("/categories/:cid", view, categories.GetCategory)
	scoped.PUT("/categories/:cid", manage, categories.UpdateCategory)
	scoped.PATCH("/categories/:cid/status", manage, categories.SetCategoryStatus)
	scoped.DELETE("/categories/:cid", manage, categories.DeleteCategory)

	scoped.GET("/menus", view, menus.ListMenus)
	scoped.POST("/menus", manage, menus.CreateMenu)
	scoped.GET("/menus/:mid", view, menus.GetMenu)
	scoped.PUT("/menus/:mid", manage, menus.UpdateMenu)
	scoped.PATCH("/menus/:mid/status", manage, menus.SetMenuStatus)
	scoped.DELETE("/menus/:mid", manage, menus.DeleteMenu)

	// The alert is a field of the menu form too, so either may set it.
	adjust, alert := middlewares.RequirePermission("stock.adjust"), middlewares.RequireAnyPermission("stock.adjust", "menu.manage")
	scoped.POST("/menus/:mid/stock-adjustments", adjust, stock.AdjustStock)
	scoped.PUT("/menus/:mid/stock-alert", alert, stock.SetStockAlert)
	scoped.GET("/menus/:mid/stock-movements", view, stock.ListStockMovements)
}
