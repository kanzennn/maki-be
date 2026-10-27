package models

import "time"

// Kinds of stock adjustment.
const (
	AdjustIn  = "in"  // stock added
	AdjustOut = "out" // stock taken away
	AdjustSet = "set" // a stock take: the count found
)

// StockAdjustment is a change a person makes to a tracked menu's count, as
// filled in: Qty added (in) or taken away (out), or the count found (set).
// Reason is one of restock, damaged, correction or other.
type StockAdjustment struct {
	Type   string
	Qty    *int
	Reason string
	Note   *string
}

// StockChange is a StockAdjustment, checked.
type StockChange struct {
	Type   string
	Qty    int
	Reason string
	Note   *string
}

// StockMovement is one entry of a menu's stock history.
type StockMovement struct {
	StockMovementID string
	Type            string // initial, in, out, set, sale or void
	QtyChange       int
	StockAfter      int
	Reason          *string
	Note            *string
	UserID          string
	UserName        string
	OrderID         *string // a sale's or void's
	CreatedAt       time.Time
}

// NewStockMovement is a change to a tracked menu's stock to record, from
// Before to After.
type NewStockMovement struct {
	OutletID, MenuID, UserID string
	Type                     string  // initial, in, out, set, sale or void
	Reason                   *string // an adjustment's reason, or initial; a sale or void has none
	Note                     *string
	OrderID                  *string // a sale's or void's
	Before, After            int
}

// StockMovementResponse is an entry of a menu's stock history, as the stock
// endpoints send it.
type StockMovementResponse struct {
	StockMovementID string         `json:"stock_movement_id"`
	Type            string         `json:"type"`
	QtyChange       int            `json:"qty_change"`
	StockAfter      int            `json:"stock_after"`
	Reason          *string        `json:"reason"`
	Note            *string        `json:"note"`
	User            PersonResponse `json:"user"`
	OrderID         *string        `json:"order_id"`
	CreatedAt       time.Time      `json:"created_at"`
}

// NewStockMovementResponse returns the entry's response, its time in UTC.
func NewStockMovementResponse(m StockMovement) StockMovementResponse {
	return StockMovementResponse{
		StockMovementID: m.StockMovementID,
		Type:            m.Type,
		QtyChange:       m.QtyChange,
		StockAfter:      m.StockAfter,
		Reason:          m.Reason,
		Note:            m.Note,
		User:            PersonResponse{UserID: m.UserID, Name: m.UserName},
		OrderID:         m.OrderID,
		CreatedAt:       m.CreatedAt.UTC(),
	}
}
