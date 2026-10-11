package order

import (
	"time"

	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/menu"
)

// The catalog, GET /outlets/{oid}/pos/catalog.

type catalogResponse struct {
	Outlet         catalogOutletResponse     `json:"outlet"`
	PaymentMethods []string                  `json:"payment_methods"`
	DineInMode     string                    `json:"dine_in_mode"`
	Categories     []catalogCategoryResponse `json:"categories"`
	Menus          []catalogMenuResponse     `json:"menus"`
}

type catalogOutletResponse struct {
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

type catalogCategoryResponse struct {
	CategoryID string  `json:"category_id"`
	Name       string  `json:"name"`
	Icon       *string `json:"icon"`
	ItemCount  int     `json:"item_count"`
}

type catalogMenuResponse struct {
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

func toCatalogResponse(c Catalog) catalogResponse {
	o := c.Outlet
	out := catalogResponse{
		Outlet: catalogOutletResponse{
			OutletID: o.OutletID, Code: o.Code, Name: o.Name, Address: o.Address, Phone: o.Phone,
			Timezone: o.Timezone, Locale: o.Locale, TaxBPS: o.TaxBPS, ServiceBPS: o.ServiceBPS,
		},
		PaymentMethods: c.PaymentMethods,
		DineInMode:     string(c.DineInMode),
		Categories:     make([]catalogCategoryResponse, len(c.Categories)),
		Menus:          make([]catalogMenuResponse, len(c.Menus)),
	}
	if out.PaymentMethods == nil {
		out.PaymentMethods = []string{}
	}
	for i, cat := range c.Categories {
		out.Categories[i] = catalogCategoryResponse{
			CategoryID: cat.CategoryID, Name: cat.Name, Icon: cat.Icon, ItemCount: cat.ItemCount,
		}
	}
	for i, m := range c.Menus {
		out.Menus[i] = toCatalogMenu(m)
	}
	return out
}

func toCatalogMenu(m menu.Menu) catalogMenuResponse {
	out := catalogMenuResponse{
		MenuID: m.MenuID, CategoryID: m.Category.CategoryID, Name: m.Name, Description: m.Description, SKU: m.SKU,
		Price: m.Price, TrackStock: m.TrackStock, StockStatus: m.StockStatus,
	}
	if m.TrackStock {
		qty := m.StockQty
		out.StockQty, out.StockAlert = &qty, m.StockAlert
	}
	return out
}

// The device, GET /outlets/{oid}/pos/device.

type posDeviceResponse struct {
	Device posDeviceInfo `json:"device"`
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

type posDeviceInfo struct {
	DeviceID string `json:"device_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

func toPosDeviceResponse(d PosDevice) posDeviceResponse {
	return posDeviceResponse{
		Device:        toPosDeviceInfo(d.Device),
		OrderNoPrefix: d.Prefix,
		Day:           d.Day.Format(time.DateOnly),
		NextNumber:    d.NextNumber,
	}
}

func toPosDeviceInfo(d device.Device) posDeviceInfo {
	return posDeviceInfo{DeviceID: d.DeviceID, Code: d.Code, Name: d.Name, Platform: d.Platform}
}

// Orders.

// createOrderRequest is the body of POST /outlets/{oid}/orders. The caps
// here only bound what the server reads; checkOrder applies the real rules,
// to the lines and the payment too.
type createOrderRequest struct {
	OrderID         string             `json:"order_id" binding:"required,max=64"`
	OrderNo         string             `json:"order_no" binding:"required,max=64"`
	Type            string             `json:"type" binding:"required,max=16"`
	TableID         *string            `json:"table_id" binding:"omitempty,max=64"`
	StandNumber     *string            `json:"stand_number" binding:"omitempty,max=64"`
	ClientCreatedAt *time.Time         `json:"client_created_at" binding:"required"`
	Items           []orderItemRequest `json:"items" binding:"required,max=200"`
	VoucherCode     *string            `json:"voucher_code" binding:"omitempty,max=64"`
	Total           *int64             `json:"total" binding:"required"`
	Payment         *paymentRequest    `json:"payment" binding:"required"`
}

type orderItemRequest struct {
	MenuID string  `json:"menu_id"`
	Qty    *int    `json:"qty"`
	Price  *int64  `json:"price"`
	Note   *string `json:"note"`
}

type paymentRequest struct {
	Method   string `json:"method"`
	Received *int64 `json:"received"`
}

func (r createOrderRequest) input() NewOrder {
	in := NewOrder{
		OrderID: r.OrderID, OrderNo: r.OrderNo, Type: r.Type, TableID: r.TableID, StandNumber: r.StandNumber,
		ClientCreatedAt: r.ClientCreatedAt, Items: make([]NewItem, len(r.Items)),
		VoucherCode: r.VoucherCode, Total: r.Total,
	}
	for i, it := range r.Items {
		in.Items[i] = NewItem(it)
	}
	if r.Payment != nil {
		in.Payment = NewPayment(*r.Payment)
	}
	return in
}

// voidRequest is the body of POST …/orders/{orid}/void.
type voidRequest struct {
	Reason string `json:"reason" binding:"required,max=1000"`
}

type personResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type orderResponse struct {
	OrderID         string              `json:"order_id"`
	OrderNo         string              `json:"order_no"`
	DeviceID        string              `json:"device_id"`
	Type            string              `json:"type"`
	TableID         *string             `json:"table_id"`
	TableName       *string             `json:"table_name"`
	StandNumber     *string             `json:"stand_number"`
	Status          string              `json:"status"`
	Cashier         personResponse      `json:"cashier"`
	Items           []orderItemResponse `json:"items"`
	Subtotal        int64               `json:"subtotal"`
	VoucherCode     *string             `json:"voucher_code"`
	VoucherDiscount int64               `json:"voucher_discount"`
	ServiceBPS      int                 `json:"service_bps"`
	Service         int64               `json:"service"`
	TaxBPS          int                 `json:"tax_bps"`
	Tax             int64               `json:"tax"`
	Total           int64               `json:"total"`
	Payment         paymentResponse     `json:"payment"`
	ClientCreatedAt time.Time           `json:"client_created_at"`
	CreatedAt       time.Time           `json:"created_at"`
	VoidedAt        *time.Time          `json:"voided_at"`
	VoidedBy        *personResponse     `json:"voided_by"`
	VoidReason      *string             `json:"void_reason"`
}

type orderItemResponse struct {
	OrderItemID string  `json:"order_item_id"`
	MenuID      string  `json:"menu_id"`
	MenuName    string  `json:"menu_name"`
	Price       int64   `json:"price"`
	Qty         int     `json:"qty"`
	Note        *string `json:"note"`
	LineTotal   int64   `json:"line_total"`
}

type paymentResponse struct {
	Method   string `json:"method"`
	Amount   int64  `json:"amount"`
	Received *int64 `json:"received"`
	Change   *int64 `json:"change"`
	Status   string `json:"status"`
}

func toOrderResponse(o Order) orderResponse {
	out := orderResponse{
		OrderID:         o.OrderID,
		OrderNo:         o.OrderNo,
		DeviceID:        o.DeviceID,
		Type:            o.Type,
		TableID:         o.TableID,
		TableName:       o.TableName,
		StandNumber:     o.StandNumber,
		Status:          o.Status,
		Cashier:         personResponse(o.Cashier),
		Items:           make([]orderItemResponse, len(o.Items)),
		Subtotal:        o.Subtotal,
		VoucherCode:     o.VoucherCode,
		VoucherDiscount: o.Discount,
		ServiceBPS:      o.ServiceBPS,
		Service:         o.Service,
		TaxBPS:          o.TaxBPS,
		Tax:             o.Tax,
		Total:           o.Total,
		Payment:         paymentResponse(o.Payment),
		ClientCreatedAt: o.ClientCreatedAt.UTC(),
		CreatedAt:       o.CreatedAt.UTC(),
		VoidedAt:        utc(o.VoidedAt),
		VoidReason:      o.VoidReason,
	}
	for i, it := range o.Items {
		out.Items[i] = orderItemResponse(it)
	}
	if o.VoidedBy != nil {
		p := personResponse(*o.VoidedBy)
		out.VoidedBy = &p
	}
	return out
}

type orderSummaryResponse struct {
	OrderID         string         `json:"order_id"`
	OrderNo         string         `json:"order_no"`
	DeviceID        string         `json:"device_id"`
	Type            string         `json:"type"`
	TableName       *string        `json:"table_name"`
	StandNumber     *string        `json:"stand_number"`
	Status          string         `json:"status"`
	Total           int64          `json:"total"`
	ItemCount       int            `json:"item_count"`
	PaymentMethod   string         `json:"payment_method"`
	Cashier         personResponse `json:"cashier"`
	ClientCreatedAt time.Time      `json:"client_created_at"`
	CreatedAt       time.Time      `json:"created_at"`
}

func toOrderSummaryResponse(s Summary) orderSummaryResponse {
	return orderSummaryResponse{
		OrderID: s.OrderID, OrderNo: s.OrderNo, DeviceID: s.DeviceID, Type: s.Type, TableName: s.TableName,
		StandNumber: s.StandNumber, Status: s.Status, Total: s.Total, ItemCount: s.ItemCount,
		PaymentMethod: s.PaymentMethod, Cashier: personResponse(s.Cashier),
		ClientCreatedAt: s.ClientCreatedAt.UTC(), CreatedAt: s.CreatedAt.UTC(),
	}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
