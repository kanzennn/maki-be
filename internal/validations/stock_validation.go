package validations

import (
	"slices"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// StockAdjustmentRequest is the body of POST …/stock-adjustments. The caps
// only bound what the server reads; CheckStockAdjustment applies the real
// rules.
type StockAdjustmentRequest struct {
	Type   string  `json:"type" binding:"required,max=16"`
	Qty    *int    `json:"qty" binding:"required"`
	Reason string  `json:"reason" binding:"required,max=32"`
	Note   *string `json:"note" binding:"omitempty,max=1000"`
}

// Input converts the request, whose fields models.StockAdjustment repeats
// without JSON tags.
func (r StockAdjustmentRequest) Input() models.StockAdjustment {
	return models.StockAdjustment(r)
}

// StockAlertRequest is the body of PUT …/stock-alert; null turns the alert
// off.
type StockAlertRequest struct {
	StockAlert *int `json:"stock_alert"`
}

// adjustReasons are the reasons an adjustment gives. Other says what in
// its note.
var adjustReasons = []string{"restock", "damaged", "correction", "other"}

// CheckStockAdjustment validates an adjustment as filled in: every problem
// at once as validation_failed. Only a stock take may count 0, and a reason
// of other needs a note saying what it was.
func CheckStockAdjustment(in models.StockAdjustment) (models.StockChange, error) {
	fields := map[string]string{}
	a := models.StockChange{Type: in.Type, Reason: in.Reason, Note: trimmed(in.Note)}
	least := 1
	switch in.Type {
	case models.AdjustIn, models.AdjustOut:
	case models.AdjustSet:
		least = 0
	default:
		fields["type"] = "oneof"
	}
	switch {
	case in.Qty == nil:
		fields["qty"] = "required"
	case *in.Qty < least || *in.Qty > models.MaxStock:
		fields["qty"] = "range"
	default:
		a.Qty = *in.Qty
	}
	if !slices.Contains(adjustReasons, in.Reason) {
		fields["reason"] = "oneof"
	}
	switch {
	case a.Note != nil && utf8.RuneCountInString(*a.Note) > 200:
		fields["note"] = "max"
	case a.Note == nil && in.Reason == "other":
		fields["note"] = "required"
	}
	if len(fields) > 0 {
		return models.StockChange{}, apperror.Invalid(fields)
	}
	return a, nil
}

// CheckStockAlert validates a stock alert: none, or a count from 0 to
// MaxStock.
func CheckStockAlert(alert *int) error {
	if alert != nil && (*alert < 0 || *alert > models.MaxStock) {
		return apperror.Invalid(map[string]string{"stock_alert": "range"})
	}
	return nil
}
