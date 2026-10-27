package table

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/response"
)

// areas returns o1's areas as its Manager lists them.
func (h *harness) areas() []models.AreaResponse {
	h.t.Helper()
	var l response.List[models.AreaResponse]
	h.do(http.MethodGet, "/api/v1/outlets/o1/areas", "manager", nil).status(http.StatusOK, "").decode(&l)
	return l.Data
}

func areaNames(as []models.AreaResponse) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name
	}
	return out
}

func TestAreas(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	outdoor := h.area("  Outdoor  ")
	upstairs := h.area("Lantai 2")
	h.table(indoor.AreaID, "A1")

	got := h.areas()
	if want := []string{"Indoor", "Outdoor", "Lantai 2"}; !slices.Equal(areaNames(got), want) {
		t.Fatalf("areas = %v, want %v", areaNames(got), want)
	}
	if got[0].SortOrder != 0 || got[2].SortOrder != 2 || got[0].TableCount != 1 || got[1].TableCount != 0 {
		t.Errorf("orders and counts = %+v", got)
	}

	// The name's rules.
	for _, tt := range []struct {
		name string
		rule string
	}{{"   ", "required"}, {strings.Repeat("a", 51), "max"}} {
		r := h.do(http.MethodPost, "/api/v1/outlets/o1/areas", "manager", map[string]any{"name": tt.name})
		r.status(http.StatusUnprocessableEntity, "validation_failed")
		if r.fields()["name"] != tt.rule {
			t.Errorf("name %q: fields = %v, want %s", tt.name, r.fields(), tt.rule)
		}
	}
	h.do(http.MethodPost, "/api/v1/outlets/o1/areas", "manager", map[string]any{"name": "INDOOR"}).
		status(http.StatusConflict, "area_name_taken")

	// Renamed, then renamed to what it is: one change in the Log Action.
	path := "/api/v1/outlets/o1/areas/" + outdoor.AreaID
	var renamed models.AreaResponse
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "Teras"}).status(http.StatusOK, "").decode(&renamed)
	if renamed.Name != "Teras" {
		t.Errorf("renamed = %+v", renamed)
	}
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "Teras "}).status(http.StatusOK, "")
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "indoor"}).status(http.StatusConflict, "area_name_taken")
	entries := h.log(outdoor.AreaID)
	if want := []string{"area.created", "area.updated"}; !slices.Equal(actions(entries), want) {
		t.Fatalf("log = %v, want %v", actions(entries), want)
	}
	if entries[1].Before["name"] != "Outdoor" || entries[1].After["name"] != "Teras" || *entries[1].OutletID != "o1" {
		t.Errorf("update logged as %+v", entries[1])
	}

	// A new order; a list that misses one, or names another, is out of date.
	order := "/api/v1/outlets/o1/areas/order"
	ids := []string{upstairs.AreaID, indoor.AreaID, outdoor.AreaID}
	h.do(http.MethodPut, order, "manager", map[string]any{"area_ids": ids}).status(http.StatusNoContent, "")
	if want := []string{"Lantai 2", "Indoor", "Teras"}; !slices.Equal(areaNames(h.areas()), want) {
		t.Errorf("reordered = %v, want %v", areaNames(h.areas()), want)
	}
	h.do(http.MethodPut, order, "manager", map[string]any{"area_ids": ids[:2]}).status(http.StatusConflict, "areas_changed")
	h.do(http.MethodPut, order, "manager", map[string]any{"area_ids": []string{ids[0], ids[1], "nope"}}).
		status(http.StatusConflict, "areas_changed")
	h.do(http.MethodPut, order, "manager", map[string]any{"area_ids": ids}).status(http.StatusNoContent, "")
	reorders := h.log("o1")
	if want := []string{"area.reordered"}; !slices.Equal(actions(reorders), want) {
		t.Fatalf("reorders logged = %v, want %v", actions(reorders), want)
	}
	if after, _ := reorders[0].After["area_ids"].([]any); len(after) != 3 || after[0] != upstairs.AreaID {
		t.Errorf("reorder logged as %+v", reorders[0])
	}

	// An area with a table in it stays; an empty one goes, and its name is
	// free again.
	r := h.do(http.MethodDelete, "/api/v1/outlets/o1/areas/"+indoor.AreaID, "manager", nil)
	r.status(http.StatusConflict, "area_not_empty")
	if r.envelope().Error.Details["tables"] != float64(1) {
		t.Errorf("details = %v, want 1 table", r.envelope().Error.Details)
	}
	h.do(http.MethodDelete, "/api/v1/outlets/o1/areas/"+upstairs.AreaID, "manager", nil).status(http.StatusNoContent, "")
	h.do(http.MethodGet, "/api/v1/outlets/o1/areas/"+upstairs.AreaID, "manager", nil).status(http.StatusNotFound, "area_not_found")
	h.do(http.MethodPut, "/api/v1/outlets/o1/areas/"+upstairs.AreaID, "manager", map[string]any{"name": "X"}).
		status(http.StatusNotFound, "area_not_found")
	h.area("Lantai 2")
	if want := []string{"area.created", "area.deleted"}; !slices.Equal(actions(h.log(upstairs.AreaID)), want) {
		t.Errorf("deleted area's log = %v, want %v", actions(h.log(upstairs.AreaID)), want)
	}
}

func TestAreasAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a := h.area("Indoor")
	path := "/api/v1/outlets/o1/areas/" + a.AreaID

	// The Owner and a user with table.view look; neither changes anything.
	for _, who := range []string{"owner", "viewer"} {
		h.do(http.MethodGet, "/api/v1/outlets/o1/areas", who, nil).status(http.StatusOK, "")
		h.do(http.MethodGet, path, who, nil).status(http.StatusOK, "")
		h.do(http.MethodGet, path+"/layout", who, nil).status(http.StatusOK, "")
		h.do(http.MethodPost, "/api/v1/outlets/o1/areas", who, map[string]any{"name": "X"}).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPut, path, who, map[string]any{"name": "X"}).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPut, "/api/v1/outlets/o1/areas/order", who, map[string]any{"area_ids": []string{a.AreaID}}).
			status(http.StatusForbidden, "forbidden")
		h.do(http.MethodDelete, path, who, nil).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPut, path+"/layout", who, map[string]any{"version": 0, "tables": []any{}, "decorations": []any{}}).
			status(http.StatusForbidden, "forbidden")
	}
	// A cashier, and staff without table.view, don't read them.
	for _, who := range []string{"cashier", "staff"} {
		h.do(http.MethodGet, "/api/v1/outlets/o1/areas", who, nil).status(http.StatusForbidden, "forbidden")
	}
	// Another outlet's Manager finds no such outlet.
	h.do(http.MethodGet, "/api/v1/outlets/o1/areas", "manager2", nil).status(http.StatusNotFound, "outlet_not_found")

	// Another outlet may have an area of the same name, and its areas aren't
	// found in this one.
	var other models.AreaResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/areas", "manager2", map[string]any{"name": "Indoor"}).
		status(http.StatusCreated, "").decode(&other)
	h.do(http.MethodGet, "/api/v1/outlets/o1/areas/"+other.AreaID, "owner", nil).status(http.StatusNotFound, "area_not_found")
	h.do(http.MethodGet, "/api/v1/outlets/o1/areas/"+other.AreaID+"/layout", "manager", nil).
		status(http.StatusNotFound, "area_not_found")
	h.do(http.MethodDelete, "/api/v1/outlets/o1/areas/"+other.AreaID, "manager", nil).status(http.StatusNotFound, "area_not_found")
	if want := []string{"Indoor"}; !slices.Equal(areaNames(h.areas()), want) {
		t.Errorf("o1's areas = %v, want %v", areaNames(h.areas()), want)
	}
}
