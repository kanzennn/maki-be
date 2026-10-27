package validations

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
)

// CreateOrderRequest is the body of POST /outlets/{oid}/orders. The caps
// here only bound what the server reads; CheckOrder applies the real rules,
// to the lines and the payment too.
type CreateOrderRequest struct {
	OrderID         string               `json:"order_id" binding:"required,max=64"`
	OrderNo         string               `json:"order_no" binding:"required,max=64"`
	Type            string               `json:"type" binding:"required,max=16"`
	TableID         *string              `json:"table_id" binding:"omitempty,max=64"`
	StandNumber     *string              `json:"stand_number" binding:"omitempty,max=64"`
	ClientCreatedAt *time.Time           `json:"client_created_at" binding:"required"`
	Items           []OrderItemRequest   `json:"items" binding:"required,max=200"`
	VoucherCode     *string              `json:"voucher_code" binding:"omitempty,max=64"`
	Total           *int64               `json:"total" binding:"required"`
	Payment         *OrderPaymentRequest `json:"payment" binding:"required"`
}

// OrderItemRequest is a line of CreateOrderRequest.
type OrderItemRequest struct {
	MenuID string  `json:"menu_id"`
	Qty    *int    `json:"qty"`
	Price  *int64  `json:"price"`
	Note   *string `json:"note"`
}

// OrderPaymentRequest is how CreateOrderRequest is paid.
type OrderPaymentRequest struct {
	Method   string `json:"method"`
	Received *int64 `json:"received"`
}

// Input converts the request.
func (r CreateOrderRequest) Input() models.NewOrder {
	in := models.NewOrder{
		OrderID: r.OrderID, OrderNo: r.OrderNo, Type: r.Type, TableID: r.TableID, StandNumber: r.StandNumber,
		ClientCreatedAt: r.ClientCreatedAt, Items: make([]models.NewOrderItem, len(r.Items)),
		VoucherCode: r.VoucherCode, Total: r.Total,
	}
	for i, it := range r.Items {
		in.Items[i] = models.NewOrderItem(it)
	}
	if r.Payment != nil {
		in.Payment = models.NewOrderPayment(*r.Payment)
	}
	return in
}

// VoidOrderRequest is the body of POST …/orders/{orid}/void.
type VoidOrderRequest struct {
	Reason string `json:"reason" binding:"required,max=1000"`
}

// How far the device's clock may be from the server's for an order sent
// online: a little ahead, or a day behind, for a resend.
const (
	maxAhead  = 5 * time.Minute
	maxBehind = 24 * time.Hour
)

// orderNoPattern is orders_order_no_check's rule, in parts: the outlet's
// code and the device's, the day, and the device's count that day.
var orderNoPattern = regexp.MustCompile(`^([A-Z0-9]{2,10}-K[0-9]{2,4})-([0-9]{8})-([0-9]{4,6})$`)

// standNumberPattern is orders_stand_number_check's rule: 1 to 10 capitals,
// digits and hyphens, a hyphen only between them, as 12, A5 or B-3.
var standNumberPattern = regexp.MustCompile(`^[A-Z0-9]([A-Z0-9-]{0,8}[A-Z0-9])?$`)

// CheckOrder validates an order as the device sent it, at the server's
// moment now: every problem at once as validation_failed, a line's fields
// by their path (items.0.qty). The receipt number is checked against the
// device and the outlet in the sale, which knows them.
func CheckOrder(in models.NewOrder, now time.Time) (models.CheckedOrder, error) {
	fields := map[string]string{}
	c := models.CheckedOrder{OrderID: in.OrderID, OrderNo: in.OrderNo, Type: in.Type, Method: in.Payment.Method}
	switch {
	case in.OrderID == "":
		fields["order_id"] = "required"
	case !id.Valid(in.OrderID):
		fields["order_id"] = "format"
	}
	switch m := orderNoPattern.FindStringSubmatch(in.OrderNo); {
	case in.OrderNo == "":
		fields["order_no"] = "required"
	case m == nil || strings.Trim(m[3], "0") == "":
		fields["order_no"] = "format"
	default:
		c.Prefix, c.Day = m[1], m[2]
	}
	switch in.Type {
	case models.OrderDineIn, models.OrderTakeaway:
	case "":
		fields["type"] = "required"
	default:
		fields["type"] = "oneof"
	}
	if tableID := trimmed(in.TableID); tableID != nil {
		if in.Type == models.OrderDineIn {
			c.TableID = *tableID
		} else {
			fields["table_id"] = "dine_in"
		}
	}
	// As typed on the stand, its small letters made capitals.
	if number := trimmed(in.StandNumber); number != nil {
		switch n := strings.ToUpper(*number); {
		case in.Type != models.OrderDineIn:
			fields["stand_number"] = "dine_in"
		case c.TableID != "":
			fields["stand_number"] = "with_table"
		case !standNumberPattern.MatchString(n):
			fields["stand_number"] = "format"
		default:
			c.StandNumber = n
		}
	}
	switch t := in.ClientCreatedAt; {
	case t == nil:
		fields["client_created_at"] = "required"
	case t.After(now.Add(maxAhead)) || t.Before(now.Add(-maxBehind)):
		fields["client_created_at"] = "range"
	default:
		// As the database keeps it, so a resend compares equal.
		c.CreatedAt = t.UTC().Truncate(time.Microsecond)
	}
	c.Lines = checkOrderLines(in.Items, fields)
	if code := trimmed(in.VoucherCode); code != nil {
		c.VoucherCode = NormalizeVoucherCode(*code)
	}
	switch {
	case in.Total == nil:
		fields["total"] = "required"
	case *in.Total < 0:
		fields["total"] = "range"
	default:
		c.Total = *in.Total
	}
	switch in.Payment.Method {
	case models.PaymentCash:
		switch r := in.Payment.Received; {
		case r == nil:
			fields["payment.received"] = "required"
		case *r < 0 || *r > models.MaxReceived:
			fields["payment.received"] = "range"
		case in.Total != nil && *r < *in.Total:
			fields["payment.received"] = "short"
		default:
			c.Received = r
		}
	case models.PaymentQRIS, models.PaymentDebit:
	case "":
		fields["payment.method"] = "required"
	default:
		fields["payment.method"] = "oneof"
	}
	if len(fields) > 0 {
		return models.CheckedOrder{}, apperror.Invalid(fields)
	}
	return c, nil
}

// checkOrderLines validates the order's lines into fields, and returns them
// when they all pass. Together they come to at most MaxSubtotal.
func checkOrderLines(items []models.NewOrderItem, fields map[string]string) []models.CheckedLine {
	switch {
	case len(items) == 0:
		fields["items"] = "required"
		return nil
	case len(items) > models.MaxOrderLines:
		fields["items"] = "max"
		return nil
	}
	lines := make([]models.CheckedLine, len(items))
	broken := false
	fail := func(i int, field, rule string) {
		fields["items."+strconv.Itoa(i)+"."+field] = rule
		broken = true
	}
	for i, it := range items {
		l := models.CheckedLine{MenuID: it.MenuID, Note: trimmed(it.Note)}
		switch {
		case it.MenuID == "":
			fail(i, "menu_id", "required")
		case !id.Valid(it.MenuID):
			fail(i, "menu_id", "format")
		}
		switch {
		case it.Qty == nil:
			fail(i, "qty", "required")
		case *it.Qty < 1 || *it.Qty > models.MaxOrderQty:
			fail(i, "qty", "range")
		default:
			l.Qty = *it.Qty
		}
		switch {
		case it.Price == nil:
			fail(i, "price", "required")
		case *it.Price < 0 || *it.Price > models.MaxPrice:
			fail(i, "price", "range")
		default:
			l.Price = *it.Price
		}
		if l.Note != nil && utf8.RuneCountInString(*l.Note) > models.MaxOrderNote {
			fail(i, "note", "max")
		}
		lines[i] = l
	}
	if broken {
		return nil
	}
	var subtotal int64
	for _, l := range lines {
		subtotal += l.Price * int64(l.Qty)
	}
	if subtotal > models.MaxSubtotal {
		fields["items"] = "range"
		return nil
	}
	return lines
}

// CheckVoidReason validates a void's reason, and returns it trimmed: 1 to
// MaxVoidReason characters.
func CheckVoidReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	switch n := utf8.RuneCountInString(reason); {
	case n == 0:
		return "", apperror.Invalid(map[string]string{"reason": "required"})
	case n > models.MaxVoidReason:
		return "", apperror.Invalid(map[string]string{"reason": "max"})
	}
	return reason, nil
}
