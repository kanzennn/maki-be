package table

import (
	"maps"
	"net/http"
	"testing"

	"github.com/kervzent/maki-be/internal/models"
)

// with returns a copy of m with the fields set.
func with(m map[string]any, kv ...any) map[string]any {
	out := maps.Clone(m)
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

// byName indexes a layout's tables by name.
func byName(l models.LayoutResponse) map[string]models.TableResponse {
	out := map[string]models.TableResponse{}
	for _, t := range l.Tables {
		out[t.Name] = t
	}
	return out
}

func TestLayout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1, a2 := h.table(indoor.AreaID, "A1"), h.table(indoor.AreaID, "A2")
	h.table(indoor.AreaID, "A3", "shape", models.ShapeRound)

	// A new area: no decorations, its tables off the layout; each table
	// made moved its version on.
	l := h.layout(indoor.AreaID)
	if l.Area.Name != "Indoor" || len(l.Decorations) != 0 || len(l.Tables) != 3 || l.Tables[0].Box != nil || l.Version != 3 {
		t.Fatalf("a new area's layout = %+v", l)
	}

	// Two tables placed, one turned and long; a wall, and a door in it.
	tables := []map[string]any{
		placing(a1.TableID, 0, 0, 3, 3),
		with(placing(a2.TableID, 44, 29, 4, 3), "rotation", 90, "shape", models.ShapeRectangle),
	}
	decorations := []map[string]any{deco(models.KindWall, 0, 31, 48, 1), deco(models.KindDoor, 20, 31, 3, 1)}
	var saved models.LayoutResponse
	h.saveLayout(indoor.AreaID, l.Version, tables, decorations).status(http.StatusOK, "").decode(&saved)
	got := byName(saved)
	if saved.Version != 4 || len(saved.Decorations) != 2 || saved.Decorations[1] != (models.DecorationResponse{
		Kind: models.KindDoor, X: 20, Y: 31, W: 3, H: 1,
	}) {
		t.Errorf("saved = version %d, decorations %+v", saved.Version, saved.Decorations)
	}
	if b := got["A1"].Box; b == nil || *b != (models.BoxResponse{X: 0, Y: 0, W: 3, H: 3}) {
		t.Errorf("A1 = %+v", got["A1"])
	}
	if a := got["A2"]; a.Box == nil || *a.Box != (models.BoxResponse{X: 44, Y: 29, W: 4, H: 3}) || a.Rotation != 90 || a.Shape != models.ShapeRectangle {
		t.Errorf("A2 = %+v", a)
	}
	if got["A3"].Box != nil {
		t.Errorf("A3 = %+v, want it off the layout", got["A3"])
	}
	entries := h.log(indoor.AreaID)
	e := entries[len(entries)-1]
	if e.Action != "area.layout_updated" || e.Before["A1"] != nil || e.Before["decorations"] != float64(0) ||
		e.After["decorations"] != float64(2) {
		t.Fatalf("layout logged as %+v", e)
	}
	if moved, _ := e.After["A2"].(map[string]any); moved["x"] != float64(44) || moved["rotation"] != float64(90) {
		t.Errorf("A2 logged as %v", e.After["A2"])
	}
	if _, ok := e.After["A3"]; ok {
		t.Errorf("A3, off the layout and staying off, logged as %v", e.After["A3"])
	}

	// Saved as it is: nothing recorded, and the version kept.
	var again models.LayoutResponse
	h.saveLayout(indoor.AreaID, saved.Version, tables, decorations).status(http.StatusOK, "").decode(&again)
	if again.Version != saved.Version || len(h.log(indoor.AreaID)) != len(entries) {
		t.Errorf("saved unchanged = version %d, %d log entries", again.Version, len(h.log(indoor.AreaID)))
	}

	// The door moved along the wall: as many decorations as before.
	decorations[1] = deco(models.KindDoor, 30, 31, 3, 1)
	var redecorated models.LayoutResponse
	h.saveLayout(indoor.AreaID, saved.Version, tables, decorations).status(http.StatusOK, "").decode(&redecorated)
	if redecorated.Version != saved.Version+1 || redecorated.Decorations[1].X != 30 {
		t.Errorf("the door moved = version %d, %+v", redecorated.Version, redecorated.Decorations)
	}
	saved = redecorated

	// A table left out comes off the layout.
	var fewer models.LayoutResponse
	h.saveLayout(indoor.AreaID, saved.Version, tables[:1], decorations).status(http.StatusOK, "").decode(&fewer)
	if byName(fewer)["A2"].Box != nil {
		t.Errorf("A2 left out = %+v", byName(fewer)["A2"])
	}
	entries = h.log(indoor.AreaID)
	if e := entries[len(entries)-1]; e.Before["A2"] == nil || e.After["A2"] != nil || len(e.After) != 1 {
		t.Errorf("A2 taken off logged as %+v", e)
	}

	// An editor opened before a change can't save over it: a layout saved
	// since, or a table made.
	r := h.saveLayout(indoor.AreaID, saved.Version, nil, nil)
	r.status(http.StatusConflict, "layout_changed")
	if r.envelope().Error.Details["version"] != float64(fewer.Version) {
		t.Errorf("details = %v, want version %d", r.envelope().Error.Details, fewer.Version)
	}
	h.table(indoor.AreaID, "A4")
	h.saveLayout(indoor.AreaID, fewer.Version, nil, nil).status(http.StatusConflict, "layout_changed")

	// A change to a table moves the version on; turning it off doesn't,
	// since the layout doesn't show it.
	version := h.layout(indoor.AreaID).Version
	h.do(http.MethodPatch, "/api/v1/outlets/o1/tables/"+a1.TableID+"/status", "manager", map[string]any{"is_active": false}).
		status(http.StatusOK, "")
	if v := h.layout(indoor.AreaID).Version; v != version {
		t.Errorf("after turning a table off, version %d, want %d", v, version)
	}
	h.do(http.MethodPut, "/api/v1/outlets/o1/tables/"+a1.TableID, "manager", map[string]any{
		"name": "A1", "area_id": indoor.AreaID, "shape": models.ShapeRound,
	}).status(http.StatusOK, "")
	if v := h.layout(indoor.AreaID).Version; v != version+1 {
		t.Errorf("after changing a table's shape, version %d, want %d", v, version+1)
	}
	if b := h.get(a1.TableID).Box; b == nil || b.W != 3 {
		t.Errorf("a table changed in the form keeps its place: %+v", b)
	}
	h.do(http.MethodDelete, "/api/v1/outlets/o1/tables/"+a2.TableID, "manager", nil).status(http.StatusNoContent, "")
	if v := h.layout(indoor.AreaID).Version; v != version+2 {
		t.Errorf("after deleting a table, version %d, want %d", v, version+2)
	}
}

func TestLayoutRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor, terrace := h.area("Indoor"), h.area("Teras")
	a1, a2 := h.table(indoor.AreaID, "A1").TableID, h.table(indoor.AreaID, "A2").TableID
	t1 := h.table(terrace.AreaID, "T1").TableID
	version := h.layout(indoor.AreaID).Version
	square := placing(a1, 0, 0, 3, 3)

	for _, tt := range []struct {
		name        string
		tables      []map[string]any
		decorations []map[string]any
		want        map[string]string
	}{
		{"past the right edge", []map[string]any{placing(a1, 46, 0, 3, 3)}, nil, map[string]string{"tables.0.x": "range"}},
		{"past the bottom edge", []map[string]any{placing(a1, 0, 30, 3, 3)}, nil, map[string]string{"tables.0.y": "range"}},
		{"left of the grid", []map[string]any{placing(a1, -1, 0, 3, 3)}, nil, map[string]string{"tables.0.x": "range"}},
		{"above the grid", []map[string]any{placing(a1, 0, -1, 3, 3)}, nil, map[string]string{"tables.0.y": "range"}},
		{"one cell wide", []map[string]any{placing(a1, 0, 0, 1, 3)}, nil, map[string]string{"tables.0.w": "range"}},
		{"seventeen cells tall", []map[string]any{placing(a1, 0, 0, 3, 17)}, nil, map[string]string{"tables.0.h": "range"}},
		{"a turn that isn't a quarter", []map[string]any{with(square, "rotation", 45)}, nil, map[string]string{"tables.0.rotation": "oneof"}},
		{"an unknown shape", []map[string]any{with(square, "shape", "oval")}, nil, map[string]string{"tables.0.shape": "oneof"}},
		{"its fields left out", []map[string]any{{"table_id": a1}}, nil, map[string]string{
			"tables.0.x": "required", "tables.0.y": "required", "tables.0.w": "required", "tables.0.h": "required",
			"tables.0.rotation": "required", "tables.0.shape": "required",
		}},
		{"no table", []map[string]any{with(square, "table_id", "")}, nil, map[string]string{"tables.0.table_id": "required"}},
		{"a table twice", []map[string]any{square, placing(a1, 10, 0, 3, 3)}, nil, map[string]string{"tables.1.table_id": "duplicate"}},
		{"tables on top of each other", []map[string]any{square, placing(a2, 2, 2, 3, 3)}, nil, map[string]string{
			"tables.0": "overlap", "tables.1": "overlap",
		}},
		{"another area's table", []map[string]any{placing(t1, 0, 0, 3, 3)}, nil, map[string]string{"tables.0.table_id": "exists"}},
		{"a table that isn't there", []map[string]any{placing("nope", 0, 0, 3, 3)}, nil, map[string]string{"tables.0.table_id": "exists"}},
		{"an unknown decoration", nil, []map[string]any{deco("plant", 0, 0, 1, 1)}, map[string]string{"decorations.0.kind": "oneof"}},
		{"a wall wider than the grid", nil, []map[string]any{deco(models.KindWall, 0, 0, 49, 1)}, map[string]string{
			"decorations.0.w": "range", "decorations.0.x": "range",
		}},
		{"a door past the bottom", nil, []map[string]any{deco(models.KindDoor, 0, 32, 2, 1)}, map[string]string{"decorations.0.y": "range"}},
		{"a decoration without a size", nil, []map[string]any{deco(models.KindCashier, 0, 0, 0, 1)}, map[string]string{"decorations.0.w": "range"}},
		{"a decoration turned 45°", nil, []map[string]any{with(deco(models.KindDoor, 0, 0, 2, 1), "rotation", 45)}, map[string]string{
			"decorations.0.rotation": "oneof",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := h.saveLayout(indoor.AreaID, version, tt.tables, tt.decorations)
			r.status(http.StatusUnprocessableEntity, "validation_failed")
			got := r.fields()
			if len(got) != len(tt.want) {
				t.Errorf("fields = %v, want %v", got, tt.want)
			}
			for field, rule := range tt.want {
				if got[field] != rule {
					t.Errorf("fields = %v, want %s: %s", got, field, rule)
				}
			}
		})
	}

	tooMany := make([]map[string]any, models.MaxDecorations+1)
	for i := range tooMany {
		tooMany[i] = deco(models.KindWall, 0, 0, 1, 1)
	}
	if r := h.saveLayout(indoor.AreaID, version, nil, tooMany); r.fields()["decorations"] != "max" {
		t.Errorf("too many decorations: %s", r.Body)
	}
	// Both lists are required, empty or not; so is the version.
	r := h.do(http.MethodPut, "/api/v1/outlets/o1/areas/"+indoor.AreaID+"/layout", "manager", map[string]any{"version": version})
	r.status(http.StatusUnprocessableEntity, "validation_failed")
	if f := r.fields(); f["tables"] != "required" || f["decorations"] != "required" {
		t.Errorf("lists left out: %v", f)
	}
	r = h.do(http.MethodPut, "/api/v1/outlets/o1/areas/"+indoor.AreaID+"/layout", "manager", map[string]any{
		"tables": []any{}, "decorations": []any{},
	})
	if r.fields()["version"] != "required" {
		t.Errorf("version left out: %s", r.Body)
	}
	if v := h.layout(indoor.AreaID).Version; v != version {
		t.Errorf("after refusals, version %d, want %d", v, version)
	}

	// Tables side by side don't overlap; a decoration may lie under one.
	h.saveLayout(indoor.AreaID, version, []map[string]any{square, placing(a2, 3, 0, 3, 3)},
		[]map[string]any{deco(models.KindCashier, 1, 1, 4, 1)}).status(http.StatusOK, "")
	h.saveLayout(terrace.AreaID, h.layout(terrace.AreaID).Version, nil, nil).status(http.StatusOK, "")
	h.do(http.MethodPut, "/api/v1/outlets/o1/areas/nope/layout", "manager", map[string]any{
		"version": 0, "tables": []any{}, "decorations": []any{},
	}).status(http.StatusNotFound, "area_not_found")
}
