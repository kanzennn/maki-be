package models

import (
	"slices"
	"time"
)

// Order types.
const (
	OrderDineIn   = "dine_in"
	OrderTakeaway = "takeaway"
)

// Statuses of an order and its payment.
const (
	OrderPaid = "paid"
	OrderVoid = "void"
)

// Limits of an order, the schema's own and the cashier's.
const (
	MaxOrderLines = 100 // lines in one order
	MaxOrderQty   = 999 // of one line
	MaxOrderNote  = 100 // characters in a line's note
	// MaxReceived is the most cash one payment takes in, in Rupiah.
	MaxReceived = 100_000_000_000
	// MaxSubtotal is the most an order's lines may add up to, in Rupiah: a
	// billion, so that every sum with a rate stays exact in JavaScript's
	// numbers too, where the web cashier works it out.
	MaxSubtotal = 1_000_000_000
	// MaxVoidReason is the most characters a void's reason has.
	MaxVoidReason = 200
)

// OrderLine is one line of an order, as its totals see it.
type OrderLine struct {
	Price int64 // Rupiah
	Qty   int
}

// OrderTotals are an order's amounts, in Rupiah.
type OrderTotals struct {
	Subtotal int64
	Discount int64
	Service  int64
	Tax      int64
	Total    int64
}

// Order is one of the cashier's transactions, as it was taken.
type Order struct {
	OrderID  string
	DeviceID string
	// OrderNo is the receipt number the device made:
	// OUTLETCODE-DEVICECODE-YYYYMMDD-0001.
	OrderNo string
	Type    string
	// TableID and TableName are the table a dine-in sale seated its
	// customers at, its name as it was then, for the receipt. Nil for none.
	TableID   *string
	TableName *string
	// StandNumber is the number on the stand a dine-in customer took, where
	// a waiter brings the order. Nil for none.
	StandNumber *string
	Status      string
	Cashier     Person
	Items       []OrderItem
	OrderTotals
	// VoucherCode is the code of the voucher the sale used, as it was then:
	// the receipt prints it beside the discount. Nil for none.
	VoucherCode *string
	// ServiceBPS and TaxBPS are the rates the order was charged at, in basis
	// points.
	ServiceBPS int
	TaxBPS     int
	Payment    OrderPayment
	// ClientCreatedAt is when the device made the order; CreatedAt, when the
	// server took it.
	ClientCreatedAt time.Time
	CreatedAt       time.Time
	VoidedAt        *time.Time
	VoidedBy        *Person
	VoidReason      *string
}

// OrderItem is a line of an order, with the menu's name and price as they
// were at the sale.
type OrderItem struct {
	OrderItemID string
	MenuID      string
	MenuName    string
	Price       int64
	Qty         int
	Note        *string
	LineTotal   int64
}

// OrderPayment is how an order was paid.
type OrderPayment struct {
	Method string
	Amount int64
	// Received and Change are cash's: what the customer handed over, and
	// what they got back.
	Received *int64
	Change   *int64
	Status   string
}

// OrderSummary is an order in a list.
type OrderSummary struct {
	OrderID         string
	DeviceID        string
	OrderNo         string
	Type            string
	TableName       *string
	StandNumber     *string
	Status          string
	Total           int64
	ItemCount       int // every line's qty, together
	PaymentMethod   string
	Cashier         Person
	ClientCreatedAt time.Time
	CreatedAt       time.Time
}

// OrderLock is an order locked for its void.
type OrderLock struct {
	OrderNo   string
	Total     int64
	Status    string
	VoucherID *string // the voucher the sale used; nil for none
}

// NewOrder is an order as the device sends it. The device makes its ID and
// receipt number; every figure in it is checked against the menus and the
// outlet's rates as they are when the server takes it. Nil is a field left
// out.
type NewOrder struct {
	OrderID string
	// OrderNo is OUTLETCODE-DEVICECODE-YYYYMMDD-0001: the device's codes,
	// the outlet's day of ClientCreatedAt, and the device's count that day.
	OrderNo string
	Type    string
	// TableID is the table a dine-in order seats its customers at; nil or
	// blank for none.
	TableID *string
	// StandNumber is the number on the stand a dine-in customer took, in an
	// outlet that seats them by number stands; nil or blank for none.
	StandNumber     *string
	ClientCreatedAt *time.Time
	Items           []NewOrderItem
	// VoucherCode is the voucher the customer gave, one per order; nil or
	// blank for none.
	VoucherCode *string
	// Total is what the cashier charged.
	Total   *int64
	Payment NewOrderPayment
}

// NewOrderItem is a line of a NewOrder. Price is the menu's, as the cashier
// showed it.
type NewOrderItem struct {
	MenuID string
	Qty    *int
	Price  *int64
	Note   *string
}

// NewOrderPayment is how a NewOrder is paid. Received is cash's: what the
// customer handed over.
type NewOrderPayment struct {
	Method   string
	Received *int64
}

// CheckedOrder is a NewOrder that passed its checks.
type CheckedOrder struct {
	OrderID, OrderNo, Type string
	// TableID is the table a dine-in order seats its customers at; empty for
	// none.
	TableID string
	// StandNumber is the number on a dine-in customer's stand, in capitals;
	// empty for none.
	StandNumber string
	// Prefix and Day are the receipt number's codes and its YYYYMMDD.
	Prefix, Day string
	CreatedAt   time.Time
	Lines       []CheckedLine
	// VoucherCode is the voucher's, as the vouchers keep it; empty for none.
	VoucherCode string
	Total       int64
	Method      string
	Received    *int64
}

// CheckedLine is a line of a CheckedOrder.
type CheckedLine struct {
	MenuID string
	Qty    int
	Price  int64
	Note   *string
}

// MenuIDs are the order's menus, each once, sorted: the order the sale
// locks them in.
func (c CheckedOrder) MenuIDs() []string {
	ids := make([]string, len(c.Lines))
	for i, l := range c.Lines {
		ids[i] = l.MenuID
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// Sold is how many of each menu the order sells, its lines together, by
// menu ID.
func (c CheckedOrder) Sold() []Sold {
	qty := map[string]int{}
	for _, l := range c.Lines {
		qty[l.MenuID] += l.Qty
	}
	ids := c.MenuIDs()
	out := make([]Sold, len(ids))
	for i, menuID := range ids {
		out[i] = Sold{MenuID: menuID, Qty: qty[menuID]}
	}
	return out
}

// Subtotal is what the lines add up to, the voucher's discount not off yet.
func (c CheckedOrder) Subtotal() int64 {
	var sum int64
	for _, l := range c.Lines {
		sum += l.Price * int64(l.Qty)
	}
	return sum
}

// TotalLines are the lines as an order's totals read them.
func (c CheckedOrder) TotalLines() []OrderLine {
	out := make([]OrderLine, len(c.Lines))
	for i, l := range c.Lines {
		out[i] = OrderLine{Price: l.Price, Qty: l.Qty}
	}
	return out
}

// OrderRecord is an order the checkout took, as it is saved: its lines with
// the menus' names as they are, and its payment.
type OrderRecord struct {
	OrderID, OutletID, DeviceID, OrderNo, Type, CashierID string
	TableID, TableName, StandNumber                       *string
	VoucherID, VoucherCode                                *string
	OrderTotals
	ServiceBPS, TaxBPS int
	ClientCreatedAt    time.Time
	// RequestHash is the order as the device sent it, hashed: a resend has
	// the same.
	RequestHash string
	// Items are its lines in their order, each with its new ID.
	Items     []OrderItem
	PaymentID string
	Payment   OrderPayment
}

// OrderItemResponse is a line of an order, as the order endpoints send it.
type OrderItemResponse struct {
	OrderItemID string  `json:"order_item_id"`
	MenuID      string  `json:"menu_id"`
	MenuName    string  `json:"menu_name"`
	Price       int64   `json:"price"`
	Qty         int     `json:"qty"`
	Note        *string `json:"note"`
	LineTotal   int64   `json:"line_total"`
}

// OrderPaymentResponse is how an order was paid.
type OrderPaymentResponse struct {
	Method   string `json:"method"`
	Amount   int64  `json:"amount"`
	Received *int64 `json:"received"`
	Change   *int64 `json:"change"`
	Status   string `json:"status"`
}

// OrderResponse is an order as the order endpoints send it.
type OrderResponse struct {
	OrderID         string               `json:"order_id"`
	OrderNo         string               `json:"order_no"`
	DeviceID        string               `json:"device_id"`
	Type            string               `json:"type"`
	TableID         *string              `json:"table_id"`
	TableName       *string              `json:"table_name"`
	StandNumber     *string              `json:"stand_number"`
	Status          string               `json:"status"`
	Cashier         PersonResponse       `json:"cashier"`
	Items           []OrderItemResponse  `json:"items"`
	Subtotal        int64                `json:"subtotal"`
	VoucherCode     *string              `json:"voucher_code"`
	VoucherDiscount int64                `json:"voucher_discount"`
	ServiceBPS      int                  `json:"service_bps"`
	Service         int64                `json:"service"`
	TaxBPS          int                  `json:"tax_bps"`
	Tax             int64                `json:"tax"`
	Total           int64                `json:"total"`
	Payment         OrderPaymentResponse `json:"payment"`
	ClientCreatedAt time.Time            `json:"client_created_at"`
	CreatedAt       time.Time            `json:"created_at"`
	VoidedAt        *time.Time           `json:"voided_at"`
	VoidedBy        *PersonResponse      `json:"voided_by"`
	VoidReason      *string              `json:"void_reason"`
}

// NewOrderResponse returns the order's response, its times in UTC.
func NewOrderResponse(o Order) OrderResponse {
	out := OrderResponse{
		OrderID:         o.OrderID,
		OrderNo:         o.OrderNo,
		DeviceID:        o.DeviceID,
		Type:            o.Type,
		TableID:         o.TableID,
		TableName:       o.TableName,
		StandNumber:     o.StandNumber,
		Status:          o.Status,
		Cashier:         NewPersonResponse(o.Cashier),
		Items:           make([]OrderItemResponse, len(o.Items)),
		Subtotal:        o.Subtotal,
		VoucherCode:     o.VoucherCode,
		VoucherDiscount: o.Discount,
		ServiceBPS:      o.ServiceBPS,
		Service:         o.Service,
		TaxBPS:          o.TaxBPS,
		Tax:             o.Tax,
		Total:           o.Total,
		Payment:         OrderPaymentResponse(o.Payment),
		ClientCreatedAt: o.ClientCreatedAt.UTC(),
		CreatedAt:       o.CreatedAt.UTC(),
		VoidedAt:        utc(o.VoidedAt),
		VoidReason:      o.VoidReason,
	}
	for i, it := range o.Items {
		out.Items[i] = OrderItemResponse(it)
	}
	if o.VoidedBy != nil {
		p := NewPersonResponse(*o.VoidedBy)
		out.VoidedBy = &p
	}
	return out
}

// OrderSummaryResponse is an order in a list, as the order endpoints send
// it.
type OrderSummaryResponse struct {
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
	Cashier         PersonResponse `json:"cashier"`
	ClientCreatedAt time.Time      `json:"client_created_at"`
	CreatedAt       time.Time      `json:"created_at"`
}

// NewOrderSummaryResponse returns the summary's response, its times in UTC.
func NewOrderSummaryResponse(s OrderSummary) OrderSummaryResponse {
	return OrderSummaryResponse{
		OrderID: s.OrderID, OrderNo: s.OrderNo, DeviceID: s.DeviceID, Type: s.Type, TableName: s.TableName,
		StandNumber: s.StandNumber, Status: s.Status, Total: s.Total, ItemCount: s.ItemCount,
		PaymentMethod: s.PaymentMethod, Cashier: NewPersonResponse(s.Cashier),
		ClientCreatedAt: s.ClientCreatedAt.UTC(), CreatedAt: s.CreatedAt.UTC(),
	}
}
