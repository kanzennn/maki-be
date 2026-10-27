package validations

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// MenuRequest is the body of POST and PUT on a menu. The caps only bound
// what the server reads; CheckMenu applies the real rules.
type MenuRequest struct {
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

// Input converts the request.
func (r MenuRequest) Input() models.MenuInput {
	return models.MenuInput{
		Name: r.Name, CategoryID: r.CategoryID, Price: r.Price, Description: r.Description, SKU: r.SKU,
		TrackStock: r.TrackStock, InitialStock: r.InitialStock, StockAlert: r.StockAlert, Active: r.IsActive,
	}
}

// CheckMenuFilter checks the menu list's filters: a status is active or
// inactive, a stock one of the stock conditions. The search comes back
// trimmed.
func CheckMenuFilter(f models.MenuFilter) (models.MenuFilter, error) {
	fields := map[string]string{}
	if f.Status != "" && f.Status != "active" && f.Status != "inactive" {
		fields["status"] = "oneof"
	}
	switch f.Stock {
	case "", models.StockOK, models.StockLow, models.StockOut, models.StockUntracked:
	default:
		fields["stock"] = "oneof"
	}
	if len(fields) > 0 {
		return models.MenuFilter{}, apperror.Invalid(fields)
	}
	f.Search = strings.TrimSpace(f.Search)
	return f, nil
}

// skuPattern is the menus_sku_check constraint's rule.
var skuPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// CheckMenu validates a menu as filled in: every problem at once as
// validation_failed. An untracked menu has no alert.
func CheckMenu(in models.MenuInput) (models.MenuFields, error) {
	fields := map[string]string{}
	m := models.MenuFields{
		CategoryID: strings.TrimSpace(in.CategoryID), Name: strings.TrimSpace(in.Name),
		Description: trimmed(in.Description), SKU: trimmed(in.SKU), TrackStock: in.TrackStock,
	}
	switch n := utf8.RuneCountInString(m.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 100:
		fields["name"] = "max"
	}
	if m.CategoryID == "" {
		fields["category_id"] = "required"
	}
	switch {
	case in.Price == nil:
		fields["price"] = "required"
	case *in.Price < 0 || *in.Price > models.MaxPrice:
		fields["price"] = "range"
	default:
		m.Price = *in.Price
	}
	if m.Description != nil && utf8.RuneCountInString(*m.Description) > 500 {
		fields["description"] = "max"
	}
	if m.SKU != nil && !skuPattern.MatchString(*m.SKU) {
		fields["sku"] = "format"
	}
	if in.InitialStock != nil && (*in.InitialStock < 0 || *in.InitialStock > models.MaxStock) {
		fields["initial_stock"] = "range"
	}
	if m.TrackStock && in.StockAlert != nil {
		if *in.StockAlert < 0 || *in.StockAlert > models.MaxStock {
			fields["stock_alert"] = "range"
		}
		m.StockAlert = in.StockAlert
	}
	if len(fields) > 0 {
		return models.MenuFields{}, apperror.Invalid(fields)
	}
	return m, nil
}
