package menu

import (
	"net/http"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Errors of the menu endpoints. Messages live in the i18n catalogs.
var (
	// ErrCategoryNotFound is a category that isn't the outlet's, or was
	// deleted.
	ErrCategoryNotFound = httpx.NewError(http.StatusNotFound, "category_not_found")
	// ErrCategoryNameTaken is a name another category of the outlet has.
	ErrCategoryNameTaken = httpx.NewError(http.StatusConflict, "category_name_taken")
	// ErrCategoryInUse is deleting a category that still holds menus;
	// details.menus counts them.
	ErrCategoryInUse = httpx.NewError(http.StatusConflict, "category_in_use")
	// ErrCategoriesChanged is a new order that doesn't list the outlet's
	// categories as they are now, each once: one was added or deleted since
	// the list was read.
	ErrCategoriesChanged = httpx.NewError(http.StatusConflict, "categories_changed")

	// ErrMenuNotFound is a menu that isn't the outlet's, or was deleted.
	ErrMenuNotFound = httpx.NewError(http.StatusNotFound, "menu_not_found")
	// ErrMenuSKUTaken is a SKU another menu of the outlet has.
	ErrMenuSKUTaken = httpx.NewError(http.StatusConflict, "menu_sku_taken")

	// ErrStockInsufficient is taking away more than the count;
	// details.available is what there is.
	ErrStockInsufficient = httpx.NewError(http.StatusConflict, "stock_insufficient")
	// ErrStockNotTracked is adjusting, or setting the alert of, a menu whose
	// stock isn't tracked.
	ErrStockNotTracked = httpx.NewError(http.StatusConflict, "stock_not_tracked")
)
