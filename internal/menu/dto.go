package menu

import "time"

// categoryRequest is the body of POST and PUT on a category. The caps here
// only bound what the server reads; checkCategory applies the real rules.
type categoryRequest struct {
	Name string  `json:"name" binding:"required,max=400"`
	Icon *string `json:"icon" binding:"omitempty,max=64"`
}

func (r categoryRequest) input() CategoryInput {
	return CategoryInput(r)
}

// statusRequest turns a category or a menu on or off at the cashier.
type statusRequest struct {
	IsActive *bool `json:"is_active" binding:"required"`
}

type orderRequest struct {
	CategoryIDs []string `json:"category_ids" binding:"required,max=1000,dive,max=32"`
}

type categoryResponse struct {
	CategoryID string    `json:"category_id"`
	Name       string    `json:"name"`
	Icon       *string   `json:"icon"`
	SortOrder  int       `json:"sort_order"`
	IsActive   bool      `json:"is_active"`
	MenuCount  int       `json:"menu_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toCategoryResponse(c Category) categoryResponse {
	return categoryResponse{
		CategoryID: c.CategoryID,
		Name:       c.Name,
		Icon:       c.Icon,
		SortOrder:  c.SortOrder,
		IsActive:   c.Active,
		MenuCount:  c.MenuCount,
		CreatedAt:  c.CreatedAt.UTC(),
		UpdatedAt:  c.UpdatedAt.UTC(),
	}
}

// menuRequest is the body of POST and PUT on a menu. The caps here only
// bound what the server reads; checkMenu applies the real rules.
type menuRequest struct {
	Name         string  `json:"name" binding:"required,max=400"`
	CategoryID   string  `json:"category_id" binding:"required,max=64"`
	Price        *int64  `json:"price" binding:"required"`
	Description  *string `json:"description" binding:"omitempty,max=2000"`
	SKU          *string `json:"sku" binding:"omitempty,max=128"`
	TrackStock   bool    `json:"track_stock"`
	InitialStock *int    `json:"initial_stock"`
	StockAlert   *int    `json:"stock_alert"`
	IsActive     *bool   `json:"is_active"`
}

func (r menuRequest) input() MenuInput {
	return MenuInput{
		Name: r.Name, CategoryID: r.CategoryID, Price: r.Price, Description: r.Description, SKU: r.SKU,
		TrackStock: r.TrackStock, InitialStock: r.InitialStock, StockAlert: r.StockAlert, Active: r.IsActive,
	}
}

type menuCategoryResponse struct {
	CategoryID string  `json:"category_id"`
	Name       string  `json:"name"`
	Icon       *string `json:"icon"`
	IsActive   bool    `json:"is_active"`
}

type menuResponse struct {
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
	Category    menuCategoryResponse `json:"category"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

func toMenuResponse(m Menu) menuResponse {
	return menuResponse{
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
		Category: menuCategoryResponse{
			CategoryID: m.Category.CategoryID, Name: m.Category.Name, Icon: m.Category.Icon, IsActive: m.Category.Active,
		},
		CreatedAt: m.CreatedAt.UTC(),
		UpdatedAt: m.UpdatedAt.UTC(),
	}
}

// adjustmentRequest is the body of POST …/stock-adjustments. The caps here
// only bound what the server reads; checkAdjustment applies the real rules.
type adjustmentRequest struct {
	Type   string  `json:"type" binding:"required,max=16"`
	Qty    *int    `json:"qty" binding:"required"`
	Reason string  `json:"reason" binding:"required,max=32"`
	Note   *string `json:"note" binding:"omitempty,max=1000"`
}

func (r adjustmentRequest) input() Adjustment {
	return Adjustment(r)
}

// stockAlertRequest is the body of PUT …/stock-alert; null turns the alert
// off.
type stockAlertRequest struct {
	StockAlert *int `json:"stock_alert"`
}

type movementUserResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type movementResponse struct {
	StockMovementID string               `json:"stock_movement_id"`
	Type            string               `json:"type"`
	QtyChange       int                  `json:"qty_change"`
	StockAfter      int                  `json:"stock_after"`
	Reason          *string              `json:"reason"`
	Note            *string              `json:"note"`
	User            movementUserResponse `json:"user"`
	OrderID         *string              `json:"order_id"`
	CreatedAt       time.Time            `json:"created_at"`
}

func toMovementResponse(m StockMovement) movementResponse {
	return movementResponse{
		StockMovementID: m.StockMovementID,
		Type:            m.Type,
		QtyChange:       m.QtyChange,
		StockAfter:      m.StockAfter,
		Reason:          m.Reason,
		Note:            m.Note,
		User:            movementUserResponse{UserID: m.UserID, Name: m.UserName},
		OrderID:         m.OrderID,
		CreatedAt:       m.CreatedAt.UTC(),
	}
}
