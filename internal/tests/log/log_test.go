package log

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/response"
)

// seed writes the entries, oldest first: a00 at 01:00 UTC on 1 October and so
// on by the hour.
func (h *harness) seed() {
	h.t.Helper()
	h.exec(`INSERT INTO roles (role_id, outlet_id, name) VALUES ('r1', 'o1', 'Kasir')`)
	h.exec(`INSERT INTO user_roles (user_id, role_id, outlet_id) VALUES ('k1', 'r1', 'o1')`)
	h.exec(`INSERT INTO devices (device_id, outlet_id, code, name, platform, token_hash, activated_by) VALUES
		('d1', 'o1', 'K01', 'Kasir Depan', 'web', repeat('a', 64), 'm1')`)
	rows := []struct{ id, outlet, user, device, action, before, after, ip string }{
		{"a00", "", "owner", "", "outlet.created", "", `{"name":"Bandung"}`, "203.0.113.9"},
		{"a01", "o1", "m1", "", "menu.created", "", `{"name":"Kopi"}`, ""},
		{"a02", "o1", "m1", "", "menu.updated", `{"price":10}`, `{"price":12}`, ""},
		{"a03", "o1", "k1", "d1", "order.created", "", `{"order_no":"BDG01-K01-20261001-0001"}`, ""},
		{"a04", "o2", "m2", "", "menu.created", "", `{"name":"Bolu"}`, ""},
		{"a05", "", "", "", "auth.login_failed", "", "", "203.0.113.10"},
	}
	for i, r := range rows {
		h.exec(`INSERT INTO action_logs (action_log_id, outlet_id, user_id, device_id, action, target_type, target_id,
			before_json, after_json, ip, occurred_at)
			VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), $5, 'thing', 't1',
				NULLIF($6, '')::jsonb, NULLIF($7, '')::jsonb, NULLIF($8, '')::inet,
				'2026-10-01T01:00:00Z'::timestamptz + make_interval(hours => $9))`,
			r.id, r.outlet, r.user, r.device, r.action, r.before, r.after, r.ip, i)
	}
}

func (h *harness) ids(path, who string) []string {
	h.t.Helper()
	var l response.List[models.ActionLogResponse]
	h.do(http.MethodGet, path, who, nil).status(http.StatusOK, "").decode(&l)
	if l.Meta.Total != len(l.Data) && !strings.Contains(path, "page_size") {
		h.t.Errorf("%s: total %d, %d on the page", path, l.Meta.Total, len(l.Data))
	}
	out := make([]string, len(l.Data))
	for i, e := range l.Data {
		out[i] = e.ActionLogID
	}
	return out
}

func same(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestOwnerSeesEverything(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed()
	const p = "/api/v1/owner/logs"
	same(t, "all, newest first", h.ids(p, "owner"), "a05", "a04", "a03", "a02", "a01", "a00")
	same(t, "self", h.ids(p+"?self=true", "owner"), "a00")
	same(t, "outlets", h.ids(p+"?outlet_ids=o1,o2", "owner"), "a04", "a03", "a02", "a01")
	same(t, "one outlet", h.ids(p+"?outlet_ids=o2", "owner"), "a04")
	same(t, "actions", h.ids(p+"?action=menu.created,auth.login_failed", "owner"), "a05", "a04", "a01")
	from, to := url.QueryEscape("2026-10-01T02:00:00Z"), url.QueryEscape("2026-10-01T04:00:00+00:00")
	same(t, "period, to left out", h.ids(p+"?from="+from+"&to="+to, "owner"), "a02", "a01")

	var page response.List[models.ActionLogResponse]
	h.do(http.MethodGet, p+"?page=2&page_size=4", "owner", nil).status(http.StatusOK, "").decode(&page)
	if page.Meta.Total != 6 || len(page.Data) != 2 || page.Data[0].ActionLogID != "a01" {
		t.Errorf("page 2 = %+v", page)
	}
}

func TestOutletSeesItsOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed()
	const p = "/api/v1/outlets/o1/logs"
	same(t, "the Manager", h.ids(p, "manager"), "a03", "a02", "a01")
	same(t, "the Owner, in the outlet", h.ids(p, "owner"), "a03", "a02", "a01")
	same(t, "by user", h.ids(p+"?user_id=k1", "manager"), "a03")
	same(t, "by action", h.ids(p+"?action=menu.updated", "manager"), "a02")
	// A user of another outlet, asked for by name, finds nothing here.
	same(t, "another outlet's user", h.ids(p+"?user_id=m2", "manager"))
}

func TestEntryNamesWhoAndWhere(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed()
	var l response.List[models.ActionLogResponse]
	h.do(http.MethodGet, "/api/v1/owner/logs", "owner", nil).status(http.StatusOK, "").decode(&l)
	by := map[string]models.ActionLogResponse{}
	for _, e := range l.Data {
		by[e.ActionLogID] = e
	}
	sale := by["a03"]
	if sale.User == nil || sale.User.Name != "Budi" || sale.User.RoleName == nil || *sale.User.RoleName != "Kasir" {
		t.Errorf("cashier = %+v", sale.User)
	}
	if sale.Outlet == nil || sale.Outlet.Code != "BDG01" || sale.Device == nil || sale.Device.Code != "K01" {
		t.Errorf("sale = %+v %+v", sale.Outlet, sale.Device)
	}
	if m := by["a01"]; m.User == nil || !m.User.IsManager {
		t.Errorf("manager = %+v", m.User)
	}
	own := by["a00"]
	if own.User == nil || !own.User.IsOwner || own.Outlet != nil || own.IP == nil || *own.IP != "203.0.113.9" {
		t.Errorf("owner's = %+v", own)
	}
	if unknown := by["a05"]; unknown.User != nil || unknown.Outlet != nil || len(unknown.After) != 0 && string(unknown.After) != "null" {
		t.Errorf("unknown = %+v", unknown)
	}
	var before, after map[string]int
	upd := by["a02"]
	if err := json.Unmarshal(upd.Before, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(upd.After, &after); err != nil {
		t.Fatal(err)
	}
	if before["price"] != 10 || after["price"] != 12 {
		t.Errorf("before %v, after %v", before, after)
	}
}

func TestAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.seed()
	h.do(http.MethodGet, "/api/v1/owner/logs", "manager", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o1/logs", "cashier", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o1/logs", "manager2", nil).status(http.StatusNotFound, "outlet_not_found")
}

func TestBadFilters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, q := range []string{"from=kemarin", "to=2026-10-01", "from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", "self=mungkin", "page=0"} {
		h.do(http.MethodGet, "/api/v1/owner/logs?"+q, "owner", nil).status(http.StatusUnprocessableEntity, "validation_failed")
	}
}
