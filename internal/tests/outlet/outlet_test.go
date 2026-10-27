package outlet

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

func bandung() map[string]any {
	return map[string]any{
		"code": "bdg01", "name": "Maki Bandung", "address": "Jl. Braga 1", "phone": "022 123",
		"open_time": "08:00", "close_time": "22:00", "tax_pct": 11, "service_pct": 5.5,
	}
}

func TestCreateOutlet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	o := h.create(bandung())
	if o.Code != "BDG01" || o.Name != "Maki Bandung" || *o.Address != "Jl. Braga 1" || *o.Phone != "022 123" {
		t.Errorf("outlet = %+v", o)
	}
	if o.Timezone != "Asia/Jakarta" || o.Locale != "id" || o.Status != "active" || len(o.Managers) != 0 {
		t.Errorf("defaults: %+v, want Asia/Jakarta, id, active, no managers", o)
	}
	if *o.OpenTime != "08:00" || *o.CloseTime != "22:00" || o.TaxPct != 11 || o.ServicePct != 5.5 {
		t.Errorf("hours and charges: %s–%s, %v%%, %v%%", *o.OpenTime, *o.CloseTime, o.TaxPct, o.ServicePct)
	}
	var bps int
	h.scalar(&bps, `SELECT service_bps FROM outlets WHERE outlet_id = $1`, o.OutletID)
	if bps != 550 {
		t.Errorf("service_bps = %d, want 550: stored as basis points", bps)
	}

	// Its Manager starts with a cashier role, named in the outlet's language.
	var role, perms string
	h.scalar(&role, `SELECT name FROM roles WHERE outlet_id = $1`, o.OutletID)
	h.scalar(&perms, `SELECT string_agg(permission_code, ',') FROM role_permissions JOIN roles USING (role_id) WHERE outlet_id = $1`, o.OutletID)
	if role != "Kasir" || perms != "checkout.create" {
		t.Errorf("template role = %q with %q, want Kasir with checkout.create", role, perms)
	}
	// And its cashier takes cash, the one method that works offline.
	var methods string
	h.scalar(&methods, `SELECT string_agg(method || ' ' || is_enabled, ', ' ORDER BY method) FROM outlet_payment_methods WHERE outlet_id = $1`, o.OutletID)
	if methods != "cash true, debit false, qris false" {
		t.Errorf("payment methods = %q, want cash on and the others off", methods)
	}
	en := h.create(map[string]any{"code": "SBY01", "name": "Maki Surabaya", "locale": "en", "timezone": "Asia/Jakarta"})
	h.scalar(&role, `SELECT name FROM roles WHERE outlet_id = $1`, en.OutletID)
	if role != "Cashier" {
		t.Errorf("template role of an en outlet = %q, want Cashier", role)
	}

	if got := h.actions(o.OutletID); !slices.Equal(got, []string{"outlet.created"}) {
		t.Errorf("Log Action = %v, want outlet.created", got)
	}
	var after string
	h.scalar(&after, `SELECT after_json->>'code' FROM action_logs WHERE outlet_id = $1`, o.OutletID)
	if after != "BDG01" {
		t.Errorf("outlet.created after.code = %q", after)
	}

	// A code is taken whatever its case.
	body := bandung()
	body["code"] = "BDG01"
	h.do(http.MethodPost, "/api/v1/outlets", "owner", body).status(http.StatusConflict, "outlet_code_taken")
}

func TestCreateOutletChecksItsFields(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	with := func(changes map[string]any) map[string]any {
		b := bandung()
		for k, v := range changes {
			if v == nil {
				delete(b, k)
			} else {
				b[k] = v
			}
		}
		return b
	}

	tests := []struct {
		name        string
		body        map[string]any
		field, rule string
	}{
		{"code with a dash", with(map[string]any{"code": "BDG-01"}), "code", "format"},
		{"one-letter code", with(map[string]any{"code": "B"}), "code", "format"},
		{"no name", with(map[string]any{"name": nil}), "name", "required"},
		{"blank name", with(map[string]any{"name": "   "}), "name", "required"},
		{"long address", with(map[string]any{"address": strings.Repeat("a", 501)}), "address", "max"},
		{"unknown zone", with(map[string]any{"timezone": "Mars/Olympus"}), "timezone", "timezone"},
		{"the server's zone", with(map[string]any{"timezone": "Local"}), "timezone", "timezone"},
		{"unknown language", with(map[string]any{"locale": "fr"}), "locale", "oneof"},
		{"opening only", with(map[string]any{"close_time": nil}), "close_time", "required"},
		{"closing only", with(map[string]any{"open_time": nil}), "open_time", "required"},
		{"same hours", with(map[string]any{"close_time": "08:00"}), "close_time", "different"},
		{"no such hour", with(map[string]any{"open_time": "24:00"}), "open_time", "format"},
		{"seconds", with(map[string]any{"open_time": "08:00:00"}), "open_time", "format"},
		{"negative tax", with(map[string]any{"tax_pct": -1}), "tax_pct", "range"},
		{"tax over 100%", with(map[string]any{"tax_pct": 100.5}), "tax_pct", "range"},
		{"three decimals", with(map[string]any{"service_pct": 5.555}), "service_pct", "decimals"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := h.do(http.MethodPost, "/api/v1/outlets", "owner", tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
			if got := r.fields()[tt.field]; got != tt.rule {
				t.Errorf("fields = %v, want %s: %s", r.fields(), tt.field, tt.rule)
			}
		})
	}

	// Hours past midnight are fine, and so are no hours at all.
	h.create(with(map[string]any{"code": "NIGHT1", "open_time": "18:00", "close_time": "02:00"}))
	o := h.create(with(map[string]any{"code": "ALLDAY", "open_time": nil, "close_time": nil, "phone": "  "}))
	if o.OpenTime != nil || o.CloseTime != nil || o.Phone != nil {
		t.Errorf("no hours and a blank phone: %+v, want nil", o)
	}
}

func TestListOutlets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	bdg := h.create(map[string]any{"code": "BDG01", "name": "Bandung"})
	jkt := h.create(map[string]any{"code": "JKT01", "name": "jakarta Selatan"})
	sby := h.create(map[string]any{"code": "SBY01", "name": "Surabaya"})
	gone := h.create(map[string]any{"code": "OLD01", "name": "Lama"})
	h.do(http.MethodPatch, "/api/v1/outlets/"+sby.OutletID+"/status", "owner", map[string]any{"status": "inactive"}).status(http.StatusOK, "")
	h.do(http.MethodPatch, "/api/v1/outlets/"+gone.OutletID+"/status", "owner", map[string]any{"status": "inactive"}).status(http.StatusOK, "")
	h.do(http.MethodDelete, "/api/v1/outlets/"+gone.OutletID, "owner", map[string]any{"confirm_name": "Lama"}).status(http.StatusNoContent, "")
	h.exec(`INSERT INTO users (user_id, outlet_id, name, username, password_hash, is_manager)
		VALUES ('m1', $1, 'Rina', 'rina', 'x', true)`, bdg.OutletID)

	names := func(l listResponse) []string {
		var out []string
		for _, o := range l.Data {
			out = append(out, o.Name)
		}
		return out
	}
	all := h.do(http.MethodGet, "/api/v1/outlets", "owner", nil).status(http.StatusOK, "").list()
	if got := names(all); !slices.Equal(got, []string{"Bandung", "jakarta Selatan", "Surabaya"}) || all.Meta.Total != 3 {
		t.Errorf("list = %v (total %d), want the three left, by name whatever its case", got, all.Meta.Total)
	}
	if m := all.Data[0].Managers; len(m) != 1 || m[0].Name != "Rina" {
		t.Errorf("managers of Bandung = %+v, want Rina", m)
	}

	for query, want := range map[string][]string{
		"?status=inactive": {"Surabaya"},
		"?status=active":   {"Bandung", "jakarta Selatan"},
		"?q=JAKARTA":       {"jakarta Selatan"},
		"?q=sby":           {"Surabaya"},
		"?q=%25":           nil,
		"?q=a_":            nil,
	} {
		l := h.do(http.MethodGet, "/api/v1/outlets"+query, "owner", nil).status(http.StatusOK, "").list()
		if got := names(l); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", query, got, want)
		}
	}

	page := h.do(http.MethodGet, "/api/v1/outlets?page=2&page_size=2", "owner", nil).status(http.StatusOK, "").list()
	if got := names(page); !slices.Equal(got, []string{"Surabaya"}) || page.Meta.Total != 3 || page.Meta.Page != 2 || page.Meta.PageSize != 2 {
		t.Errorf("page 2 of 2 = %v, meta %+v", got, page.Meta)
	}
	h.do(http.MethodGet, "/api/v1/outlets?status=closed", "owner", nil).status(http.StatusUnprocessableEntity, "validation_failed")
	_ = jkt
}

func TestUpdateOutlet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o := h.create(bandung())
	h.create(map[string]any{"code": "JKT01", "name": "Jakarta"})

	body := bandung()
	body["name"], body["tax_pct"] = "Maki Braga", 10
	u := h.do(http.MethodPut, "/api/v1/outlets/"+o.OutletID, "owner", body).status(http.StatusOK, "").outlet()
	if u.Name != "Maki Braga" || u.TaxPct != 10 || u.Code != "BDG01" {
		t.Errorf("updated = %+v", u)
	}
	var before, after string
	h.scalar(&before, `SELECT before_json::text FROM action_logs WHERE action = 'outlet.updated'`)
	h.scalar(&after, `SELECT after_json::text FROM action_logs WHERE action = 'outlet.updated'`)
	if before != `{"name": "Maki Bandung", "tax_bps": 1100}` || after != `{"name": "Maki Braga", "tax_bps": 1000}` {
		t.Errorf("outlet.updated before %s after %s, want only what changed", before, after)
	}

	// Saving it unchanged records nothing.
	h.do(http.MethodPut, "/api/v1/outlets/"+o.OutletID, "owner", body).status(http.StatusOK, "")
	if got := h.actions(o.OutletID); !slices.Equal(got, []string{"outlet.created", "outlet.updated"}) {
		t.Errorf("Log Action = %v", got)
	}

	body["code"] = "jkt01"
	h.do(http.MethodPut, "/api/v1/outlets/"+o.OutletID, "owner", body).status(http.StatusConflict, "outlet_code_taken")
	h.do(http.MethodPut, "/api/v1/outlets/nowhere", "owner", bandung()).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, "/api/v1/outlets/nowhere", "owner", nil).status(http.StatusNotFound, "outlet_not_found")
}

func TestDeactivateAndDeleteOutlet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o := h.create(bandung())
	path := "/api/v1/outlets/" + o.OutletID
	setStatus := func(status string) reply {
		return h.do(http.MethodPatch, path+"/status", "owner", map[string]any{"status": status})
	}

	h.do(http.MethodDelete, path, "owner", map[string]any{"confirm_name": "Maki Bandung"}).status(http.StatusConflict, "outlet_active")
	if got := setStatus("inactive").status(http.StatusOK, "").outlet().Status; got != "inactive" {
		t.Errorf("status = %q", got)
	}
	setStatus("inactive").status(http.StatusOK, "") // again: nothing to record
	h.do(http.MethodDelete, path, "owner", map[string]any{"confirm_name": "maki bandung"}).status(http.StatusUnprocessableEntity, "outlet_name_mismatch")
	h.do(http.MethodDelete, path, "owner", map[string]any{"confirm_name": " Maki Bandung "}).status(http.StatusNoContent, "")

	h.do(http.MethodGet, path, "owner", nil).status(http.StatusNotFound, "outlet_not_found")
	setStatus("active").status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodPost, "/api/v1/outlets", "owner", bandung()).status(http.StatusConflict, "outlet_code_taken")
	if got := h.actions(o.OutletID); !slices.Equal(got, []string{"outlet.created", "outlet.deactivated", "outlet.deleted"}) {
		t.Errorf("Log Action = %v", got)
	}
}

func TestOutletsAreTheOwnersAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	o := h.create(bandung())
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/outlets"},
		{http.MethodPost, "/api/v1/outlets"},
		{http.MethodGet, "/api/v1/outlets/" + o.OutletID},
		{http.MethodPut, "/api/v1/outlets/" + o.OutletID},
		{http.MethodPatch, "/api/v1/outlets/" + o.OutletID + "/status"},
		{http.MethodDelete, "/api/v1/outlets/" + o.OutletID},
	} {
		h.do(route.method, route.path, "manager", bandung()).status(http.StatusForbidden, "forbidden")
		h.do(route.method, route.path, "", bandung()).status(http.StatusUnauthorized, "unauthorized")
	}
}
