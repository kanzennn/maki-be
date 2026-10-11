package table

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestTables(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor, terrace := h.area("Indoor"), h.area("Teras")

	a1 := h.table(indoor.AreaID, "  Meja 1 ")
	if a1.Name != "Meja 1" || a1.Shape != ShapeSquare || !a1.IsActive || a1.Box != nil || a1.Capacity != nil ||
		a1.Status != string(Available) || a1.Seated != nil || a1.Area.Name != "Indoor" {
		t.Errorf("a new table = %+v", a1)
	}
	vip := h.table(terrace.AreaID, "VIP", "capacity", 8, "shape", ShapeRectangle, "is_active", false)
	if *vip.Capacity != 8 || vip.Shape != ShapeRectangle || vip.IsActive || vip.Status != string(Inactive) {
		t.Errorf("a table filled in = %+v", vip)
	}
	h.table(indoor.AreaID, "Meja 10")
	h.table(indoor.AreaID, "Meja 2")

	// By area, then by name as people read the numbers.
	list := "/api/v1/outlets/o1/tables"
	all := h.do(http.MethodGet, list, "manager", nil).status(http.StatusOK, "").tables()
	if want := []string{"Meja 1", "Meja 2", "Meja 10", "VIP"}; !slices.Equal(names(all.Data), want) || all.Meta.Total != 4 {
		t.Fatalf("tables = %v (%d), want %v", names(all.Data), all.Meta.Total, want)
	}
	for _, tt := range []struct {
		query string
		want  []string
	}{
		{"?area_id=" + terrace.AreaID, []string{"VIP"}},
		{"?status=inactive", []string{"VIP"}},
		{"?status=available&q=MEJA", []string{"Meja 1", "Meja 2", "Meja 10"}},
		{"?q=1", []string{"Meja 1", "Meja 10"}},
		{"?page=2&page_size=3", []string{"VIP"}},
	} {
		got := h.do(http.MethodGet, list+tt.query, "manager", nil).status(http.StatusOK, "").tables()
		if !slices.Equal(names(got.Data), tt.want) {
			t.Errorf("%s = %v, want %v", tt.query, names(got.Data), tt.want)
		}
	}
	h.do(http.MethodGet, list+"?status=busy", "manager", nil).status(http.StatusUnprocessableEntity, "validation_failed")
	// The areas' order, not their names'.
	h.do(http.MethodPut, "/api/v1/outlets/o1/areas/order", "manager", map[string]any{
		"area_ids": []string{terrace.AreaID, indoor.AreaID},
	}).status(http.StatusNoContent, "")
	all = h.do(http.MethodGet, list, "manager", nil).status(http.StatusOK, "").tables()
	if want := []string{"VIP", "Meja 1", "Meja 2", "Meja 10"}; !slices.Equal(names(all.Data), want) {
		t.Errorf("with Teras first = %v, want %v", names(all.Data), want)
	}

	// The form's rules, every problem at once.
	r := h.do(http.MethodPost, list, "manager", map[string]any{
		"name": strings.Repeat("a", 31), "area_id": "nope", "capacity": 100, "shape": "oval",
	})
	r.status(http.StatusUnprocessableEntity, "validation_failed")
	if f := r.fields(); f["name"] != "max" || f["capacity"] != "range" || f["shape"] != "oneof" {
		t.Errorf("fields = %v", f)
	}
	r = h.do(http.MethodPost, list, "manager", map[string]any{"name": "B1", "area_id": "nope"})
	r.status(http.StatusUnprocessableEntity, "validation_failed")
	if r.fields()["area_id"] != "exists" {
		t.Errorf("an area that isn't there: %v", r.fields())
	}
	r = h.do(http.MethodPost, list, "manager", map[string]any{"name": "B1", "area_id": indoor.AreaID, "capacity": 0})
	if r.fields()["capacity"] != "range" {
		t.Errorf("no seats: %v", r.fields())
	}
	h.do(http.MethodPost, list, "manager", map[string]any{"name": "meja 1", "area_id": terrace.AreaID}).
		status(http.StatusConflict, "table_name_taken")

	// Changed, then changed to what it is: one change in the Log Action.
	path := "/api/v1/outlets/o1/tables/" + a1.TableID
	var changed tableResponse
	h.do(http.MethodPut, path, "manager", map[string]any{
		"name": "Meja 1", "area_id": indoor.AreaID, "capacity": 4, "shape": ShapeRound,
	}).status(http.StatusOK, "").decode(&changed)
	if *changed.Capacity != 4 || changed.Shape != ShapeRound {
		t.Errorf("changed = %+v", changed)
	}
	h.do(http.MethodPut, path, "manager", map[string]any{
		"name": "Meja 1", "area_id": indoor.AreaID, "capacity": 4, "shape": ShapeRound,
	}).status(http.StatusOK, "")
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "VIP", "area_id": indoor.AreaID}).
		status(http.StatusConflict, "table_name_taken")
	h.do(http.MethodPut, path, "manager", map[string]any{"name": "Meja 1", "area_id": "nope"}).
		status(http.StatusUnprocessableEntity, "validation_failed")

	// Turned off and on, each once in the Log Action.
	status := path + "/status"
	for _, active := range []bool{false, false, true} {
		h.do(http.MethodPatch, status, "manager", map[string]any{"is_active": active}).status(http.StatusOK, "")
	}
	h.do(http.MethodPatch, status, "manager", map[string]any{}).status(http.StatusUnprocessableEntity, "validation_failed")

	entries := h.log(a1.TableID)
	if want := []string{"table.created", "table.updated", "table.deactivated", "table.activated"}; !slices.Equal(actions(entries), want) {
		t.Fatalf("log = %v, want %v", actions(entries), want)
	}
	if e := entries[0]; e.After["name"] != "Meja 1" || e.After["shape"] != ShapeSquare || e.After["is_active"] != true ||
		e.After["capacity"] != nil {
		t.Errorf("created logged as %+v", e)
	}
	if e := entries[1]; e.Before["capacity"] != nil || e.After["capacity"] != float64(4) || e.After["shape"] != ShapeRound ||
		len(e.After) != 2 {
		t.Errorf("updated logged as %+v", e)
	}

	// Gone, and its name free again.
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNoContent, "")
	h.do(http.MethodGet, path, "manager", nil).status(http.StatusNotFound, "table_not_found")
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNotFound, "table_not_found")
	h.do(http.MethodPatch, status, "manager", map[string]any{"is_active": false}).status(http.StatusNotFound, "table_not_found")
	h.table(terrace.AreaID, "Meja 1")
	if e := h.log(a1.TableID); e[len(e)-1].Action != "table.deleted" || e[len(e)-1].Before["name"] != "Meja 1" {
		t.Errorf("deleted logged as %+v", e[len(e)-1])
	}
}

// A table that moves to another area leaves its place on the old one's
// layout, and both layouts move on.
func TestTableMovesArea(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor, terrace := h.area("Indoor"), h.area("Teras")
	a1 := h.table(indoor.AreaID, "A1")
	h.saveLayout(indoor.AreaID, h.layout(indoor.AreaID).Version, []map[string]any{placing(a1.TableID, 1, 1, 3, 3)}, nil).
		status(http.StatusOK, "")
	indoorVersion, terraceVersion := h.layout(indoor.AreaID).Version, h.layout(terrace.AreaID).Version

	var moved tableResponse
	h.do(http.MethodPut, "/api/v1/outlets/o1/tables/"+a1.TableID, "manager", map[string]any{
		"name": "A1", "area_id": terrace.AreaID,
	}).status(http.StatusOK, "").decode(&moved)
	if moved.Area.AreaID != terrace.AreaID || moved.Area.Name != "Teras" || moved.Box != nil {
		t.Errorf("moved = %+v", moved)
	}
	if v := h.layout(indoor.AreaID).Version; v <= indoorVersion {
		t.Errorf("the old area's layout = version %d, want past %d", v, indoorVersion)
	}
	if v := h.layout(terrace.AreaID).Version; v <= terraceVersion {
		t.Errorf("the new area's layout = version %d, want past %d", v, terraceVersion)
	}
	if e := h.log(a1.TableID); e[len(e)-1].Before["area_id"] != indoor.AreaID || e[len(e)-1].After["area_id"] != terrace.AreaID {
		t.Errorf("move logged as %+v", e[len(e)-1])
	}
}

func TestTableStatus(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1 := h.table(indoor.AreaID, "A1")

	// A sale seats customers until the table is released.
	h.orders.sell("o1", a1.TableID, h.at(0, "11:00"))
	h.orders.sell("o1", a1.TableID, h.at(0, "11:40"))
	got := h.get(a1.TableID)
	if got.Status != string(Occupied) || got.Seated == nil || !got.Seated.Since.Equal(h.at(0, "11:00")) || got.Seated.Orders != 2 {
		t.Fatalf("after two sales = %+v %+v", got, got.Seated)
	}
	h.exec(`UPDATE tables SET released_at = $1 WHERE table_id = $2`, h.at(0, "11:50"), a1.TableID)
	if got := h.get(a1.TableID); got.Status != string(Available) || got.Seated != nil {
		t.Errorf("released = %+v", got)
	}
	h.orders.sell("o1", a1.TableID, h.at(0, "11:55"))
	if got := h.get(a1.TableID); got.Status != string(Occupied) || got.Seated.Orders != 1 {
		t.Errorf("a sale after the release = %+v", got)
	}

	// Turned off while customers are seated: occupied until released.
	h.do(http.MethodPatch, "/api/v1/outlets/o1/tables/"+a1.TableID+"/status", "manager", map[string]any{"is_active": false}).
		status(http.StatusOK, "")
	if got := h.get(a1.TableID); got.Status != string(Occupied) || got.IsActive {
		t.Errorf("turned off while seated = %+v", got)
	}
	h.exec(`UPDATE tables SET released_at = $1 WHERE table_id = $2`, h.at(0, "11:58"), a1.TableID)
	if got := h.get(a1.TableID); got.Status != string(Inactive) {
		t.Errorf("turned off, then released = %s", got.Status)
	}
	h.do(http.MethodPatch, "/api/v1/outlets/o1/tables/"+a1.TableID+"/status", "manager", map[string]any{"is_active": true}).
		status(http.StatusOK, "")

	// A schedule under way has it out of service; one cancelled, or over,
	// doesn't.
	h.exec(`INSERT INTO table_schedules (schedule_id, outlet_id, title, start_at, end_at, created_by) VALUES
		('now', 'o1', 'Acara', $1, $2, 'm1'), ('over', 'o1', 'Lalu', $3, $1, 'm1')`,
		h.at(0, "11:00"), h.at(0, "13:00"), h.at(0, "09:00"))
	h.exec(`INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('over', $1, 'o1')`, a1.TableID)
	if got := h.get(a1.TableID); got.Status != string(Available) {
		t.Errorf("a schedule over = %s", got.Status)
	}
	h.exec(`INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('now', $1, 'o1')`, a1.TableID)
	if got := h.get(a1.TableID); got.Status != string(Scheduled) {
		t.Errorf("a schedule under way = %s", got.Status)
	}
	h.orders.sell("o1", a1.TableID, h.at(0, "11:59"))
	if got := h.get(a1.TableID); got.Status != string(Occupied) {
		t.Errorf("seated while scheduled = %s", got.Status)
	}
	h.exec(`UPDATE table_schedules SET cancelled_at = $1, cancelled_by = 'm1' WHERE schedule_id = 'now'`, h.at(0, "11:59"))
	h.exec(`UPDATE tables SET released_at = $1 WHERE table_id = $2`, h.at(0, "11:59"), a1.TableID)
	if got := h.get(a1.TableID); got.Status != string(Available) {
		t.Errorf("the schedule cancelled = %s", got.Status)
	}
	if got := h.do(http.MethodGet, "/api/v1/outlets/o1/tables?status=available", "manager", nil).tables(); len(got.Data) != 1 {
		t.Errorf("available tables = %v", names(got.Data))
	}
}

// A table empties itself when the outlet closes, and one sold at after
// closing stays occupied until it opens again; without hours, at midnight.
func TestTableEmptiesAtClosing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1, a2 := h.table(indoor.AreaID, "A1"), h.table(indoor.AreaID, "A2")
	// Every time first: h.at counts from the clock, which moves below.
	ninePM, closing, lastSale, elevenPM := h.at(0, "21:00"), h.at(0, "22:00"), h.at(0, "22:10"), h.at(0, "23:00")
	beforeOpening, opening := h.at(1, "09:59"), h.at(1, "10:00")
	check := func(clock time.Time, wantA1, wantA2 Status) {
		t.Helper()
		h.clock = clock
		if got := h.get(a1.TableID).Status; got != string(wantA1) {
			t.Errorf("at %s, A1 = %s, want %s", clock.Format(time.DateTime), got, wantA1)
		}
		if got := h.get(a2.TableID).Status; got != string(wantA2) {
			t.Errorf("at %s, A2 = %s, want %s", clock.Format(time.DateTime), got, wantA2)
		}
	}

	h.clock = ninePM
	h.orders.sell("o1", a1.TableID, ninePM)
	check(closing.Add(-time.Minute), Occupied, Available)
	check(closing, Available, Available)
	// The last customers, after closing.
	h.clock = lastSale
	h.orders.sell("o1", a2.TableID, lastSale)
	check(elevenPM, Available, Occupied)
	check(beforeOpening, Available, Occupied)
	check(opening, Available, Available)

	// Jakarta's outlet has no hours: its tables empty themselves at
	// midnight.
	var b1 tableResponse
	var area areaResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/areas", "manager2", map[string]any{"name": "Indoor"}).
		status(http.StatusCreated, "").decode(&area)
	h.do(http.MethodPost, "/api/v1/outlets/o2/tables", "manager2", map[string]any{"name": "B1", "area_id": area.AreaID}).
		status(http.StatusCreated, "").decode(&b1)
	late, beforeMidnight, midnight := h.at(0, "23:30"), h.at(0, "23:59"), h.at(1, "00:00")
	h.clock = late
	h.orders.sell("o2", b1.TableID, late)
	for _, tt := range []struct {
		clock time.Time
		want  Status
	}{{beforeMidnight, Occupied}, {midnight, Available}} {
		h.clock = tt.clock
		var got tableResponse
		h.do(http.MethodGet, "/api/v1/outlets/o2/tables/"+b1.TableID, "manager2", nil).status(http.StatusOK, "").decode(&got)
		if got.Status != string(tt.want) {
			t.Errorf("at %s, B1 = %s, want %s", tt.clock.Format(time.DateTime), got.Status, tt.want)
		}
	}
}

// A void of the last sale at a table empties it; nobody occupied stays
// deletable, and seated customers keep their table.
func TestTableDeletedOnlyWhenEmpty(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1 := h.table(indoor.AreaID, "A1")
	path := "/api/v1/outlets/o1/tables/" + a1.TableID
	sale := h.at(0, "11:30")
	h.orders.sell("o1", a1.TableID, sale)
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusConflict, "table_occupied")
	h.orders.void("o1", a1.TableID, sale)
	if got := h.get(a1.TableID).Status; got != string(Available) {
		t.Errorf("its sale voided = %s", got)
	}
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNoContent, "")
	// Released after its sale: nobody is seated, so it can go.
	a2 := h.table(indoor.AreaID, "A2")
	h.orders.sell("o1", a2.TableID, sale)
	h.exec(`UPDATE tables SET released_at = $1 WHERE table_id = $2`, h.at(0, "11:45"), a2.TableID)
	h.do(http.MethodDelete, "/api/v1/outlets/o1/tables/"+a2.TableID, "manager", nil).status(http.StatusNoContent, "")
	var count int
	h.scalar(&count, `SELECT count(*) FROM action_logs WHERE target_id = $1 AND action = 'table.deleted'`, a1.TableID)
	if count != 1 {
		t.Errorf("deletes logged = %d", count)
	}
}

func TestTablesAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1 := h.table(indoor.AreaID, "A1")
	path := "/api/v1/outlets/o1/tables/" + a1.TableID
	body := map[string]any{"name": "X", "area_id": indoor.AreaID}

	for _, who := range []string{"owner", "viewer"} {
		h.do(http.MethodGet, "/api/v1/outlets/o1/tables", who, nil).status(http.StatusOK, "")
		h.do(http.MethodGet, path, who, nil).status(http.StatusOK, "")
		h.do(http.MethodPost, "/api/v1/outlets/o1/tables", who, body).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPut, path, who, body).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPatch, path+"/status", who, map[string]any{"is_active": false}).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodDelete, path, who, nil).status(http.StatusForbidden, "forbidden")
	}
	for _, who := range []string{"cashier", "staff"} {
		h.do(http.MethodGet, "/api/v1/outlets/o1/tables", who, nil).status(http.StatusForbidden, "forbidden")
	}
	h.do(http.MethodGet, path, "manager2", nil).status(http.StatusNotFound, "outlet_not_found")

	// Another outlet's area and tables aren't this one's.
	var area areaResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/areas", "manager2", map[string]any{"name": "Indoor"}).
		status(http.StatusCreated, "").decode(&area)
	r := h.do(http.MethodPost, "/api/v1/outlets/o1/tables", "manager", map[string]any{"name": "B1", "area_id": area.AreaID})
	r.status(http.StatusUnprocessableEntity, "validation_failed")
	if r.fields()["area_id"] != "exists" {
		t.Errorf("another outlet's area: %v", r.fields())
	}
	h.do(http.MethodGet, "/api/v1/outlets/o2/tables/"+a1.TableID, "manager2", nil).status(http.StatusNotFound, "table_not_found")
	h.do(http.MethodPut, "/api/v1/outlets/o2/tables/"+a1.TableID, "manager2", map[string]any{"name": "X", "area_id": area.AreaID}).
		status(http.StatusNotFound, "table_not_found")
}

// A table that moves to another area while a change to it waits for its
// area is locked again where it is now, in order: the change never holds
// the table while it waits for an area, so a layout being saved there,
// which holds the area and then takes its tables, can't deadlock with it.
func TestTableMovedWhileWaiting(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	a, b, c := h.area("A"), h.area("B"), h.area("C")
	t1 := h.table(a.AreaID, "T1")
	version := h.layout(c.AreaID).Version
	ctx := context.WithoutCancel(t.Context())
	begin := func() (pgx.Tx, int) {
		t.Helper()
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback(ctx) })
		var pid int
		if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		return tx, pid
	}
	exec := func(tx pgx.Tx, sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	// C is held, as by a layout being saved there (its key aside, so the
	// table can still move into it); A too.
	holdC, cPID := begin()
	exec(holdC, `UPDATE table_areas SET layout_version = layout_version WHERE area_id = $1`, c.AreaID)
	holdA, aPID := begin()
	exec(holdA, `SELECT 1 FROM table_areas WHERE area_id = $1 FOR UPDATE`, a.AreaID)

	done := make(chan response)
	go func() {
		done <- h.do(http.MethodPut, "/api/v1/outlets/o1/tables/"+t1.TableID, "manager", map[string]any{
			"name": "T1", "area_id": b.AreaID,
		})
	}()
	h.waitBlockedBy(aPID)
	// Meanwhile T1 moves to C.
	exec(holdA, `UPDATE tables SET area_id = $1 WHERE table_id = $2`, c.AreaID, t1.TableID)
	if err := holdA.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// The change waits for C now, without T1: the save there takes it.
	h.waitBlockedBy(cPID)
	exec(holdC, `SET LOCAL lock_timeout = '3s'`)
	exec(holdC, `SELECT 1 FROM tables WHERE table_id = $1 FOR UPDATE`, t1.TableID)
	if err := holdC.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	(<-done).status(http.StatusOK, "")
	if got := h.get(t1.TableID); got.Area.AreaID != b.AreaID {
		t.Errorf("T1 is in %s, want B", got.Area.Name)
	}
	if v := h.layout(c.AreaID).Version; v <= version {
		t.Errorf("C's layout = version %d, want past %d: the table left it", v, version)
	}
	if e := h.log(t1.TableID); e[len(e)-1].Before["area_id"] != c.AreaID {
		t.Errorf("the move logged as %+v, want it from C", e[len(e)-1])
	}
}
