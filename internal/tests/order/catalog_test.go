package order

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/cookie"
)

const (
	catalogPath = "/api/v1/outlets/o1/pos/catalog"
	devicePath  = "/api/v1/outlets/o1/pos/device"
	ordersPath  = "/api/v1/outlets/o1/orders"
)

func (h *harness) catalog() models.CatalogResponse {
	h.t.Helper()
	var c models.CatalogResponse
	h.do(http.MethodGet, catalogPath, "cashier", "", nil).status(http.StatusOK, "").decode(&c)
	return c
}

func TestCatalog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	food := h.category("o1", "Makanan")
	empty := h.category("o1", "Kosong")
	hidden := h.category("o1", "Musiman")
	// Food comes before drinks at the cashier.
	if err := h.categories.ReorderCategories(t.Context(), principals["manager"], "", "o1",
		[]string{food, h.drinks, empty, hidden}); err != nil {
		t.Fatal(err)
	}
	n := func(v int) *int { return &v }
	jeruk := h.menu("o1", h.drinks, "Es jeruk", 8000, nil)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, n(20))
	soda := h.menu("o1", h.drinks, "Soda", 12000, n(0))
	nasi := h.menu("o1", food, "Nasi goreng", 25000, n(2))
	roti := h.menu("o1", food, "Roti", 15000, n(0))
	h.exec(`UPDATE menus SET stock_alert = 5 WHERE menu_id = $1`, nasi.MenuID)
	off := h.menu("o1", h.drinks, "Jus", 15000, nil)
	if _, err := h.menus.SetMenuActive(t.Context(), principals["manager"], "", "o1", off.MenuID, false); err != nil {
		t.Fatal(err)
	}
	gone := h.menu("o1", food, "Bakso", 20000, nil)
	if err := h.menus.DeleteMenu(t.Context(), principals["manager"], "", "o1", gone.MenuID); err != nil {
		t.Fatal(err)
	}
	h.menu("o1", hidden, "Es campur", 20000, nil)
	if _, err := h.categories.SetCategoryActive(t.Context(), principals["manager"], "", "o1", hidden, false); err != nil {
		t.Fatal(err)
	}
	h.menu("o2", h.category("o2", "Drinks"), "Coffee", 30000, nil)

	c := h.catalog()
	var names []string
	for _, m := range c.Menus {
		names = append(names, m.Name)
	}
	// What can be sold first, by category, then name; out of stock last, the
	// same way. Inactive and deleted menus, and an inactive category's, not
	// at all.
	if want := []string{"Nasi goreng", "Es jeruk", "Kopi", "Roti", "Soda"}; !slices.Equal(names, want) {
		t.Errorf("menus = %v, want %v", names, want)
	}
	var cats []string
	for _, cat := range c.Categories {
		cats = append(cats, fmt.Sprintf("%s:%d", cat.Name, cat.ItemCount))
	}
	// Only the categories holding one of those menus.
	if want := []string{"Makanan:2", "Minuman:3"}; !slices.Equal(cats, want) {
		t.Errorf("categories = %v, want %v", cats, want)
	}
	byName := map[string]models.CatalogMenuResponse{}
	for _, m := range c.Menus {
		byName[m.Name] = m
	}
	if m := byName["Es jeruk"]; m.StockQty != nil || m.TrackStock || m.StockStatus != "untracked" || m.MenuID != jeruk.MenuID {
		t.Errorf("an untracked menu: %+v", m)
	}
	if m := byName["Kopi"]; m.StockQty == nil || *m.StockQty != 20 || m.StockStatus != "ok" ||
		m.CategoryID != h.drinks || m.Price != 18000 || m.MenuID != kopi.MenuID {
		t.Errorf("a tracked menu: %+v", m)
	}
	if m := byName["Nasi goreng"]; m.StockStatus != "low" || m.StockAlert == nil || *m.StockAlert != 5 || m.MenuID != nasi.MenuID {
		t.Errorf("a menu running low: %+v", m)
	}
	if m := byName["Soda"]; m.StockStatus != "out" || m.MenuID != soda.MenuID || byName["Roti"].MenuID != roti.MenuID {
		t.Errorf("a menu out of stock: %+v", m)
	}

	o := c.Outlet
	if o.OutletID != "o1" || o.Code != "BDG01" || o.Name != "Bandung" || o.Address == nil || *o.Address != "Jl. Braga 1" ||
		o.Phone == nil || o.Timezone != "Asia/Jakarta" || o.Locale != "id" || o.TaxBPS != o1Tax || o.ServiceBPS != o1Service {
		t.Errorf("outlet = %+v", o)
	}
	if !slices.Equal(c.PaymentMethods, []string{"cash"}) {
		t.Errorf("payment methods = %v, want [cash]", c.PaymentMethods)
	}
	// Another outlet's catalog is its own.
	var other models.CatalogResponse
	h.do(http.MethodGet, "/api/v1/outlets/o2/pos/catalog", "other", "", nil).status(http.StatusOK, "").decode(&other)
	if len(other.Menus) != 1 || other.Menus[0].Name != "Coffee" || other.Outlet.Code != "JKT01" {
		t.Errorf("o2's catalog = %+v", other)
	}
}

func TestEmptyCatalog(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	r := h.do(http.MethodGet, catalogPath, "cashier", "", nil).status(http.StatusOK, "")
	// Empty lists, never null.
	if body := r.Body.String(); !strings.Contains(body, `"categories":[]`) || !strings.Contains(body, `"menus":[]`) {
		t.Errorf("empty catalog = %s", body)
	}
}

func TestPosDevice(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	front := h.till("o1", "Kasir depan")
	back := h.till("o1", "Kasir belakang")

	// Activating counts as being seen; a day ago, then.
	h.exec(`UPDATE devices SET last_seen_at = now() - interval '1 day'`)
	var d models.PosDeviceResponse
	r := h.do(http.MethodGet, devicePath, "cashier", front.token, nil).status(http.StatusOK, "")
	r.decode(&d)
	if d.Device.DeviceID != front.DeviceID || d.Device.Code != "K01" || d.Device.Name != "Kasir depan" ||
		d.Device.Platform != models.PlatformWeb || d.OrderNoPrefix != "BDG01-K01" || d.Day != "2026-10-01" ||
		d.NextNumber != 1 {
		t.Errorf("device = %+v", d)
	}
	// The browser's cookie starts its 400 days again.
	var renewed *http.Cookie
	for _, c := range r.Result().Cookies() {
		if c.Name == cookie.DeviceCookie {
			renewed = c
		}
	}
	if renewed == nil || renewed.Value != front.token || !renewed.HttpOnly || renewed.MaxAge != cookie.DeviceCookieMaxAge {
		t.Errorf("cookie = %+v", renewed)
	}
	var seen bool
	h.scalar(&seen, `SELECT last_seen_at > now() - interval '1 minute' FROM devices WHERE device_id = $1`, front.DeviceID)
	if !seen {
		t.Error("the device wasn't marked seen")
	}

	// The count is the device's, of the day: yesterday's numbers and another
	// device's don't count, and it goes on from the highest.
	today := h.clock
	h.clock = today.AddDate(0, 0, -1)
	h.sell(front, "cashier", h.order(front, item(kopi, 1)))
	h.clock = today
	h.sell(back, "cashier", h.order(back, item(kopi, 1)))
	front.next = 7
	h.sell(front, "cashier", h.order(front, item(kopi, 1)))
	h.do(http.MethodGet, devicePath, "cashier", front.token, nil).status(http.StatusOK, "").decode(&d)
	if d.NextNumber != 8 || d.Day != "2026-10-01" {
		t.Errorf("after 0007: next %d on %s, want 8", d.NextNumber, d.Day)
	}
	h.do(http.MethodGet, devicePath, "cashier", back.token, nil).status(http.StatusOK, "").decode(&d)
	if d.NextNumber != 2 || d.OrderNoPrefix != "BDG01-K02" {
		t.Errorf("the other device: %+v", d)
	}
	// The day is the outlet's: 23:30 UTC is the next morning in Bandung.
	h.clock = time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC)
	h.do(http.MethodGet, devicePath, "cashier", front.token, nil).status(http.StatusOK, "").decode(&d)
	if d.Day != "2026-10-02" || d.NextNumber != 1 {
		t.Errorf("the next day in Bandung: %+v", d)
	}
}

func TestPosDeviceOfTheApp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	d, token, err := h.devices.Activate(t.Context(), principals["manager"], "", "o1", "", "Tablet", models.PlatformAndroid)
	if err != nil {
		t.Fatal(err)
	}
	r := h.request(http.MethodGet, devicePath, "cashier", nil, func(req *http.Request) {
		req.Header.Set(cookie.DeviceHeader, token)
	}).status(http.StatusOK, "")
	var got models.PosDeviceResponse
	r.decode(&got)
	if got.Device.DeviceID != d.DeviceID || got.Device.Platform != models.PlatformAndroid {
		t.Errorf("device = %+v", got)
	}
	// The app keeps its token itself; it gets no cookie.
	if cookies := r.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("cookies = %v", cookies)
	}
}

func TestDevicesThatCantSell(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	kopi := h.menu("o1", h.drinks, "Kopi", 18000, nil)
	tl := h.till("o1", "Kasir")
	elsewhere := h.till("o2", "Kasir")

	h.do(http.MethodGet, devicePath, "cashier", "", nil).status(http.StatusForbidden, "device_not_registered")
	h.do(http.MethodGet, devicePath, "cashier", "not-a-token", nil).status(http.StatusForbidden, "device_not_registered")
	h.do(http.MethodGet, devicePath, "cashier", elsewhere.token, nil).status(http.StatusForbidden, "device_not_registered")
	h.do(http.MethodPost, ordersPath, "cashier", "", h.order(tl, item(kopi, 1))).
		status(http.StatusForbidden, "device_not_registered")
	h.do(http.MethodPost, ordersPath, "cashier", elsewhere.token, h.order(tl, item(kopi, 1))).
		status(http.StatusForbidden, "device_not_registered")

	if err := h.devices.Revoke(t.Context(), principals["manager"], "", "o1", tl.DeviceID); err != nil {
		t.Fatal(err)
	}
	h.do(http.MethodGet, devicePath, "cashier", tl.token, nil).status(http.StatusForbidden, "device_revoked")
	h.do(http.MethodPost, ordersPath, "cashier", tl.token, h.order(tl, item(kopi, 1))).
		status(http.StatusForbidden, "device_revoked")
	if n := h.count(`SELECT count(*) FROM orders`); n != 0 {
		t.Errorf("%d orders from devices that can't sell", n)
	}
}

func TestCashierAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	tl := h.till("o1", "Kasir")
	// The Owner doesn't run a till; whoever only voids, or only manages the
	// menu, doesn't sell.
	for _, who := range []string{"owner", "voider", "staff"} {
		h.do(http.MethodGet, catalogPath, who, "", nil).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodGet, devicePath, who, tl.token, nil).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPost, ordersPath, who, tl.token, map[string]any{}).status(http.StatusForbidden, "forbidden")
	}
	h.do(http.MethodGet, catalogPath, "manager", "", nil).status(http.StatusOK, "")
	// Another outlet's cashier can't reach it, and nobody signed in is turned
	// away.
	h.do(http.MethodGet, catalogPath, "other", "", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodPost, ordersPath, "other", tl.token, map[string]any{}).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, catalogPath, "", "", nil).status(http.StatusUnauthorized, "unauthorized")
}
