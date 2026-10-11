package voucher

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/outlet"
)

func TestCreateVoucher(t *testing.T) {
	h := newHarness(t)
	expiry := h.at(30, "23:59")

	v := h.create(h.body("  merdeka17 ", set("name", "  Diskon Merdeka  ", "min_spend", 50_000)))
	if v.Code != "MERDEKA17" || v.Name == nil || *v.Name != "Diskon Merdeka" {
		t.Errorf("code %q, name %v: want the code in capitals, both trimmed", v.Code, v.Name)
	}
	if v.Type != TypePercent || v.Value != 1000 || v.MaxDiscount == nil || *v.MaxDiscount != 20_000 ||
		v.MinSpend == nil || *v.MinSpend != 50_000 {
		t.Errorf("terms %s %d max %v min %v", v.Type, v.Value, v.MaxDiscount, v.MinSpend)
	}
	if v.Quota != 100 || v.UsedCount != 0 || v.StartAt != nil || !v.ExpiredAt.Equal(expiry) {
		t.Errorf("quota %d used %d start %v expiry %v, want 100, 0, none, %v", v.Quota, v.UsedCount, v.StartAt, v.ExpiredAt, expiry)
	}
	if !v.AllOutlets || v.Outlets == nil || len(v.Outlets) != 0 {
		t.Errorf("all outlets %v, outlets %v: want every outlet, as an empty list", v.AllOutlets, v.Outlets)
	}
	if !v.IsActive || v.Status != StatusActive || v.FirstUsedAt != nil {
		t.Errorf("active %v, status %s, first used %v", v.IsActive, v.Status, v.FirstUsedAt)
	}
	if v.ExpiredAt.Location() != time.UTC {
		t.Errorf("expiry in %v, want UTC", v.ExpiredAt.Location())
	}
	if h.get(v.VoucherID).Code != "MERDEKA17" {
		t.Error("the voucher created isn't the one stored")
	}

	log := h.log(v.VoucherID)
	if len(log) != 1 || log[0].Action != "voucher.created" || log[0].UserID == nil || *log[0].UserID != "owner" ||
		log[0].OutletID != nil {
		t.Fatalf("log %+v, want voucher.created by the Owner, in no outlet", log)
	}
	after := log[0].After
	if after["code"] != "MERDEKA17" || after["value"] != float64(1000) || after["max_discount"] != float64(20_000) ||
		after["min_spend"] != float64(50_000) || after["quota"] != float64(100) || after["start_at"] != nil ||
		after["expired_at"] != expiry.UTC().Format(time.RFC3339) || after["all_outlets"] != true ||
		after["outlet_ids"] != nil || after["is_active"] != true {
		t.Errorf("logged %v", after)
	}

	// An amount for some outlets: they come by name, each once, and an
	// amount has no maximum.
	some := h.create(h.body("HEMAT5", set("type", TypeAmount, "value", 5000, "all_outlets", false,
		"outlet_ids", []string{"o3", "o1", "o3"}, "start_at", h.at(1, "08:00"), "is_active", false)))
	if some.AllOutlets || len(some.Outlets) != 2 || some.Outlets[0].Code != "ACH01" || some.Outlets[1].OutletID != "o1" ||
		some.Outlets[1].Name != "Bandung" {
		t.Errorf("outlets %+v, want aceh and Bandung", some.Outlets)
	}
	if some.MaxDiscount != nil || some.Value != 5000 {
		t.Errorf("an amount of %d keeps max %v", some.Value, some.MaxDiscount)
	}
	if some.IsActive || some.Status != StatusInactive || some.StartAt == nil || !some.StartAt.Equal(h.at(1, "08:00")) {
		t.Errorf("active %v status %s start %v, want inactive from tomorrow 08:00", some.IsActive, some.Status, some.StartAt)
	}
	var pairs int
	h.scalar(&pairs, `SELECT count(*) FROM voucher_outlets WHERE voucher_id = $1`, some.VoucherID)
	if pairs != 2 {
		t.Errorf("%d voucher_outlets, want 2", pairs)
	}

	// A list of outlets for a voucher for all of them is dropped.
	all := h.create(h.body("SEMUA", set("outlet_ids", []string{"o1"})))
	if !all.AllOutlets || len(all.Outlets) != 0 {
		t.Errorf("all outlets %v with %v", all.AllOutlets, all.Outlets)
	}
	h.scalar(&pairs, `SELECT count(*) FROM voucher_outlets WHERE voucher_id = $1`, all.VoucherID)
	if pairs != 0 {
		t.Errorf("%d voucher_outlets for a voucher for every outlet", pairs)
	}

	// Times are kept to the second.
	odd := h.create(h.body("DETIK", set("expired_at", h.at(2, "10:00").Add(1500*time.Millisecond))))
	if !odd.ExpiredAt.Equal(h.at(2, "10:00").Add(time.Second)) {
		t.Errorf("expiry %v, want to the second", odd.ExpiredAt)
	}
}

func TestCreateVoucherRejects(t *testing.T) {
	h := newHarness(t)
	h.exec(`UPDATE outlets SET deleted_at = now(), status = 'inactive' WHERE outlet_id = 'o3'`)
	long := strings.Repeat("a", MaxName+1)
	tests := []struct {
		name string
		body map[string]any
		want map[string]string
	}{
		{"blank code", h.body("   "), map[string]string{"code": "required"}},
		{"short code", h.body("AB"), map[string]string{"code": "format"}},
		{"long code", h.body(strings.Repeat("A", 21)), map[string]string{"code": "format"}},
		{"code with a dash", h.body("HEMAT-5"), map[string]string{"code": "format"}},
		{"long name", h.body("NAMA", set("name", long)), map[string]string{"name": "max"}},
		{"no type", h.body("TIPE", set("type", " ")), map[string]string{"type": "oneof"}},
		{"other type", h.body("TIPE", set("type", "free")), map[string]string{"type": "oneof"}},
		{"no percent", h.body("NILAI", set("value", 0)), map[string]string{"value": "range"}},
		{"over 100%", h.body("NILAI", set("value", MaxPercentBPS+1)), map[string]string{"value": "range"}},
		{"no amount", h.body("NILAI", set("type", TypeAmount, "value", 0)), map[string]string{"value": "range"}},
		{"amount too big", h.body("NILAI", set("type", TypeAmount, "value", MaxAmount+1)), map[string]string{"value": "range"}},
		{"no maximum", h.body("MAKS", set("max_discount", 0)), map[string]string{"max_discount": "range"}},
		{"maximum too big", h.body("MAKS", set("max_discount", MaxAmount+1)), map[string]string{"max_discount": "range"}},
		{"no minimum", h.body("MIN", set("min_spend", 0)), map[string]string{"min_spend": "range"}},
		{"minimum too big", h.body("MIN", set("min_spend", MaxAmount+1)), map[string]string{"min_spend": "range"}},
		{"no quota", h.body("KUOTA", set("quota", 0)), map[string]string{"quota": "range"}},
		{"quota too big", h.body("KUOTA", set("quota", MaxQuota+1)), map[string]string{"quota": "range"}},
		{"expired", h.body("LALU", set("expired_at", h.clock)), map[string]string{"expired_at": "future"}},
		{"ends as it starts", h.body("PERIODE", set("start_at", h.at(3, "10:00"), "expired_at", h.at(3, "10:00"))),
			map[string]string{"expired_at": "after"}},
		{"no outlets", h.body("OUTLET", set("all_outlets", false)), map[string]string{"outlet_ids": "required"}},
		{"empty outlets", h.body("OUTLET", set("all_outlets", false, "outlet_ids", []string{})), map[string]string{"outlet_ids": "required"}},
		{"unknown outlet", h.body("OUTLET", set("all_outlets", false, "outlet_ids", []string{"o1", "o9"})),
			map[string]string{"outlet_ids": "exists"}},
		{"deleted outlet", h.body("OUTLET", set("all_outlets", false, "outlet_ids", []string{"o3"})),
			map[string]string{"outlet_ids": "exists"}},
		{"every problem at once", h.body("x", set("type", TypeAmount, "value", -1, "quota", -1, "min_spend", -1,
			"expired_at", h.clock.Add(-time.Hour), "all_outlets", false)),
			map[string]string{"code": "format", "value": "range", "quota": "range", "min_spend": "range",
				"expired_at": "future", "outlet_ids": "required"}},
		// The binding's own: what JSON can leave out.
		{"missing fields", h.body("", without("type", "value", "quota", "expired_at", "all_outlets")),
			map[string]string{"code": "required", "type": "required", "value": "required", "quota": "required",
				"expired_at": "required", "all_outlets": "required"}},
	}
	for _, tt := range tests {
		r := h.do(http.MethodPost, "/api/v1/vouchers", "owner", "", tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
		got := r.fields()
		if len(got) != len(tt.want) {
			t.Errorf("%s: fields %v, want %v", tt.name, got, tt.want)
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("%s: %s = %v, want %s", tt.name, k, got[k], v)
			}
		}
	}
	var n int
	h.scalar(&n, `SELECT count(*) FROM vouchers`)
	if n != 0 {
		t.Errorf("%d vouchers stored from refused bodies", n)
	}
}

func TestVoucherCodesAreUnique(t *testing.T) {
	h := newHarness(t)
	first := h.create(h.body("KOPI10"))
	h.do(http.MethodPost, "/api/v1/vouchers", "owner", "", h.body("kopi10")).status(http.StatusConflict, "voucher_code_taken")
	other := h.create(h.body("TEH10"))
	h.do(http.MethodPut, "/api/v1/vouchers/"+other.VoucherID, "owner", "", h.body("KOPI10")).
		status(http.StatusConflict, "voucher_code_taken")

	// A deleted voucher was never used, so its code is free again.
	h.do(http.MethodDelete, "/api/v1/vouchers/"+first.VoucherID, "owner", "", nil).status(http.StatusNoContent, "")
	again := h.create(h.body("KOPI10"))
	if again.VoucherID == first.VoucherID {
		t.Error("the deleted voucher came back")
	}
}

func TestOnlyTheOwnerManagesVouchers(t *testing.T) {
	h := newHarness(t)
	v := h.create(h.body("OWNER"))
	for _, who := range []string{"manager", "cashier", "staff"} {
		for _, r := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/vouchers"},
			{http.MethodPost, "/api/v1/vouchers"},
			{http.MethodGet, "/api/v1/vouchers/" + v.VoucherID},
			{http.MethodPut, "/api/v1/vouchers/" + v.VoucherID},
			{http.MethodPatch, "/api/v1/vouchers/" + v.VoucherID + "/status"},
			{http.MethodDelete, "/api/v1/vouchers/" + v.VoucherID},
			{http.MethodGet, "/api/v1/vouchers/" + v.VoucherID + "/redemptions"},
		} {
			h.do(r.method, r.path, who, "", h.body("X"+who)).status(http.StatusForbidden, "forbidden")
		}
	}
	if h.get(v.VoucherID).Code != "OWNER" {
		t.Error("the voucher changed")
	}
}

func TestListVouchers(t *testing.T) {
	h := newHarness(t)
	// One of each status. The list puts the ones that run out last first.
	active := h.create(h.body("AKTIF", set("name", "Promo 100%", "expired_at", h.at(30, "23:59"))))
	scheduled := h.create(h.body("NANTI", set("start_at", h.at(5, "08:00"), "expired_at", h.at(40, "23:59"))))
	usedUp := h.create(h.body("HABIS", set("quota", 1, "expired_at", h.at(20, "23:59"))))
	if _, err := h.redeem("o1", "HABIS", 50_000); err != nil {
		t.Fatal(err)
	}
	expired := h.create(h.body("LEWAT", set("expired_at", h.at(1, "10:00"))))
	inactive := h.create(h.body("MATI", set("is_active", false, "expired_at", h.at(10, "23:59"))))
	deleted := h.create(h.body("HAPUS", set("expired_at", h.at(50, "23:59"))))
	h.do(http.MethodDelete, "/api/v1/vouchers/"+deleted.VoucherID, "owner", "", nil).status(http.StatusNoContent, "")
	h.clock = h.at(2, "09:00")

	l := h.do(http.MethodGet, "/api/v1/vouchers", "owner", "", nil).status(http.StatusOK, "").list()
	if want := []string{"NANTI", "AKTIF", "HABIS", "MATI", "LEWAT"}; !slices.Equal(codes(l.Data), want) {
		t.Fatalf("list %v, want %v", codes(l.Data), want)
	}
	if l.Meta.Total != 5 || l.Meta.Page != 1 || l.Meta.PageSize != 20 {
		t.Errorf("meta %+v", l.Meta)
	}
	for _, tt := range []struct {
		status string
		want   voucherResponse
	}{
		{StatusActive, active}, {StatusScheduled, scheduled}, {StatusUsedUp, usedUp},
		{StatusExpired, expired}, {StatusInactive, inactive},
	} {
		l := h.do(http.MethodGet, "/api/v1/vouchers?status="+tt.status, "owner", "", nil).status(http.StatusOK, "").list()
		if len(l.Data) != 1 || l.Data[0].VoucherID != tt.want.VoucherID || l.Data[0].Status != tt.status || l.Meta.Total != 1 {
			t.Errorf("status %s: %v (total %d), want %s", tt.status, codes(l.Data), l.Meta.Total, tt.want.Code)
		}
	}

	// A search matches the code or the name, in any case, and LIKE's
	// wildcards are only themselves.
	for q, want := range map[string][]string{
		"akt":  {"AKTIF"},
		"100%": {"AKTIF"},
		"%":    {"AKTIF"},
		"_":    nil,
		"diSK": {"NANTI", "HABIS", "MATI", "LEWAT"},
	} {
		l := h.do(http.MethodGet, "/api/v1/vouchers?q="+url.QueryEscape(q), "owner", "", nil).status(http.StatusOK, "").list()
		if !slices.Equal(codes(l.Data), want) {
			t.Errorf("q=%s: %v, want %v", q, codes(l.Data), want)
		}
	}
	l = h.do(http.MethodGet, "/api/v1/vouchers?status=active&q=nanti", "owner", "", nil).status(http.StatusOK, "").list()
	if len(l.Data) != 0 || l.Meta.Total != 0 {
		t.Errorf("filters together: %v", codes(l.Data))
	}

	l = h.do(http.MethodGet, "/api/v1/vouchers?page=2&page_size=2", "owner", "", nil).status(http.StatusOK, "").list()
	if !slices.Equal(codes(l.Data), []string{"HABIS", "MATI"}) || l.Meta.Total != 5 || l.Meta.Page != 2 {
		t.Errorf("page 2: %v %+v", codes(l.Data), l.Meta)
	}
	if r := h.do(http.MethodGet, "/api/v1/vouchers?status=deleted", "owner", "", nil); r.Code != http.StatusUnprocessableEntity ||
		r.fields()["status"] != "oneof" {
		t.Errorf("status=deleted: %d %s", r.Code, r.Body)
	}
	h.do(http.MethodGet, "/api/v1/vouchers?page=0", "owner", "", nil).status(http.StatusUnprocessableEntity, "validation_failed")
}

func TestVoucherStatuses(t *testing.T) {
	h := newHarness(t)
	start, end := h.at(1, "08:00"), h.at(3, "22:00")
	v := h.create(h.body("WAKTU", set("quota", 2, "start_at", start, "expired_at", end)))
	status := func(want string) {
		t.Helper()
		if got := h.get(v.VoucherID).Status; got != want {
			t.Errorf("at %v: status %s, want %s", h.clock.In(jakarta), got, want)
		}
	}
	status(StatusScheduled)
	h.clock = start.Add(-time.Second)
	status(StatusScheduled)
	h.clock = start
	status(StatusActive)
	if _, err := h.redeem("o1", "WAKTU", 10_000); err != nil {
		t.Fatal(err)
	}
	status(StatusActive)
	if _, err := h.redeem("o2", "WAKTU", 10_000); err != nil {
		t.Fatal(err)
	}
	status(StatusUsedUp)
	h.release(v.VoucherID)
	status(StatusActive)
	h.do(http.MethodPatch, "/api/v1/vouchers/"+v.VoucherID+"/status", "owner", "", map[string]any{"is_active": false}).
		status(http.StatusOK, "")
	status(StatusInactive)
	h.clock = end
	status(StatusInactive) // inactive before expired
	h.do(http.MethodPatch, "/api/v1/vouchers/"+v.VoucherID+"/status", "owner", "", map[string]any{"is_active": true}).
		status(http.StatusOK, "")
	status(StatusExpired)
	if _, err := h.redeem("o1", "WAKTU", 10_000); !errors.Is(err, ErrVoucherExpired) {
		t.Errorf("redeem after expiry: %v", err)
	}
	h.clock = end.Add(-time.Second)
	if _, err := h.redeem("o1", "WAKTU", 10_000); err != nil {
		t.Fatal(err)
	}
	status(StatusUsedUp)
	h.clock = end
	status(StatusExpired) // expired before used up
}

func TestManagerListsTheirOutletsVouchers(t *testing.T) {
	h := newHarness(t)
	h.create(h.body("SEMUA", set("expired_at", h.at(30, "23:59"))))
	h.create(h.body("BERDUA", set("all_outlets", false, "outlet_ids", []string{"o1", "o2"}, "expired_at", h.at(20, "23:59"))))
	h.create(h.body("JAKARTA", set("all_outlets", false, "outlet_ids", []string{"o2"}, "expired_at", h.at(15, "23:59"))))
	h.create(h.body("MATI", set("is_active", false, "all_outlets", false, "outlet_ids", []string{"o1"},
		"expired_at", h.at(10, "23:59"))))

	l := h.do(http.MethodGet, "/api/v1/outlets/o1/vouchers", "manager", "", nil).status(http.StatusOK, "").list()
	if want := []string{"SEMUA", "BERDUA", "MATI"}; !slices.Equal(codes(l.Data), want) || l.Meta.Total != 3 {
		t.Fatalf("o1's vouchers %v (total %d), want %v", codes(l.Data), l.Meta.Total, want)
	}
	// Of the outlets a voucher is for, the Manager sees only theirs.
	both := l.Data[1]
	if both.AllOutlets || len(both.Outlets) != 1 || both.Outlets[0].OutletID != "o1" || both.Outlets[0].Name != "Bandung" {
		t.Errorf("BERDUA for %+v, want only Bandung", both.Outlets)
	}
	if !l.Data[0].AllOutlets || len(l.Data[0].Outlets) != 0 {
		t.Errorf("SEMUA for %v, %+v", l.Data[0].AllOutlets, l.Data[0].Outlets)
	}

	l = h.do(http.MethodGet, "/api/v1/outlets/o1/vouchers?status=inactive", "manager", "", nil).status(http.StatusOK, "").list()
	if !slices.Equal(codes(l.Data), []string{"MATI"}) {
		t.Errorf("inactive: %v", codes(l.Data))
	}
	l = h.do(http.MethodGet, "/api/v1/outlets/o1/vouchers?q=dua", "manager", "", nil).status(http.StatusOK, "").list()
	if !slices.Equal(codes(l.Data), []string{"BERDUA"}) {
		t.Errorf("q=dua: %v", codes(l.Data))
	}
	l = h.do(http.MethodGet, "/api/v1/outlets/o2/vouchers", "manager2", "", nil).status(http.StatusOK, "").list()
	if want := []string{"SEMUA", "BERDUA", "JAKARTA"}; !slices.Equal(codes(l.Data), want) {
		t.Errorf("o2's vouchers %v, want %v", codes(l.Data), want)
	}
	// The Owner looks into any outlet.
	l = h.do(http.MethodGet, "/api/v1/outlets/o2/vouchers", "owner", "", nil).status(http.StatusOK, "").list()
	if len(l.Data) != 3 {
		t.Errorf("the Owner sees %v in o2", codes(l.Data))
	}

	h.do(http.MethodGet, "/api/v1/outlets/o1/vouchers", "manager2", "", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, "/api/v1/outlets/o1/vouchers", "cashier", "", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o1/vouchers?status=x", "manager", "", nil).
		status(http.StatusUnprocessableEntity, "validation_failed")
}

func TestUpdateVoucher(t *testing.T) {
	h := newHarness(t)
	v := h.create(h.body("AWAL", set("min_spend", 30_000)))

	// Before any sale used it, everything may change: the code, the type, the
	// quota down, the expiry earlier, the outlets.
	var got voucherResponse
	h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("baru", set("name", nil,
		"type", TypeAmount, "value", 15_000, "quota", 10, "expired_at", h.at(7, "21:00"),
		"start_at", h.at(1, "06:00"), "all_outlets", false, "outlet_ids", []string{"o2"}, "is_active", false))).
		status(http.StatusOK, "").decode(&got)
	if got.Code != "BARU" || got.Name != nil || got.Type != TypeAmount || got.Value != 15_000 || got.MaxDiscount != nil ||
		got.MinSpend != nil || got.Quota != 10 || !got.ExpiredAt.Equal(h.at(7, "21:00")) || got.StartAt == nil ||
		got.AllOutlets || len(got.Outlets) != 1 || got.Outlets[0].OutletID != "o2" {
		t.Fatalf("updated %+v", got)
	}
	// The status isn't the form's.
	if !got.IsActive || got.Status != StatusScheduled {
		t.Errorf("active %v, status %s: is_active changed by PUT", got.IsActive, got.Status)
	}
	if !got.UpdatedAt.After(v.UpdatedAt) && !got.UpdatedAt.Equal(v.UpdatedAt) {
		t.Errorf("updated_at %v before %v", got.UpdatedAt, v.UpdatedAt)
	}
	log := h.log(v.VoucherID)
	if !slices.Equal(actions(log), []string{"voucher.created", "voucher.updated"}) {
		t.Fatalf("log %v", actions(log))
	}
	before, after := log[1].Before, log[1].After
	if before["code"] != "AWAL" || after["code"] != "BARU" || before["name"] != "Diskon AWAL" || after["name"] != nil ||
		after["type"] != TypeAmount || after["max_discount"] != nil || before["max_discount"] != float64(20_000) ||
		before["min_spend"] != float64(30_000) || after["all_outlets"] != false || before["outlet_ids"] != nil ||
		fmt.Sprint(after["outlet_ids"]) != "[o2]" || after["start_at"] != h.at(1, "06:00").UTC().Format(time.RFC3339) {
		t.Errorf("logged %v → %v", before, after)
	}
	if _, ok := after["is_active"]; ok {
		t.Errorf("logged is_active %v", after)
	}

	// Back to every outlet: the list goes.
	h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("BARU", set("name", nil,
		"type", TypeAmount, "value", 15_000, "quota", 10, "expired_at", h.at(7, "21:00"), "start_at", h.at(1, "06:00")))).
		status(http.StatusOK, "").decode(&got)
	var pairs int
	h.scalar(&pairs, `SELECT count(*) FROM voucher_outlets WHERE voucher_id = $1`, v.VoucherID)
	if !got.AllOutlets || len(got.Outlets) != 0 || pairs != 0 {
		t.Errorf("all outlets %v, outlets %v, %d rows", got.AllOutlets, got.Outlets, pairs)
	}
	log = h.log(v.VoucherID)
	if len(log) != 3 || len(log[2].After) != 2 || log[2].After["all_outlets"] != true || log[2].After["outlet_ids"] != nil {
		t.Errorf("logged %v, want only the outlets", log[len(log)-1].After)
	}

	// The same again records nothing.
	h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("BARU", set("name", "  ",
		"type", TypeAmount, "value", 15_000, "max_discount", 1, "quota", 10, "expired_at", h.at(7, "21:00"),
		"start_at", h.at(1, "06:00")))).status(http.StatusOK, "")
	if n := len(h.log(v.VoucherID)); n != 3 {
		t.Errorf("%d entries after a change of nothing, want 3", n)
	}

	h.do(http.MethodPut, "/api/v1/vouchers/v404", "owner", "", h.body("BARU")).status(http.StatusNotFound, "voucher_not_found")
	h.do(http.MethodPut, "/api/v1/vouchers/v404", "owner", "", h.body("x")).status(http.StatusNotFound, "voucher_not_found")
	r := h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("BARU",
		set("expired_at", h.clock.Add(-time.Minute), "quota", 0))).status(http.StatusUnprocessableEntity, "validation_failed")
	if f := r.fields(); len(f) != 2 || f["expired_at"] != "future" || f["quota"] != "range" {
		t.Errorf("fields %v", f)
	}
}

func TestUpdateUsedVoucher(t *testing.T) {
	h := newHarness(t)
	v := h.create(h.body("PAKAI", set("quota", 5, "expired_at", h.at(10, "23:59"))))
	if _, err := h.redeem("o1", "PAKAI", 100_000); err != nil {
		t.Fatal(err)
	}
	// Given back, it stays used.
	h.release(v.VoucherID)
	if got := h.get(v.VoucherID); got.UsedCount != 0 || got.FirstUsedAt == nil || !got.FirstUsedAt.Equal(h.clock) {
		t.Fatalf("used %d, first used %v: want 0 uses, used at %v", got.UsedCount, got.FirstUsedAt, h.clock)
	}
	h.clock = h.clock.Add(time.Hour)

	// The code and the type stay, the quota doesn't go down, and the expiry
	// doesn't move earlier: each refused, all at once.
	r := h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("LAIN", set("type", TypeAmount,
		"value", 5000, "quota", 4, "expired_at", h.at(10, "23:58")))).status(http.StatusUnprocessableEntity, "validation_failed")
	if f := r.fields(); len(f) != 4 || f["code"] != "locked" || f["type"] != "locked" || f["quota"] != "decrease" ||
		f["expired_at"] != "earlier" {
		t.Errorf("fields %v", f)
	}
	// The code compares as stored.
	h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body(" pakai ", set("quota", 5,
		"expired_at", h.at(10, "23:59")))).status(http.StatusOK, "")

	// Everything else may change: the terms, more uses, a later expiry, the
	// outlets.
	var got voucherResponse
	h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("PAKAI", set("name", "Baru",
		"value", 1500, "max_discount", nil, "min_spend", 25_000, "quota", 50, "start_at", h.at(-1, "00:00"),
		"expired_at", h.at(40, "23:59"), "all_outlets", false, "outlet_ids", []string{"o1"}))).
		status(http.StatusOK, "").decode(&got)
	if got.Value != 1500 || got.MaxDiscount != nil || got.Quota != 50 || !got.ExpiredAt.Equal(h.at(40, "23:59")) ||
		got.AllOutlets || got.Status != StatusActive {
		t.Errorf("updated %+v", got)
	}

	// Expired, it may be extended, into the future only.
	h.clock = h.at(41, "08:00")
	r = h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("PAKAI", set("name", "Baru",
		"value", 1500, "max_discount", nil, "min_spend", 25_000, "quota", 50, "start_at", h.at(-42, "00:00"),
		"expired_at", h.at(0, "07:00"), "all_outlets", false, "outlet_ids", []string{"o1"}))).
		status(http.StatusUnprocessableEntity, "validation_failed")
	if f := r.fields(); len(f) != 1 || f["expired_at"] != "future" {
		t.Errorf("fields %v", f)
	}
	// An expired voucher's other fields change without moving its expiry.
	h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("PAKAI", set("name", "Lama",
		"value", 1500, "max_discount", nil, "min_spend", 25_000, "quota", 50, "start_at", h.at(-42, "00:00"),
		"expired_at", h.at(-1, "23:59"), "all_outlets", false, "outlet_ids", []string{"o1"}))).
		status(http.StatusOK, "").decode(&got)
	if got.Name == nil || *got.Name != "Lama" || got.Status != StatusExpired {
		t.Errorf("name %v, status %s", got.Name, got.Status)
	}
	h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("PAKAI", set("name", "Lama",
		"value", 1500, "max_discount", nil, "min_spend", 25_000, "quota", 50, "start_at", h.at(-42, "00:00"),
		"expired_at", h.at(7, "23:59"), "all_outlets", false, "outlet_ids", []string{"o1"}))).
		status(http.StatusOK, "").decode(&got)
	if got.Status != StatusActive {
		t.Errorf("extended: status %s", got.Status)
	}
}

func TestVoucherOutletsAreNamed(t *testing.T) {
	h := newHarness(t)
	v := h.create(h.body("TIGA", set("all_outlets", false, "outlet_ids", []string{"o2", "o3", "o1"})))
	if got := v.Outlets; len(got) != 3 || got[0].Name != "aceh" || got[1].Name != "Bandung" || got[2].Name != "Jakarta" {
		t.Errorf("outlets %+v, want by name, in any case", got)
	}
	// A deleted outlet sells nothing; the voucher no longer lists it.
	h.exec(`UPDATE outlets SET deleted_at = now(), status = 'inactive' WHERE outlet_id = 'o2'`)
	got := h.get(v.VoucherID)
	if len(got.Outlets) != 2 || got.Outlets[0].OutletID != "o3" || got.Outlets[1].OutletID != "o1" {
		t.Errorf("outlets %+v, want o3 and o1", got.Outlets)
	}
	l := h.do(http.MethodGet, "/api/v1/vouchers", "owner", "", nil).status(http.StatusOK, "").list()
	if len(l.Data) != 1 || len(l.Data[0].Outlets) != 2 {
		t.Errorf("listed with %+v", l.Data)
	}
}

func TestSetVoucherStatus(t *testing.T) {
	h := newHarness(t)
	v := h.create(h.body("STATUS"))
	path := "/api/v1/vouchers/" + v.VoucherID + "/status"
	var got voucherResponse
	h.do(http.MethodPatch, path, "owner", "", map[string]any{"is_active": false}).status(http.StatusOK, "").decode(&got)
	if got.IsActive || got.Status != StatusInactive {
		t.Errorf("deactivated: %v %s", got.IsActive, got.Status)
	}
	h.do(http.MethodPatch, path, "owner", "", map[string]any{"is_active": false}).status(http.StatusOK, "")
	h.do(http.MethodPatch, path, "owner", "", map[string]any{"is_active": true}).status(http.StatusOK, "").decode(&got)
	if !got.IsActive || got.Status != StatusActive {
		t.Errorf("activated: %v %s", got.IsActive, got.Status)
	}
	log := h.log(v.VoucherID)
	if !slices.Equal(actions(log), []string{"voucher.created", "voucher.deactivated", "voucher.activated"}) {
		t.Errorf("log %v", actions(log))
	}
	for _, e := range log[1:] {
		if e.UserID == nil || *e.UserID != "owner" || e.OutletID != nil {
			t.Errorf("%s by %v in %v", e.Action, e.UserID, e.OutletID)
		}
	}
	h.do(http.MethodPatch, path, "owner", "", map[string]any{}).status(http.StatusUnprocessableEntity, "validation_failed")
	h.do(http.MethodPatch, "/api/v1/vouchers/v404/status", "owner", "", map[string]any{"is_active": true}).
		status(http.StatusNotFound, "voucher_not_found")
}

func TestDeleteVoucher(t *testing.T) {
	h := newHarness(t)
	v := h.create(h.body("HAPUS", set("all_outlets", false, "outlet_ids", []string{"o1"})))
	h.do(http.MethodDelete, "/api/v1/vouchers/"+v.VoucherID, "owner", "", nil).status(http.StatusNoContent, "")
	h.do(http.MethodGet, "/api/v1/vouchers/"+v.VoucherID, "owner", "", nil).status(http.StatusNotFound, "voucher_not_found")
	h.do(http.MethodDelete, "/api/v1/vouchers/"+v.VoucherID, "owner", "", nil).status(http.StatusNotFound, "voucher_not_found")
	h.do(http.MethodPut, "/api/v1/vouchers/"+v.VoucherID, "owner", "", h.body("HAPUS")).status(http.StatusNotFound, "voucher_not_found")
	if l := h.do(http.MethodGet, "/api/v1/vouchers", "owner", "", nil).list(); len(l.Data) != 0 {
		t.Errorf("listed %v", codes(l.Data))
	}
	if l := h.do(http.MethodGet, "/api/v1/outlets/o1/vouchers", "manager", "", nil).list(); len(l.Data) != 0 {
		t.Errorf("the Manager lists %v", codes(l.Data))
	}
	if _, err := h.redeem("o1", "HAPUS", 50_000); !errors.Is(err, ErrVoucherNotFound) {
		t.Errorf("redeem a deleted voucher: %v", err)
	}
	log := h.log(v.VoucherID)
	if !slices.Equal(actions(log), []string{"voucher.created", "voucher.deleted"}) || log[1].Before["code"] != "HAPUS" ||
		log[1].Before["is_active"] != true || fmt.Sprint(log[1].Before["outlet_ids"]) != "[o1]" {
		t.Errorf("log %+v", log)
	}

	// A voucher a sale used stays, with the orders that name it.
	used := h.create(h.body("DIPAKAI"))
	if _, err := h.redeem("o1", "DIPAKAI", 50_000); err != nil {
		t.Fatal(err)
	}
	h.release(used.VoucherID)
	h.do(http.MethodDelete, "/api/v1/vouchers/"+used.VoucherID, "owner", "", nil).status(http.StatusConflict, "voucher_used")
	if h.get(used.VoucherID).Code != "DIPAKAI" {
		t.Error("the used voucher is gone")
	}
	h.do(http.MethodDelete, "/api/v1/vouchers/v404", "owner", "", nil).status(http.StatusNotFound, "voucher_not_found")
}

func TestVoucherRedemptions(t *testing.T) {
	h := newHarness(t)
	v := h.create(h.body("RIWAYAT"))
	h.exec(`UPDATE outlets SET deleted_at = now(), status = 'inactive' WHERE outlet_id = 'o3'`)
	at := h.clock.Add(-time.Hour)
	h.orders[v.VoucherID] = []Use{
		{OrderID: "or3", OrderNo: "ACH01-K01-20261001-0001", Outlet: outlet.Ref{OutletID: "o3"}, At: at, Discount: 2000, Status: "paid"},
		{OrderID: "or2", OrderNo: "BDG01-K01-20261001-0002", Outlet: outlet.Ref{OutletID: "o1"}, At: at, Discount: 1500, Status: "void"},
		{OrderID: "or1", OrderNo: "BDG01-K01-20261001-0001", Outlet: outlet.Ref{OutletID: "o1"}, At: at, Discount: 1000, Status: "paid"},
	}
	var l struct {
		Data []redemptionResponse `json:"data"`
		Meta struct {
			Page, PageSize, Total int
		} `json:"meta"`
	}
	h.do(http.MethodGet, "/api/v1/vouchers/"+v.VoucherID+"/redemptions?page_size=2", "owner", "", nil).
		status(http.StatusOK, "").decode(&l)
	if len(l.Data) != 2 || l.Meta.Total != 3 {
		t.Fatalf("redemptions %+v", l)
	}
	// A deleted outlet keeps its name in the history.
	first := l.Data[0]
	if first.OrderID != "or3" || first.Outlet.Code != "ACH01" || first.Outlet.Name != "aceh" ||
		first.VoucherDiscount != 2000 || first.Status != "paid" || !first.ClientCreatedAt.Equal(at) ||
		first.ClientCreatedAt.Location() != time.UTC {
		t.Errorf("first %+v", first)
	}
	if l.Data[1].Outlet.Name != "Bandung" || l.Data[1].Status != "void" {
		t.Errorf("second %+v", l.Data[1])
	}
	h.do(http.MethodGet, "/api/v1/vouchers/"+v.VoucherID+"/redemptions?page=2&page_size=2", "owner", "", nil).
		status(http.StatusOK, "").decode(&l)
	if len(l.Data) != 1 || l.Data[0].OrderID != "or1" {
		t.Errorf("page 2 %+v", l.Data)
	}

	none := h.create(h.body("BARU"))
	h.do(http.MethodGet, "/api/v1/vouchers/"+none.VoucherID+"/redemptions", "owner", "", nil).
		status(http.StatusOK, "").decode(&l)
	if l.Data == nil || len(l.Data) != 0 || l.Meta.Total != 0 {
		t.Errorf("no redemptions: %+v, want an empty list", l)
	}
	h.do(http.MethodGet, "/api/v1/vouchers/v404/redemptions", "owner", "", nil).status(http.StatusNotFound, "voucher_not_found")
	h.do(http.MethodGet, "/api/v1/vouchers/"+v.VoucherID+"/redemptions?page_size=101", "owner", "", nil).
		status(http.StatusUnprocessableEntity, "validation_failed")
}
