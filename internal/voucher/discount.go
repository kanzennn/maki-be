package voucher

// The kinds of discount a voucher gives.
const (
	// TypePercent is a share of the subtotal: Value is basis points.
	TypePercent = "percent"
	// TypeAmount is a sum of money: Value is rupiah.
	TypeAmount = "amount"
)

// Terms are what a voucher's discount on a subtotal depends on.
type Terms struct {
	Type string
	// Value is basis points (1000 is 10%) for TypePercent, rupiah for
	// TypeAmount.
	Value int64
	// MaxDiscount caps a percentage's discount, in rupiah; nil caps nothing.
	MaxDiscount *int64
	// MinSpend is the subtotal the voucher needs; nil needs none.
	MinSpend *int64
}

// Discount works out the voucher's discount on subtotal, the same way the
// web and Android cashiers do (api/test-vectors/voucher-discounts.json),
// before the service charge and the tax. applies is false for a subtotal
// below the minimum spend, which gets nothing.
//
// A percentage is rounded to the nearest rupiah, half up, as the order's
// service and tax are, then held to MaxDiscount; either kind is held to the
// subtotal, so a total never goes below zero. A subtotal is at most a
// billion and a percentage at most 100%, so nothing overflows.
func (t Terms) Discount(subtotal int64) (discount int64, applies bool) {
	if t.MinSpend != nil && subtotal < *t.MinSpend {
		return 0, false
	}
	switch t.Type {
	case TypePercent:
		discount = (subtotal*t.Value + 5000) / 10000
		if t.MaxDiscount != nil {
			discount = min(discount, *t.MaxDiscount)
		}
	case TypeAmount:
		discount = t.Value
	}
	return min(discount, subtotal), true
}
