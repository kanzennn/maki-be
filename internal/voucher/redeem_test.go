package voucher

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// validate checks the code from the till of o1 as its cashier.
func (h *harness) validate(token string, body map[string]any) response {
	h.t.Helper()
	return h.do(http.MethodPost, "/api/v1/outlets/o1/vouchers/validate", "cashier", token, body)
}

func TestValidateVoucher(t *testing.T) {
	h := newHarness(t)
	till := h.till("o1")
	h.create(h.body("KOPI", set("name", "Kopi Pagi", "min_spend", 50_000)))
	h.create(h.body("POTONG", set("name", nil, "type", TypeAmount, "value", 25_000)))

	var got validateResponse
	h.validate(till, map[string]any{"code": " kopi ", "subtotal": 100_000}).status(http.StatusOK, "").decode(&got)
	v := got.Voucher
	if v.Code != "KOPI" || v.VoucherID == "" || v.Name == nil || *v.Name != "Kopi Pagi" || v.Type != TypePercent ||
		v.Value != 1000 || v.MaxDiscount == nil || *v.MaxDiscount != 20_000 || v.MinSpend == nil || *v.MinSpend != 50_000 {
		t.Errorf("terms %+v", v)
	}
	if got.Discount != 10_000 || !got.Applies {
		t.Errorf("discount %d applies %v on 100.000, want 10.000", got.Discount, got.Applies)
	}
	for subtotal, want := range map[int64]validateResponse{
		300_000: {Discount: 20_000, Applies: true}, // the maximum
		50_000:  {Discount: 5_000, Applies: true},  // the minimum spend, just
		49_999:  {Discount: 0, Applies: false},     // short of it: not yet
		0:       {Discount: 0, Applies: false},
	} {
		h.validate(till, map[string]any{"code": "KOPI", "subtotal": subtotal}).status(http.StatusOK, "").decode(&got)
		if got.Discount != want.Discount || got.Applies != want.Applies {
			t.Errorf("on %d: discount %d applies %v, want %d %v", subtotal, got.Discount, got.Applies, want.Discount, want.Applies)
		}
	}
	h.validate(till, map[string]any{"code": "POTONG", "subtotal": 20_000}).status(http.StatusOK, "").decode(&got)
	if got.Discount != 20_000 || !got.Applies || got.Voucher.Name != nil || got.Voucher.MaxDiscount != nil || got.Voucher.MinSpend != nil {
		t.Errorf("an amount past the subtotal: %+v", got)
	}

	// Checking takes nothing.
	var used int
	h.scalar(&used, `SELECT coalesce(sum(used_count), 0) FROM vouchers`)
	if used != 0 {
		t.Errorf("%d uses taken by checking", used)
	}
}

func TestValidateVoucherRefuses(t *testing.T) {
	h := newHarness(t)
	till := h.till("o1")
	h.create(h.body("MATI", set("is_active", false)))
	h.create(h.body("NANTI", set("start_at", h.at(1, "08:00"))))
	h.create(h.body("LEWAT", set("expired_at", h.at(1, "06:00"))))
	h.create(h.body("HABIS", set("quota", 1)))
	if _, err := h.redeem("o2", "HABIS", 10_000); err != nil {
		t.Fatal(err)
	}
	h.create(h.body("JAKARTA", set("all_outlets", false, "outlet_ids", []string{"o2"})))
	// For another outlet, and inactive too: the outlet is the first reason.
	h.create(h.body("LAIN", set("all_outlets", false, "outlet_ids", []string{"o2", "o3"}, "is_active", false)))
	gone := h.create(h.body("HAPUS"))
	h.do(http.MethodDelete, "/api/v1/vouchers/"+gone.VoucherID, "owner", "", nil).status(http.StatusNoContent, "")
	h.clock = h.at(1, "07:00")

	for code, want := range map[string]struct {
		status int
		code   string
	}{
		"BELUMADA": {http.StatusNotFound, "voucher_not_found"},
		"HAPUS":    {http.StatusNotFound, "voucher_not_found"},
		"KO-PI":    {http.StatusNotFound, "voucher_not_found"},
		"MATI":     {http.StatusConflict, "voucher_inactive"},
		"NANTI":    {http.StatusConflict, "voucher_not_started"},
		"LEWAT":    {http.StatusConflict, "voucher_expired"},
		"HABIS":    {http.StatusConflict, "voucher_used_up"},
		"JAKARTA":  {http.StatusConflict, "voucher_not_for_outlet"},
		"LAIN":     {http.StatusConflict, "voucher_not_for_outlet"},
	} {
		r := h.validate(till, map[string]any{"code": code, "subtotal": 100_000})
		if r.Code != want.status || r.envelope().Error.Code != want.code {
			t.Errorf("%s: %d %s, want %d %s", code, r.Code, r.Body, want.status, want.code)
		}
	}
	// JAKARTA is fine in Jakarta.
	h.do(http.MethodPost, "/api/v1/outlets/o2/vouchers/validate", "cashier2", h.till("o2"),
		map[string]any{"code": "JAKARTA", "subtotal": 100_000}).status(http.StatusOK, "")
	h.clock = h.at(0, "08:00")
	h.validate(till, map[string]any{"code": "NANTI", "subtotal": 100_000}).status(http.StatusOK, "")

	// What the body needs.
	for _, tt := range []struct {
		body  map[string]any
		field string
		rule  string
	}{
		{map[string]any{"code": "NANTI"}, "subtotal", "required"},
		{map[string]any{"subtotal": 1}, "code", "required"},
		{map[string]any{"code": "  ", "subtotal": 1}, "code", "required"},
		{map[string]any{"code": "NANTI", "subtotal": -1}, "subtotal", "range"},
		{map[string]any{"code": "NANTI", "subtotal": MaxAmount + 1}, "subtotal", "range"},
	} {
		r := h.validate(till, tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
		if f := r.fields(); len(f) != 1 || f[tt.field] != tt.rule {
			t.Errorf("%v: fields %v, want %s %s", tt.body, f, tt.field, tt.rule)
		}
	}
}

func TestValidateVoucherNeedsTheOutletsDevice(t *testing.T) {
	h := newHarness(t)
	h.create(h.body("KOPI"))
	body := map[string]any{"code": "KOPI", "subtotal": 100_000}
	till := h.till("o1")

	h.validate("", body).status(http.StatusForbidden, "device_not_registered")
	h.validate("not-a-token", body).status(http.StatusForbidden, "device_not_registered")
	h.validate(h.till("o2"), body).status(http.StatusForbidden, "device_not_registered")
	d, revoked, err := h.devices.Activate(t.Context(), principals["manager"], "", "o1", "", "Lama", device.PlatformWeb)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.devices.Revoke(t.Context(), principals["manager"], "", "o1", d.DeviceID); err != nil {
		t.Fatal(err)
	}
	h.validate(revoked, body).status(http.StatusForbidden, "device_revoked")
	// Before the body is read: nothing about the vouchers comes back without
	// a device.
	h.validate("", map[string]any{"code": "BELUMADA", "subtotal": -1}).status(http.StatusForbidden, "device_not_registered")

	// Selling needs checkout.create, in the outlet.
	path := "/api/v1/outlets/o1/vouchers/validate"
	h.do(http.MethodPost, path, "staff", till, body).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodPost, path, "cashier2", till, body).status(http.StatusNotFound, "outlet_not_found")
	if r := h.do(http.MethodPost, path, "", till, body); r.Code != http.StatusUnauthorized {
		t.Errorf("nobody: %d %s", r.Code, r.Body)
	}
	h.do(http.MethodPost, path, "manager", till, body).status(http.StatusOK, "")
}

func TestRedeemAndRelease(t *testing.T) {
	h := newHarness(t)
	v := h.create(h.body("PROMO", set("min_spend", 50_000, "quota", 2, "all_outlets", false,
		"outlet_ids", []string{"o1", "o2"})))
	firstUse := h.clock

	taken, err := h.redeem("o1", " promo", 100_000)
	if err != nil {
		t.Fatal(err)
	}
	if taken != (Taken{VoucherID: v.VoucherID, Code: "PROMO", Discount: 10_000}) {
		t.Errorf("taken %+v", taken)
	}
	got := h.get(v.VoucherID)
	if got.UsedCount != 1 || got.FirstUsedAt == nil || !got.FirstUsedAt.Equal(firstUse) {
		t.Errorf("used %d, first used %v", got.UsedCount, got.FirstUsedAt)
	}

	// Short of the minimum spend, the sale gets an error naming it, and takes
	// nothing.
	_, err = h.redeem("o1", "PROMO", 49_999)
	coded, ok := errors.AsType[*httpx.Error](err)
	if !ok || coded.Code() != "voucher_min_spend" || coded.Status() != http.StatusConflict ||
		coded.Details()["min_spend"] != int64(50_000) {
		t.Errorf("short: %v %v", err, coded)
	}
	if _, err := h.redeem("o3", "PROMO", 100_000); !errors.Is(err, ErrVoucherNotForOutlet) {
		t.Errorf("another outlet: %v", err)
	}
	if _, err := h.redeem("o1", "TIDAKADA", 100_000); !errors.Is(err, ErrVoucherNotFound) {
		t.Errorf("unknown code: %v", err)
	}
	if got := h.get(v.VoucherID); got.UsedCount != 1 {
		t.Errorf("used %d after refusals, want 1", got.UsedCount)
	}

	h.clock = h.clock.Add(time.Hour)
	if taken, err = h.redeem("o2", "PROMO", 1_000_000); err != nil || taken.Discount != 20_000 {
		t.Errorf("o2: %+v %v, want the maximum, 20.000", taken, err)
	}
	if _, err := h.redeem("o1", "PROMO", 100_000); !errors.Is(err, ErrVoucherUsedUp) {
		t.Errorf("past the quota: %v", err)
	}
	h.release(v.VoucherID)
	if _, err := h.redeem("o1", "PROMO", 100_000); err != nil {
		t.Errorf("a use given back is taken again: %v", err)
	}
	h.release(v.VoucherID)
	h.release(v.VoucherID)
	// The first use stays the first.
	if got := h.get(v.VoucherID); got.UsedCount != 0 || got.FirstUsedAt == nil || !got.FirstUsedAt.Equal(firstUse) {
		t.Errorf("used %d, first used %v, want 0 and %v", got.UsedCount, got.FirstUsedAt, firstUse)
	}
}
