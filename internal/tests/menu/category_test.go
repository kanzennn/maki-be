package menu

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/response"
)

const categories = "/api/v1/outlets/o1/categories"

// createCategory makes a category as the outlet's Manager and returns it.
func (h *harness) createCategory(name, icon string) models.CategoryResponse {
	h.t.Helper()
	body := map[string]any{"name": name}
	if icon != "" {
		body["icon"] = icon
	}
	var c models.CategoryResponse
	h.do(http.MethodPost, categories, "manager", body).status(http.StatusCreated, "").decode(&c)
	return c
}

func (h *harness) listCategories(who string) []models.CategoryResponse {
	h.t.Helper()
	var list response.List[models.CategoryResponse]
	h.do(http.MethodGet, categories+"?page_size=100", who, nil).status(http.StatusOK, "").decode(&list)
	return list.Data
}

func names(cs []models.CategoryResponse) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}

func TestCreateCategory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	drinks := h.createCategory("  Minuman ", "🧋")
	if drinks.Name != "Minuman" || drinks.Icon == nil || *drinks.Icon != "🧋" || !drinks.IsActive || drinks.MenuCount != 0 || drinks.SortOrder != 0 {
		t.Errorf("category = %+v", drinks)
	}
	if food := h.createCategory("Makanan", "🧑‍🍳"); food.SortOrder != 1 {
		t.Errorf("a second category at %d, want it last (1)", food.SortOrder)
	}
	if got := h.actions(drinks.CategoryID); !slices.Equal(got, []string{"category.created"}) {
		t.Errorf("Log Action = %v", got)
	}

	// Emoji of every shape: a flag, a keycap, a heart with its variation
	// selector, a skin tone.
	for i, icon := range []string{"🇮🇩", "1️⃣", "❤️", "👍🏽"} {
		h.createCategory(fmt.Sprintf("Ikon %d", i), icon)
	}

	h.do(http.MethodPost, categories, "manager", map[string]any{"name": "MINUMAN"}).status(http.StatusConflict, "category_name_taken")
	// The same name in another outlet is another category.
	h.do(http.MethodPost, "/api/v1/outlets/o2/categories", "manager2", map[string]any{"name": "Minuman"}).status(http.StatusCreated, "")

	for name, tt := range map[string]struct {
		body        map[string]any
		field, rule string
	}{
		"no name":          {map[string]any{"icon": "🍜"}, "name", "required"},
		"blank name":       {map[string]any{"name": "   "}, "name", "required"},
		"long name":        {map[string]any{"name": strings.Repeat("n", 51)}, "name", "max"},
		"a word for icon":  {map[string]any{"name": "Snack", "icon": "kopi"}, "icon", "format"},
		"icon with spaces": {map[string]any{"name": "Snack", "icon": "🍜 🍜"}, "icon", "format"},
		"punctuation":      {map[string]any{"name": "Snack", "icon": "??"}, "icon", "format"},
		"digits":           {map[string]any{"name": "Snack", "icon": "12"}, "icon", "format"},
		"icon too long":    {map[string]any{"name": "Snack", "icon": "🍜🍜🍜🍜🍜🍜🍜🍜🍜"}, "icon", "max"},
	} {
		t.Run(name, func(t *testing.T) {
			r := h.do(http.MethodPost, categories, "manager", tt.body).status(http.StatusUnprocessableEntity, "validation_failed")
			if r.fields()[tt.field] != tt.rule {
				t.Errorf("fields = %v, want %s: %s", r.fields(), tt.field, tt.rule)
			}
		})
	}
}

func TestListAndGetCategories(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "")
	food := h.createCategory("Makanan", "")
	h.createCategory("snack", "")
	h.exec(`INSERT INTO menus (menu_id, outlet_id, category_id, name, price) VALUES
		('a', 'o1', $1, 'Es Kopi', 5000), ('b', 'o1', $1, 'Es Jeruk', 7000), ('c', 'o1', $2, 'Nasi', 10000)`,
		drinks.CategoryID, food.CategoryID)
	h.exec(`UPDATE menus SET deleted_at = now() WHERE menu_id = 'b'`)
	// Two categories at the same place fall back to their names.
	h.exec(`UPDATE menu_categories SET sort_order = 0 WHERE outlet_id = 'o1'`)

	got := h.listCategories("owner")
	if !slices.Equal(names(got), []string{"Makanan", "Minuman", "snack"}) {
		t.Errorf("order = %v, want by place, then name in any case", names(got))
	}
	if got[1].MenuCount != 1 || got[0].MenuCount != 1 || got[2].MenuCount != 0 {
		t.Errorf("menu counts = %d %d %d, want deleted menus left out", got[0].MenuCount, got[1].MenuCount, got[2].MenuCount)
	}

	var page response.List[models.CategoryResponse]
	h.do(http.MethodGet, categories+"?page=2&page_size=2", "manager", nil).status(http.StatusOK, "").decode(&page)
	if page.Meta.Total != 3 || len(page.Data) != 1 || page.Data[0].Name != "snack" {
		t.Errorf("page 2 = %+v", page)
	}

	var one models.CategoryResponse
	h.do(http.MethodGet, categories+"/"+food.CategoryID, "manager", nil).status(http.StatusOK, "").decode(&one)
	if one.Name != "Makanan" || one.MenuCount != 1 {
		t.Errorf("category = %+v", one)
	}
	h.do(http.MethodGet, "/api/v1/outlets/o2/categories/"+food.CategoryID, "owner", nil).status(http.StatusNotFound, "category_not_found")
}

func TestUpdateCategory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "🧋")
	h.createCategory("Makanan", "")
	path := categories + "/" + drinks.CategoryID

	var got models.CategoryResponse
	h.do(http.MethodPut, path, "chef", map[string]any{"name": "Minuman dingin", "icon": "🧋"}).status(http.StatusOK, "").decode(&got)
	if got.Name != "Minuman dingin" || *got.Icon != "🧋" {
		t.Errorf("category = %+v", got)
	}
	action, before, after := h.lastLog(drinks.CategoryID)
	if action != "category.updated" || len(after) != 1 || after["name"] != "Minuman dingin" || before["name"] != "Minuman" {
		t.Errorf("Log Action = %s %v → %v, want only the name", action, before, after)
	}

	// Nothing changed, nothing recorded.
	h.do(http.MethodPut, path, "chef", map[string]any{"name": "Minuman dingin", "icon": "🧋"}).status(http.StatusOK, "")
	if n := len(h.actions(drinks.CategoryID)); n != 2 {
		t.Errorf("%d entries, want no new one for no change", n)
	}

	h.do(http.MethodPut, path, "chef", map[string]any{"name": "Minuman dingin"}).status(http.StatusOK, "").decode(&got)
	if got.Icon != nil {
		t.Errorf("icon = %v, want it cleared", *got.Icon)
	}
	h.do(http.MethodPut, path, "chef", map[string]any{"name": "makanan"}).status(http.StatusConflict, "category_name_taken")
	h.do(http.MethodPut, categories+"/nope", "chef", map[string]any{"name": "X"}).status(http.StatusNotFound, "category_not_found")
}

func TestCategoryStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "")
	path := categories + "/" + drinks.CategoryID + "/status"

	var got models.CategoryResponse
	h.do(http.MethodPatch, path, "manager", map[string]any{"is_active": false}).status(http.StatusOK, "").decode(&got)
	if got.IsActive {
		t.Error("still active")
	}
	h.do(http.MethodPatch, path, "manager", map[string]any{"is_active": false}).status(http.StatusOK, "")
	h.do(http.MethodPatch, path, "manager", map[string]any{"is_active": true}).status(http.StatusOK, "").decode(&got)
	if !got.IsActive {
		t.Error("not active again")
	}
	if got := h.actions(drinks.CategoryID); !slices.Equal(got, []string{"category.created", "category.deactivated", "category.activated"}) {
		t.Errorf("Log Action = %v, want nothing for a status it already had", got)
	}
	h.do(http.MethodPatch, path, "manager", map[string]any{}).status(http.StatusUnprocessableEntity, "validation_failed")
}

func TestReorderCategories(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a, b, c := h.createCategory("A", ""), h.createCategory("B", ""), h.createCategory("C", "")
	order := categories + "/order"

	h.do(http.MethodPut, order, "manager", map[string]any{
		"category_ids": []string{c.CategoryID, a.CategoryID, b.CategoryID},
	}).status(http.StatusNoContent, "")
	got := h.listCategories("manager")
	if !slices.Equal(names(got), []string{"C", "A", "B"}) || got[0].SortOrder != 0 || got[2].SortOrder != 2 {
		t.Errorf("categories = %+v, want C, A, B at 0, 1, 2", got)
	}
	action, _, after := h.lastLog("o1")
	if action != "category.reordered" || len(after["category_ids"].([]any)) != 3 {
		t.Errorf("Log Action = %s %v", action, after)
	}

	// The same order again changes and records nothing.
	h.do(http.MethodPut, order, "manager", map[string]any{
		"category_ids": []string{c.CategoryID, a.CategoryID, b.CategoryID},
	}).status(http.StatusNoContent, "")
	if n := len(h.actions("o1")); n != 1 {
		t.Errorf("%d reorder entries, want 1", n)
	}

	for name, ids := range map[string][]string{
		"one missing":     {c.CategoryID, a.CategoryID},
		"one unknown":     {c.CategoryID, a.CategoryID, b.CategoryID, "nope"},
		"one twice":       {c.CategoryID, a.CategoryID, a.CategoryID},
		"another outlets": {c.CategoryID, a.CategoryID, h.otherOutletCategory()},
	} {
		t.Run(name, func(t *testing.T) {
			h.do(http.MethodPut, order, "manager", map[string]any{"category_ids": ids}).
				status(http.StatusConflict, "categories_changed")
		})
	}
}

// otherOutletCategory returns a category of outlet o2.
func (h *harness) otherOutletCategory() string {
	h.t.Helper()
	var c models.CategoryResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/categories", "manager2", map[string]any{"name": "Lain"}).
		status(http.StatusCreated, "").decode(&c)
	return c.CategoryID
}

func TestDeleteCategory(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "")
	path := categories + "/" + drinks.CategoryID
	h.exec(`INSERT INTO menus (menu_id, outlet_id, category_id, name, price) VALUES ('a', 'o1', $1, 'Es Kopi', 5000)`, drinks.CategoryID)

	r := h.do(http.MethodDelete, path, "manager", nil).status(http.StatusConflict, "category_in_use")
	if r.envelope().Error.Details["menus"] != float64(1) {
		t.Errorf("details = %v, want the menu counted", r.envelope().Error.Details)
	}

	// A deleted menu no longer holds the category.
	h.exec(`UPDATE menus SET deleted_at = now() WHERE menu_id = 'a'`)
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNoContent, "")
	h.do(http.MethodGet, path, "manager", nil).status(http.StatusNotFound, "category_not_found")
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNotFound, "category_not_found")
	if got := h.actions(drinks.CategoryID); !slices.Equal(got, []string{"category.created", "category.deleted"}) {
		t.Errorf("Log Action = %v", got)
	}
	// Its name is free again, and the order has one fewer.
	h.createCategory("Minuman", "")
}

func TestCategoryAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	drinks := h.createCategory("Minuman", "")
	path := categories + "/" + drinks.CategoryID

	// The Owner sees every outlet's categories, and changes none.
	h.do(http.MethodGet, categories, "owner", nil).status(http.StatusOK, "")
	h.do(http.MethodGet, "/api/v1/outlets/o2/categories", "owner", nil).status(http.StatusOK, "")
	h.do(http.MethodPost, categories, "owner", map[string]any{"name": "X"}).status(http.StatusForbidden, "forbidden")

	// Stock counting is not menu editing.
	h.do(http.MethodGet, path, "storekeeper", nil).status(http.StatusOK, "")
	h.do(http.MethodPut, path, "storekeeper", map[string]any{"name": "X"}).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodDelete, path, "storekeeper", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodPut, categories+"/order", "storekeeper", map[string]any{"category_ids": []string{drinks.CategoryID}}).
		status(http.StatusForbidden, "forbidden")

	h.do(http.MethodGet, categories, "cashier", nil).status(http.StatusForbidden, "forbidden")
	h.do(http.MethodGet, "/api/v1/outlets/o2/categories", "manager", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, categories, "", nil).status(http.StatusUnauthorized, "unauthorized")
}
