package models

import "time"

// Limits of a menu's fields, the schema's own.
const (
	MaxPrice = 100_000_000 // Rupiah
	MaxStock = 1_000_000   // the most a count, an alert or one adjustment may be
)

// Stock conditions, as menu_stock_status computes them.
const (
	StockUntracked = "untracked" // always available
	StockOK        = "ok"
	StockLow       = "low" // at or below the alert
	StockOut       = "out" // none left
)

// Menu is one of an outlet's menus.
type Menu struct {
	MenuID      string
	Name        string
	Description *string
	SKU         *string
	Price       int64 // Rupiah
	ImageKey    *string
	TrackStock  bool
	StockQty    int
	StockAlert  *int
	StockStatus string
	Active      bool
	Category    MenuCategory
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MenuCategory is the category a menu is in.
type MenuCategory struct {
	CategoryID string
	Name       string
	Icon       *string
	Active     bool
}

// MenuInput is a menu as a Manager fills it in. InitialStock counts only
// when tracking starts: for a new tracked menu, or when TrackStock is turned
// on. Otherwise stock changes through adjustments. Active is for a new menu
// (nil: active); an existing one changes it with SetMenuActive.
type MenuInput struct {
	Name         string
	CategoryID   string
	Price        *int64
	Description  *string
	SKU          *string
	TrackStock   bool
	InitialStock *int
	StockAlert   *int
	Active       *bool
}

// MenuFilter narrows a list of menus; empty fields don't.
type MenuFilter struct {
	Search     string // part of the name or the SKU
	CategoryID string
	Status     string // active or inactive
	Stock      string // one of the Stock conditions
}

// MenuFields are a menu's own fields, checked. StockQty and Active are only
// read from a locked row.
type MenuFields struct {
	CategoryID  string
	Name        string
	Description *string
	SKU         *string
	Price       int64
	TrackStock  bool
	StockAlert  *int
	StockQty    int
	Active      bool
}

// Snapshot is the fields as the Log Action stores them.
func (m MenuFields) Snapshot() map[string]any {
	return map[string]any{
		"category_id": m.CategoryID, "name": m.Name, "description": m.Description, "sku": m.SKU,
		"price": m.Price, "track_stock": m.TrackStock, "stock_alert": NullableInt(m.StockAlert),
	}
}

// NullableInt is an optional number as the Log Action stores it: the
// number, or null.
func NullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// MenuCategoryResponse is the category a menu is in, as the menu endpoints
// send it.
type MenuCategoryResponse struct {
	CategoryID string  `json:"category_id"`
	Name       string  `json:"name"`
	Icon       *string `json:"icon"`
	IsActive   bool    `json:"is_active"`
}

// MenuResponse is a menu as the menu endpoints send it.
type MenuResponse struct {
	MenuID      string  `json:"menu_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	SKU         *string `json:"sku"`
	Price       int64   `json:"price"`
	// The photo's CDN address, with the upload milestone; null until then.
	ImageURL    *string              `json:"image_url"`
	TrackStock  bool                 `json:"track_stock"`
	StockQty    int                  `json:"stock_qty"`
	StockAlert  *int                 `json:"stock_alert"`
	StockStatus string               `json:"stock_status"`
	IsActive    bool                 `json:"is_active"`
	Category    MenuCategoryResponse `json:"category"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

// NewMenuResponse returns the menu's response, its times in UTC.
func NewMenuResponse(m Menu) MenuResponse {
	return MenuResponse{
		MenuID:      m.MenuID,
		Name:        m.Name,
		Description: m.Description,
		SKU:         m.SKU,
		Price:       m.Price,
		TrackStock:  m.TrackStock,
		StockQty:    m.StockQty,
		StockAlert:  m.StockAlert,
		StockStatus: m.StockStatus,
		IsActive:    m.Active,
		Category: MenuCategoryResponse{
			CategoryID: m.Category.CategoryID, Name: m.Category.Name, Icon: m.Category.Icon, IsActive: m.Category.Active,
		},
		CreatedAt: m.CreatedAt.UTC(),
		UpdatedAt: m.UpdatedAt.UTC(),
	}
}

// PosCategory is a category as the cashier shows it: active, and holding
// ItemCount of the cashier's menus.
type PosCategory struct {
	CategoryID string
	Name       string
	Icon       *string
	ItemCount  int
}

// SaleMenu is one of an order's menus as its sale finds it, locked until the
// sale commits.
type SaleMenu struct {
	MenuID     string
	Name       string
	Price      int64
	TrackStock bool
	StockQty   int
	// Available is false for a menu the cashier no longer shows: inactive,
	// or in an inactive category.
	Available bool
}

// Sold is how many of a menu an order sold.
type Sold struct {
	MenuID string
	Qty    int
}
