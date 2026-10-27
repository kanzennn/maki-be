package order

import (
	"context"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/services"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// newVoucher has the Owner make a voucher: 10% off, 10 uses, for every
// outlet, from now for a year, as edit changes it.
func (h *harness) newVoucher(code string, edit func(*models.VoucherInput)) models.Voucher {
	h.t.Helper()
	in := models.VoucherInput{
		Code: code, Type: models.VoucherPercent, Value: ptr[int64](1000), Quota: ptr(10),
		ExpiredAt: ptr(time.Now().AddDate(1, 0, 0)), AllOutlets: true,
	}
	if edit != nil {
		edit(&in)
	}
	v, err := h.vouchers.Create(h.t.Context(), principals["owner"], "", in)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

// voucherByID returns the voucher as the Owner sees it.
func (h *harness) voucherByID(voucherID string) models.Voucher {
	h.t.Helper()
	v, err := h.vouchers.Get(h.t.Context(), voucherID)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

// withVoucher puts the code on the order from the till, with the total it
// comes to with the discount off. The cash handed over still covers it.
func withVoucher(tl *till, body map[string]any, code string, discount int64) map[string]any {
	var lines []models.OrderLine
	for _, l := range body["items"].([]map[string]any) {
		lines = append(lines, models.OrderLine{Price: l["price"].(int64), Qty: l["qty"].(int)})
	}
	service, tax := o1Service, o1Tax
	if tl.OutletID == "o2" {
		service, tax = 0, 0
	}
	body["voucher_code"] = code
	body["total"] = services.ComputeTotals(lines, discount, service, tax).Total
	return body
}

func TestSaleWithVoucher(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi susu", 18000, ptr(10))
	tl := h.till("o1", "Kasir depan")
	v := h.newVoucher("KOPI10", func(in *models.VoucherInput) {
		in.MaxDiscount, in.MinSpend = ptr[int64](5000), ptr[int64](20_000)
	})

	// 2 × 18,000 = 36,000, 10% off is 3,600, under the maximum; service 5% of
	// 32,400 = 1,620; tax 10% of 34,020 = 3,402; total 37,422.
	body := withVoucher(tl, h.order(tl, item(kopi, 2)), " kopi10 ", 3600)
	if body["total"] != int64(37_422) {
		t.Fatalf("total %v, want 37.422", body["total"])
	}
	o := h.sell(tl, "cashier", body)
	if o.VoucherCode == nil || *o.VoucherCode != "KOPI10" || o.Subtotal != 36_000 || o.VoucherDiscount != 3600 ||
		o.Service != 1620 || o.Tax != 3402 || o.Total != 37_422 || o.Payment.Amount != 37_422 {
		t.Errorf("order %+v", o)
	}
	if got := h.voucherByID(v.VoucherID); got.UsedCount != 1 || got.FirstUsedAt == nil {
		t.Errorf("used %d, first used %v", got.UsedCount, got.FirstUsedAt)
	}
	var voucherID, code string
	h.scalar(&voucherID, `SELECT voucher_id FROM orders WHERE order_id = $1`, o.OrderID)
	h.scalar(&code, `SELECT voucher_code FROM orders WHERE order_id = $1`, o.OrderID)
	if voucherID != v.VoucherID || code != "KOPI10" {
		t.Errorf("stored %s %s", voucherID, code)
	}
	var after map[string]any
	h.scalar(&after, `SELECT after_json FROM action_logs WHERE target_id = $1`, o.OrderID)
	if after["voucher_code"] != "KOPI10" || after["voucher_discount"] != float64(3600) || after["total"] != float64(37_422) {
		t.Errorf("logged %v", after)
	}
	var got models.OrderResponse
	h.do(http.MethodGet, ordersPath+"/"+o.OrderID, "cashier", "", nil).status(http.StatusOK, "").decode(&got)
	if got.VoucherCode == nil || *got.VoucherCode != "KOPI10" || got.VoucherDiscount != 3600 {
		t.Errorf("read back %v %d", got.VoucherCode, got.VoucherDiscount)
	}

	// The voucher's history has the sale.
	uses, total, err := h.vouchers.Uses(t.Context(), v.VoucherID, pagination.Page{Number: 1, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(uses) != 1 {
		t.Fatalf("uses %+v (%d)", uses, total)
	}
	if u := uses[0]; u.OrderID != o.OrderID || u.OrderNo != o.OrderNo || u.Outlet.OutletID != "o1" ||
		u.Outlet.Code != "BDG01" || u.Outlet.Name != "Bandung" || !u.At.Equal(h.clock) || u.Discount != 3600 ||
		u.Status != models.OrderPaid {
		t.Errorf("use %+v", u)
	}

	// Newest first: a second sale, at 30,000 off the maximum, 5,000.
	h.clock = h.clock.Add(time.Minute)
	second := h.sell(tl, "cashier", withVoucher(tl, h.order(tl, item(kopi, 3)), "KOPI10", 5000))
	uses, total, err = h.vouchers.Uses(t.Context(), v.VoucherID, pagination.Page{Number: 1, Size: 1})
	if err != nil || total != 2 || len(uses) != 1 || uses[0].OrderID != second.OrderID || uses[0].Discount != 5000 {
		t.Errorf("first page of 1: %+v (%d) %v, want the second sale", uses, total, err)
	}
	if got := h.voucherByID(v.VoucherID); got.UsedCount != 2 {
		t.Errorf("used %d, want 2", got.UsedCount)
	}

	// An order without one says so, and so does a blank code.
	plain := h.sell(tl, "cashier", h.order(tl, item(kopi, 1)))
	blank := h.order(tl, item(kopi, 1))
	blank["voucher_code"] = "  "
	for _, o := range []models.OrderResponse{plain, h.sell(tl, "cashier", blank)} {
		if o.VoucherCode != nil || o.VoucherDiscount != 0 {
			t.Errorf("without a voucher: %v %d", o.VoucherCode, o.VoucherDiscount)
		}
	}

	// A void gives the use back, the voucher turned off since or not; the
	// history keeps the sale, void, and the voucher stays used.
	if _, err := h.vouchers.SetActive(t.Context(), principals["owner"], "", v.VoucherID, false); err != nil {
		t.Fatal(err)
	}
	h.do(http.MethodPost, ordersPath+"/"+o.OrderID+"/void", "manager", "", map[string]any{"reason": "Salah input"}).
		status(http.StatusOK, "")
	if got := h.voucherByID(v.VoucherID); got.UsedCount != 1 || got.FirstUsedAt == nil {
		t.Errorf("after the void: used %d, first used %v, want the second sale's", got.UsedCount, got.FirstUsedAt)
	}
	h.do(http.MethodPost, ordersPath+"/"+o.OrderID+"/void", "manager", "", map[string]any{"reason": "Lagi"}).
		status(http.StatusConflict, "order_already_void")
	if got := h.voucherByID(v.VoucherID); got.UsedCount != 1 {
		t.Errorf("used %d after a second void, want 1", got.UsedCount)
	}
	uses, _, err = h.vouchers.Uses(t.Context(), v.VoucherID, pagination.Page{Number: 1, Size: 20})
	if err != nil || len(uses) != 2 || uses[0].Status != models.OrderPaid || uses[1].OrderID != o.OrderID || uses[1].Status != models.OrderVoid {
		t.Errorf("history after the void: %+v %v", uses, err)
	}
	// A void without a voucher gives nothing back.
	h.do(http.MethodPost, ordersPath+"/"+plain.OrderID+"/void", "manager", "", map[string]any{"reason": "Batal"}).
		status(http.StatusOK, "")
	if got := h.voucherByID(v.VoucherID); got.UsedCount != 1 {
		t.Errorf("used %d after voiding a sale without it, want 1", got.UsedCount)
	}
}

func TestSaleWithVoucherRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi susu", 18000, ptr(10))
	tl := h.till("o1", "Kasir depan")
	h.newVoucher("MIN50", func(in *models.VoucherInput) { in.MinSpend = ptr[int64](50_000) })
	h.newVoucher("JAKARTA", func(in *models.VoucherInput) { in.AllOutlets, in.OutletIDs = false, []string{"o2"} })
	h.newVoucher("MATI", func(in *models.VoucherInput) { in.Active = ptr(false) })
	full := h.newVoucher("HABIS", func(in *models.VoucherInput) { in.Quota = ptr(1) })
	h.sell(tl, "cashier", withVoucher(tl, h.order(tl, item(kopi, 1)), "HABIS", 1800))

	for code, want := range map[string]struct {
		status int
		code   string
	}{
		"TIDAKADA": {http.StatusNotFound, "voucher_not_found"},
		"MIN50":    {http.StatusConflict, "voucher_min_spend"},
		"JAKARTA":  {http.StatusConflict, "voucher_not_for_outlet"},
		"MATI":     {http.StatusConflict, "voucher_inactive"},
		"HABIS":    {http.StatusConflict, "voucher_used_up"},
	} {
		r := h.do(http.MethodPost, ordersPath, "cashier", tl.token, withVoucher(tl, h.order(tl, item(kopi, 2)), code, 3600))
		if r.Code != want.status || r.envelope().Error.Code != want.code {
			t.Errorf("%s: %d %s, want %d %s", code, r.Code, r.Body, want.status, want.code)
		}
		if code == "MIN50" && r.envelope().Error.Details["min_spend"] != float64(50_000) {
			t.Errorf("min spend refused without naming it: %s", r.Body)
		}
	}

	// A discount worked out from terms that changed: the server's total, and
	// no use taken.
	more := h.newVoucher("NAIK", func(in *models.VoucherInput) { in.Value = ptr[int64](2000) })
	r := h.do(http.MethodPost, ordersPath, "cashier", tl.token, withVoucher(tl, h.order(tl, item(kopi, 2)), "NAIK", 3600)).
		status(http.StatusConflict, "total_changed")
	if want := services.ComputeTotals([]models.OrderLine{{Price: 18000, Qty: 2}}, 7200, o1Service, o1Tax).Total; r.envelope().Error.Details["total"] != float64(want) {
		t.Errorf("total_changed %s, want %d", r.Body, want)
	}
	if got := h.voucherByID(more.VoucherID); got.UsedCount != 0 || got.FirstUsedAt != nil {
		t.Errorf("a refused sale took a use: %d, %v", got.UsedCount, got.FirstUsedAt)
	}

	// Nothing else was taken either: the one sale, and its stock.
	if n := h.count(`SELECT count(*) FROM orders`); n != 1 {
		t.Errorf("%d orders, want the one", n)
	}
	if got := h.stock(kopi.MenuID); got != 9 {
		t.Errorf("stock %d, want 9", got)
	}
	if got := h.voucherByID(full.VoucherID); got.UsedCount != 1 {
		t.Errorf("HABIS used %d", got.UsedCount)
	}
}

func TestResendWithVoucher(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi susu", 18000, nil)
	tl := h.till("o1", "Kasir depan")
	v := h.newVoucher("ULANG", nil)
	h.newVoucher("LAIN", nil)
	body := withVoucher(tl, h.order(tl, item(kopi, 2)), "ULANG", 3600)
	first := h.sell(tl, "cashier", body)

	// Sent again, in any case, it is the same order, and takes no second use.
	lower := maps.Clone(body)
	lower["voucher_code"] = " ulang"
	for _, b := range []map[string]any{body, lower} {
		var again models.OrderResponse
		h.do(http.MethodPost, ordersPath, "cashier", tl.token, b).status(http.StatusOK, "").decode(&again)
		if again.OrderID != first.OrderID || again.VoucherCode == nil || *again.VoucherCode != "ULANG" {
			t.Errorf("resent %+v", again)
		}
	}
	// With another voucher, or none, it is another order under the same ID.
	other := maps.Clone(body)
	other["voucher_code"] = "LAIN"
	none := maps.Clone(body)
	delete(none, "voucher_code")
	none["total"] = services.ComputeTotals([]models.OrderLine{{Price: 18000, Qty: 2}}, 0, o1Service, o1Tax).Total
	for _, b := range []map[string]any{other, none} {
		h.do(http.MethodPost, ordersPath, "cashier", tl.token, b).status(http.StatusConflict, "order_id_conflict")
	}
	if got := h.voucherByID(v.VoucherID); got.UsedCount != 1 {
		t.Errorf("used %d, want 1", got.UsedCount)
	}
}

func TestTwoOutletsTakeTheLastUse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	coffee := h.menu("o2", h.category("o2", "Drinks"), "Coffee", 30000, nil)
	bandung, jakarta := h.till("o1", "Kasir"), h.till("o2", "Kasir")
	v := h.newVoucher("TERAKHIR", func(in *models.VoucherInput) { in.Quota = ptr(1) })
	sales := []struct {
		tl   *till
		who  string
		body map[string]any
	}{
		{bandung, "cashier", withVoucher(bandung, h.order(bandung, item(kopi, 1)), "TERAKHIR", 1800)},
		{jakarta, "other", withVoucher(jakarta, h.order(jakarta, item(coffee, 1)), "TERAKHIR", 3000)},
	}

	// Hold the voucher, so both sales wait for it, then let them go at once.
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM vouchers WHERE voucher_id = $1 FOR UPDATE`, v.VoucherID); err != nil {
		t.Fatal(err)
	}
	results := make(chan reply, len(sales))
	for _, s := range sales {
		go func() {
			results <- h.do(http.MethodPost, "/api/v1/outlets/"+s.tl.OutletID+"/orders", s.who, s.tl.token, s.body)
		}()
	}
	h.waitForLock(2, waitingOn("vouchers"))
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var sold, refused int
	for range sales {
		r := <-results
		switch {
		case r.Code == http.StatusCreated:
			sold++
		case r.Code == http.StatusConflict && r.envelope().Error.Code == "voucher_used_up":
			refused++
		default:
			t.Errorf("status %d: %s", r.Code, r.Body)
		}
	}
	if sold != 1 || refused != 1 {
		t.Errorf("%d sold and %d refused, want exactly one of each", sold, refused)
	}
	if got := h.voucherByID(v.VoucherID); got.UsedCount != 1 {
		t.Errorf("used %d, want 1", got.UsedCount)
	}
	if n := h.count(`SELECT count(*) FROM orders WHERE voucher_id = $1`, v.VoucherID); n != 1 {
		t.Errorf("%d orders with the voucher, want 1", n)
	}
}
