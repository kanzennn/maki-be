package order

// MaxSubtotal is the most an order's lines may add up to, in Rupiah: a
// billion, so that every sum with a rate stays exact in JavaScript's
// numbers too, where the web cashier works it out.
const MaxSubtotal = 1_000_000_000

// Line is one line of an order, as its totals see it.
type Line struct {
	Price int64 // Rupiah
	Qty   int
}

// Totals are an order's amounts, in Rupiah.
type Totals struct {
	Subtotal int64
	Discount int64
	Service  int64
	Tax      int64
	Total    int64
}

// ComputeTotals works out an order's totals, the same way the web and
// Android cashiers do (api/test-vectors/order-totals.json) and the schema
// checks: the lines add up to the subtotal; the discount comes off it; the
// service charge is on what is left, and the tax on that plus the service,
// each rounded to the nearest rupiah, half up. The rates are in basis
// points. The caller has checked the amounts, so none is negative and
// nothing overflows: a subtotal of at most MaxSubtotal, at most 100% rates.
func ComputeTotals(lines []Line, discount int64, serviceBps, taxBps int) Totals {
	var t Totals
	for _, l := range lines {
		t.Subtotal += l.Price * int64(l.Qty)
	}
	t.Discount = discount
	base := t.Subtotal - discount
	t.Service = share(base, serviceBps)
	t.Tax = share(base+t.Service, taxBps)
	t.Total = base + t.Service + t.Tax
	return t
}

// share is bps basis points of amount, to the nearest rupiah, half up.
func share(amount int64, bps int) int64 {
	return (amount*int64(bps) + 5000) / 10000
}
