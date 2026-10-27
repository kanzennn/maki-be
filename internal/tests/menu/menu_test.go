package menu

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/response"
)

const menus = "/api/v1/outlets/o1/menus"

// createMenu makes a menu as the outlet's Manager and returns it.
func (h *harness) createMenu(body map[string]any) models.MenuResponse {
	h.t.Helper()
	var m models.MenuResponse
	h.do(http.MethodPost, menus, "manager", body).status(http.StatusCreated, "").decode(&m)
	return m
}

func (h *harness) listMenus(query string) response.List[models.MenuResponse] {
	h.t.Helper()
	var list response.List[models.MenuResponse]
	h.do(http.MethodGet, menus+query, "manager", nil).status(http.StatusOK, "").decode(&list)
	return list
}

func menuNames(ms []models.MenuResponse) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}

// movements returns the menu's stock movements, oldest first, as
// "type reason change→after by user".
func (h *harness) movements(menuID string) []string {
	h.t.Helper()
	rows, err := h.pool.Query(h.t.Context(), `
		SELECT type || ' ' || COALESCE(reason, '-') || ' ' || qty_change || '→' || stock_after || ' by ' || user_id
		FROM stock_movements WHERE menu_id = $1 ORDER BY created_at, stock_movement_id`, menuID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestCreateMenu(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "🧋")

	coffee := h.createMenu(map[string]any{
		"name": " Es Kopi Susu ", "category_id": drinks.CategoryID, "price": 18000, "sku": "KOPI-01",
		"description": "Kopi susu gula aren", "stock_alert": 5,
	})
	if coffee.Name != "Es Kopi Susu" || coffee.Price != 18000 || *coffee.SKU != "KOPI-01" || !coffee.IsActive ||
		coffee.Category.Name != "Minuman" || *coffee.Category.Icon != "🧋" || coffee.ImageURL != nil {
		t.Errorf("menu = %+v", coffee)
	}
	if coffee.TrackStock || coffee.StockStatus != models.StockUntracked || coffee.StockAlert != nil || coffee.StockQty != 0 {
		t.Errorf("untracked menu = %+v, want no stock and no alert", coffee)
	}
	if got := h.movements(coffee.MenuID); len(got) != 0 {
		t.Errorf("movements = %v, want none for an untracked menu", got)
	}

	cake := h.createMenu(map[string]any{
		"name": "Kue", "category_id": drinks.CategoryID, "price": 12000, "track_stock": true, "initial_stock": 10, "stock_alert": 3,
	})
	if cake.StockQty != 10 || *cake.StockAlert != 3 || cake.StockStatus != models.StockOK {
		t.Errorf("tracked menu = %+v", cake)
	}
	if got := h.movements(cake.MenuID); !slices.Equal(got, []string{"initial initial 10→10 by m1"}) {
		t.Errorf("movements = %v, want the starting count", got)
	}
	action, _, after := h.lastLog(cake.MenuID)
	if action != "menu.created" || after["price"] != float64(12000) || after["initial_stock"] != float64(10) {
		t.Errorf("Log Action = %s %v", action, after)
	}

	for _, tt := range []struct {
		stock  int
		status string
	}{{3, models.StockLow}, {1, models.StockLow}, {0, models.StockOut}} {
		m := h.createMenu(map[string]any{
			"name": "Roti", "category_id": drinks.CategoryID, "price": 8000, "track_stock": true, "initial_stock": tt.stock, "stock_alert": 3,
		})
		if m.StockStatus != tt.status {
			t.Errorf("stock %d with an alert at 3 is %s, want %s", tt.stock, m.StockStatus, tt.status)
		}
	}
	if m := h.createMenu(map[string]any{
		"name": "Air", "category_id": drinks.CategoryID, "price": 0, "track_stock": true, "initial_stock": 1, "is_active": false,
	}); m.StockStatus != models.StockOK || m.IsActive || m.Price != 0 {
		t.Errorf("a free, inactive menu without an alert = %+v, want its stock ok", m)
	}

	h.do(http.MethodPost, menus, "manager", map[string]any{
		"name": "X", "category_id": drinks.CategoryID, "price": 1, "sku": "kopi-01",
	}).status(http.StatusConflict, "menu_sku_taken")
	other := h.otherOutletCategory()
	h.do(http.MethodPost, "/api/v1/outlets/o2/menus", "manager2", map[string]any{
		"name": "X", "category_id": other, "price": 1, "sku": "KOPI-01",
	}).status(http.StatusCreated, "")

	for name, tt := range map[string]struct {
		body        map[string]any
		field, rule string
	}{
		"no name":            {map[string]any{"category_id": drinks.CategoryID, "price": 1}, "name", "required"},
		"long name":          {map[string]any{"name": strings.Repeat("n", 101), "category_id": drinks.CategoryID, "price": 1}, "name", "max"},
		"no category":        {map[string]any{"name": "X", "price": 1}, "category_id", "required"},
		"unknown category":   {map[string]any{"name": "X", "category_id": "nope", "price": 1}, "category_id", "exists"},
		"another's category": {map[string]any{"name": "X", "category_id": other, "price": 1}, "category_id", "exists"},
		"no price":           {map[string]any{"name": "X", "category_id": drinks.CategoryID}, "price", "required"},
		"negative price":     {map[string]any{"name": "X", "category_id": drinks.CategoryID, "price": -1}, "price", "range"},
		"price too high":     {map[string]any{"name": "X", "category_id": drinks.CategoryID, "price": models.MaxPrice + 1}, "price", "range"},
		"SKU with a space":   {map[string]any{"name": "X", "category_id": drinks.CategoryID, "price": 1, "sku": "KOPI 01"}, "sku", "format"},
		"long description":   {map[string]any{"name": "X", "category_id": drinks.CategoryID, "price": 1, "description": strings.Repeat("d", 501)}, "description", "max"},
		"tracked, no start":  {map[string]any{"name": "X", "category_id": drinks.CategoryID, "price": 1, "track_stock": true}, "initial_stock", "required"},
		"negative start":     {map[string]any{"name": "X", "category_id": drinks.CategoryID, "price": 1, "track_stock": true, "initial_stock": -1}, "initial_stock", "range"},
		"alert too high":     {map[string]any{"name": "X", "category_id": drinks.CategoryID, "price": 1, "track_stock": true, "initial_stock": 1, "stock_alert": models.MaxStock + 1}, "stock_alert", "range"},
	} {
		t.Run(name, func(t *testing.T) {
			r := h.do(http.MethodPost, menus, "manager", tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
			if r.fields()[tt.field] != tt.rule {
				t.Errorf("fields = %v, want %s: %s", r.fields(), tt.field, tt.rule)
			}
		})
	}
}

func TestListMenus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "")
	food := h.createCategory("Makanan", "")
	h.do(http.MethodPut, categories+"/order", "manager", map[string]any{
		"category_ids": []string{food.CategoryID, drinks.CategoryID},
	}).status(http.StatusNoContent, "")

	for _, m := range []map[string]any{
		{"name": "es kopi", "category_id": drinks.CategoryID, "price": 18000, "sku": "KP_1"},
		{"name": "Air mineral", "category_id": drinks.CategoryID, "price": 5000, "track_stock": true, "initial_stock": 0},
		{"name": "Nasi goreng", "category_id": food.CategoryID, "price": 25000, "track_stock": true, "initial_stock": 2, "stock_alert": 5},
		{"name": "Mie goreng", "category_id": food.CategoryID, "price": 22000, "track_stock": true, "initial_stock": 20, "stock_alert": 5, "is_active": false},
	} {
		h.createMenu(m)
	}
	gone := h.createMenu(map[string]any{"name": "Lama", "category_id": food.CategoryID, "price": 1})
	h.do(http.MethodDelete, menus+"/"+gone.MenuID, "manager", nil).status(http.StatusNoContent, "")

	all := h.listMenus("")
	if !slices.Equal(menuNames(all.Data), []string{"Mie goreng", "Nasi goreng", "Air mineral", "es kopi"}) || all.Meta.Total != 4 {
		t.Errorf("menus = %v (%d), want by the categories' order, then name; deleted ones left out", menuNames(all.Data), all.Meta.Total)
	}

	for query, want := range map[string][]string{
		"?q=GORENG":                         {"Mie goreng", "Nasi goreng"},
		"?q=kp_":                            {"es kopi"},
		"?q=%25":                            {},
		"?category_id=" + drinks.CategoryID: {"Air mineral", "es kopi"},
		"?status=inactive":                  {"Mie goreng"},
		"?status=active&stock=low":          {"Nasi goreng"},
		"?stock=out":                        {"Air mineral"},
		"?stock=ok":                         {"Mie goreng"},
		"?stock=untracked":                  {"es kopi"},
	} {
		if got := menuNames(h.listMenus(query).Data); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", query, got, want)
		}
	}

	page := h.listMenus("?page=2&page_size=3")
	if page.Meta.Total != 4 || !slices.Equal(menuNames(page.Data), []string{"es kopi"}) {
		t.Errorf("page 2 = %v %+v", menuNames(page.Data), page.Meta)
	}
	r := h.do(http.MethodGet, menus+"?stock=plenty&status=gone", "manager", nil).status(http.StatusUnprocessableEntity, "validation_failed")
	if r.fields()["stock"] != "oneof" || r.fields()["status"] != "oneof" {
		t.Errorf("fields = %v", r.fields())
	}
}

func TestUpdateMenu(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "")
	food := h.createCategory("Makanan", "")
	m := h.createMenu(map[string]any{"name": "Es Jeruk", "category_id": drinks.CategoryID, "price": 5000, "sku": "T1"})
	path := menus + "/" + m.MenuID
	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"name": "Es Jeruk", "category_id": drinks.CategoryID, "price": 5000, "sku": "T1"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	var got models.MenuResponse
	h.do(http.MethodPut, path, "chef", body(map[string]any{"name": "Es Jeruk Nipis", "price": 6000})).status(http.StatusOK, "").decode(&got)
	if got.Name != "Es Jeruk Nipis" || got.Price != 6000 {
		t.Errorf("menu = %+v", got)
	}
	action, before, after := h.lastLog(m.MenuID)
	if action != "menu.updated" || len(after) != 2 || after["price"] != float64(6000) || before["price"] != float64(5000) {
		t.Errorf("Log Action = %s %v → %v, want the name and the price", action, before, after)
	}
	h.do(http.MethodPut, path, "chef", body(map[string]any{"name": "Es Jeruk Nipis", "price": 6000})).status(http.StatusOK, "")
	if n := len(h.actions(m.MenuID)); n != 2 {
		t.Errorf("%d entries, want no new one for no change", n)
	}

	h.do(http.MethodPut, path, "chef", body(map[string]any{"category_id": food.CategoryID})).status(http.StatusOK, "").decode(&got)
	if got.Category.CategoryID != food.CategoryID {
		t.Errorf("category = %+v", got.Category)
	}
	h.do(http.MethodPut, path, "chef", body(map[string]any{"category_id": h.otherOutletCategory()})).
		status(http.StatusUnprocessableEntity, "validation_failed")

	// Tracking starts from a count, stops keeping it, and starts again.
	h.do(http.MethodPut, path, "chef", body(map[string]any{"track_stock": true})).status(http.StatusUnprocessableEntity, "validation_failed")
	h.do(http.MethodPut, path, "chef", body(map[string]any{"track_stock": true, "initial_stock": 5, "stock_alert": 2})).
		status(http.StatusOK, "").decode(&got)
	if got.StockQty != 5 || got.StockStatus != models.StockOK || *got.StockAlert != 2 {
		t.Errorf("menu = %+v, want 5 in stock", got)
	}
	// The count only changes through adjustments once tracked.
	h.do(http.MethodPut, path, "chef", body(map[string]any{"track_stock": true, "initial_stock": 50, "stock_alert": 2})).
		status(http.StatusOK, "").decode(&got)
	if got.StockQty != 5 {
		t.Errorf("stock = %d, want the starting count ignored while tracked", got.StockQty)
	}
	h.do(http.MethodPut, path, "chef", body(map[string]any{"stock_alert": 2})).status(http.StatusOK, "").decode(&got)
	if got.TrackStock || got.StockStatus != models.StockUntracked || got.StockAlert != nil || got.StockQty != 5 {
		t.Errorf("menu = %+v, want untracked, its count kept", got)
	}
	h.do(http.MethodPut, path, "chef", body(map[string]any{"track_stock": true, "initial_stock": 8})).status(http.StatusOK, "").decode(&got)
	if got.StockQty != 8 {
		t.Errorf("stock = %d, want 8", got.StockQty)
	}
	if got := h.movements(m.MenuID); !slices.Equal(got, []string{"initial initial 5→5 by c1", "initial initial 3→8 by c1"}) {
		t.Errorf("movements = %v", got)
	}

	other := h.createMenu(map[string]any{"name": "Kopi", "category_id": drinks.CategoryID, "price": 1, "sku": "K1"})
	h.do(http.MethodPut, menus+"/"+other.MenuID, "chef", map[string]any{
		"name": "Kopi", "category_id": drinks.CategoryID, "price": 1, "sku": "t1",
	}).status(http.StatusConflict, "menu_sku_taken")
	h.do(http.MethodPut, menus+"/nope", "chef", body(nil)).status(http.StatusNotFound, "menu_not_found")
}

func TestMenuStatusAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "")
	m := h.createMenu(map[string]any{"name": "Es Jeruk", "category_id": drinks.CategoryID, "price": 5000, "sku": "T1"})
	path := menus + "/" + m.MenuID

	var got models.MenuResponse
	h.do(http.MethodPatch, path+"/status", "manager", map[string]any{"is_active": false}).status(http.StatusOK, "").decode(&got)
	if got.IsActive {
		t.Error("still active")
	}
	h.do(http.MethodPatch, path+"/status", "manager", map[string]any{"is_active": false}).status(http.StatusOK, "")
	h.do(http.MethodPatch, path+"/status", "manager", map[string]any{"is_active": true}).status(http.StatusOK, "")

	h.do(http.MethodDelete, categories+"/"+drinks.CategoryID, "manager", nil).status(http.StatusConflict, "category_in_use")
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNoContent, "")
	h.do(http.MethodGet, path, "manager", nil).status(http.StatusNotFound, "menu_not_found")
	h.do(http.MethodPatch, path+"/status", "manager", map[string]any{"is_active": false}).status(http.StatusNotFound, "menu_not_found")
	if got := h.actions(m.MenuID); !slices.Equal(got, []string{"menu.created", "menu.deactivated", "menu.activated", "menu.deleted"}) {
		t.Errorf("Log Action = %v", got)
	}
	// Its SKU is free again, and its category can go.
	h.createMenu(map[string]any{"name": "Es Jeruk", "category_id": drinks.CategoryID, "price": 5000, "sku": "T1"})
	var cat models.CategoryResponse
	h.do(http.MethodGet, categories+"/"+drinks.CategoryID, "manager", nil).status(http.StatusOK, "").decode(&cat)
	if cat.MenuCount != 1 {
		t.Errorf("menu count = %d, want the deleted menu left out", cat.MenuCount)
	}
}

func TestMenuAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "")
	m := h.createMenu(map[string]any{"name": "Es Jeruk", "category_id": drinks.CategoryID, "price": 5000})
	path := menus + "/" + m.MenuID
	body := map[string]any{"name": "Es Jeruk", "category_id": drinks.CategoryID, "price": 1}

	h.do(http.MethodGet, menus, "owner", nil).status(http.StatusOK, "")
	h.do(http.MethodGet, path, "owner", nil).status(http.StatusOK, "")
	h.do(http.MethodPut, path, "owner", body).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, path, "storekeeper", nil).status(http.StatusOK, "")
	h.do(http.MethodPut, path, "storekeeper", body).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodDelete, path, "storekeeper", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodPost, menus, "chef", body).status(http.StatusCreated, "")
	h.do(http.MethodGet, menus, "cashier", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o2/menus/"+m.MenuID, "owner", nil).status(http.StatusNotFound, "menu_not_found")
	h.do(http.MethodGet, "/api/v1/outlets/o2/menus", "manager", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, menus, "", nil).status(http.StatusUnauthorized, "unauthorized")
}
