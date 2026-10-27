package models

import "time"

// PosCatalog is everything the cashier sells from, in one: the outlet for
// the receipt and the rates, the payment methods it takes, how it seats its
// dine-in customers, and its categories and menus in the cashier's order.
// One request, so a device can later keep it offline whole.
type PosCatalog struct {
	Outlet         OutletCashier
	PaymentMethods []string
	DineInMode     DineInMode
	Categories     []PosCategory
	Menus          []Menu
}

// PosDevice is the device the cashier runs on, and its count of receipt
// numbers today.
type PosDevice struct {
	Device Device
	// Prefix starts each of its receipt numbers: the outlet's code and the
	// device's, as in BDG01-K01.
	Prefix string
	// Day is today in the outlet's time zone, from its midnight.
	Day time.Time
	// NextNumber is the count the device's next receipt number today
	// carries: one past the highest it has used.
	NextNumber int
}

// CatalogResponse is GET /outlets/{oid}/pos/catalog.
type CatalogResponse struct {
	Outlet         CatalogOutletResponse     `json:"outlet"`
	PaymentMethods []string                  `json:"payment_methods"`
	DineInMode     string                    `json:"dine_in_mode"`
	Categories     []CatalogCategoryResponse `json:"categories"`
	Menus          []CatalogMenuResponse     `json:"menus"`
}

// CatalogOutletResponse is the outlet in the catalog.
type CatalogOutletResponse struct {
	OutletID string  `json:"outlet_id"`
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Address  *string `json:"address"`
	Phone    *string `json:"phone"`
	Timezone string  `json:"timezone"`
	Locale   string  `json:"locale"`
	// Basis points, so the cashier's totals are exact integers, as the
	// server's are.
	TaxBPS     int `json:"tax_bps"`
	ServiceBPS int `json:"service_bps"`
}

// CatalogCategoryResponse is a category in the catalog.
type CatalogCategoryResponse struct {
	CategoryID string  `json:"category_id"`
	Name       string  `json:"name"`
	Icon       *string `json:"icon"`
	ItemCount  int     `json:"item_count"`
}

// CatalogMenuResponse is a menu in the catalog.
type CatalogMenuResponse struct {
	MenuID      string  `json:"menu_id"`
	CategoryID  string  `json:"category_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	SKU         *string `json:"sku"`
	Price       int64   `json:"price"`
	// The photo's CDN address, with the upload milestone; null until then.
	ImageURL   *string `json:"image_url"`
	TrackStock bool    `json:"track_stock"`
	// Null for a menu whose stock isn't tracked: it is always available.
	StockQty    *int   `json:"stock_qty"`
	StockAlert  *int   `json:"stock_alert"`
	StockStatus string `json:"stock_status"`
}

// NewCatalogResponse returns the catalog's response.
func NewCatalogResponse(c PosCatalog) CatalogResponse {
	o := c.Outlet
	out := CatalogResponse{
		Outlet: CatalogOutletResponse{
			OutletID: o.OutletID, Code: o.Code, Name: o.Name, Address: o.Address, Phone: o.Phone,
			Timezone: o.Timezone, Locale: o.Locale, TaxBPS: o.TaxBPS, ServiceBPS: o.ServiceBPS,
		},
		PaymentMethods: c.PaymentMethods,
		DineInMode:     string(c.DineInMode),
		Categories:     make([]CatalogCategoryResponse, len(c.Categories)),
		Menus:          make([]CatalogMenuResponse, len(c.Menus)),
	}
	if out.PaymentMethods == nil {
		out.PaymentMethods = []string{}
	}
	for i, cat := range c.Categories {
		out.Categories[i] = CatalogCategoryResponse(cat)
	}
	for i, m := range c.Menus {
		out.Menus[i] = NewCatalogMenuResponse(m)
	}
	return out
}

// NewCatalogMenuResponse returns a menu as the catalog sends it.
func NewCatalogMenuResponse(m Menu) CatalogMenuResponse {
	out := CatalogMenuResponse{
		MenuID: m.MenuID, CategoryID: m.Category.CategoryID, Name: m.Name, Description: m.Description, SKU: m.SKU,
		Price: m.Price, TrackStock: m.TrackStock, StockStatus: m.StockStatus,
	}
	if m.TrackStock {
		qty := m.StockQty
		out.StockQty, out.StockAlert = &qty, m.StockAlert
	}
	return out
}

// PosDeviceResponse is GET /outlets/{oid}/pos/device.
type PosDeviceResponse struct {
	Device PosDeviceInfo `json:"device"`
	// Each of the device's receipt numbers is the prefix, the outlet's day
	// as YYYYMMDD, and its count that day, at least four digits:
	// BDG01-K01-20261001-0001.
	OrderNoPrefix string `json:"order_no_prefix"`
	// Day is today in the outlet's time zone, as YYYY-MM-DD; NextNumber is
	// the count the device's next receipt number today carries. A new day
	// starts the count at 1.
	Day        string `json:"day"`
	NextNumber int    `json:"next_number"`
}

// PosDeviceInfo is the device in PosDeviceResponse.
type PosDeviceInfo struct {
	DeviceID string `json:"device_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

// NewPosDeviceResponse returns the device's response.
func NewPosDeviceResponse(d PosDevice) PosDeviceResponse {
	return PosDeviceResponse{
		Device: PosDeviceInfo{
			DeviceID: d.Device.DeviceID, Code: d.Device.Code, Name: d.Device.Name, Platform: d.Device.Platform,
		},
		OrderNoPrefix: d.Prefix,
		Day:           d.Day.Format(time.DateOnly),
		NextNumber:    d.NextNumber,
	}
}
