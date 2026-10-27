package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/tests/dbtest"
)

// insertArea inserts an area of the outlet, first in the order, with no
// decorations.
func insertArea(t *testing.T, pool *pgxpool.Pool, id, outletID, name string, with map[string]any) error {
	t.Helper()
	return insertRow(t, pool, "table_areas", map[string]any{"area_id": id, "outlet_id": outletID, "name": name}, with)
}

// insertTable inserts an active square table of the area, not placed yet.
func insertTable(t *testing.T, pool *pgxpool.Pool, id, outletID, areaID, name string, with map[string]any) error {
	t.Helper()
	return insertRow(t, pool, "tables", map[string]any{
		"table_id": id, "outlet_id": outletID, "area_id": areaID, "name": name,
	}, with)
}

// box is a table's place on the grid.
func box(x, y, w, h int) map[string]any {
	return map[string]any{"x": x, "y": y, "w": w, "h": h}
}

// insertSchedule inserts a schedule of o1 by u1, from an hour from now for
// an hour.
func insertSchedule(t *testing.T, pool *pgxpool.Pool, id string, with map[string]any) error {
	t.Helper()
	start := time.Now().Add(time.Hour)
	return insertRow(t, pool, "table_schedules", map[string]any{
		"schedule_id": id, "outlet_id": "o1", "title": "Acara kantor", "start_at": start,
		"end_at": start.Add(time.Hour), "created_by": "u1",
	}, with)
}

// tables sets up the cashier's two outlets, an area of each (a1 in o1, a2
// in o2), and a table t1 in a1.
func tables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	cashier(t, pool)
	for _, err := range []error{
		insertArea(t, pool, "a1", "o1", "Indoor", nil),
		insertArea(t, pool, "a2", "o2", "Indoor", nil),
		insertTable(t, pool, "t1", "o1", "a1", "A1", nil),
	} {
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
}

func TestTableAreasRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	for _, err := range []error{
		insertArea(t, pool, "ok1", "o1", "Indoor", nil),
		insertArea(t, pool, "ok2", "o1", "Lantai 2", map[string]any{
			"sort_order": 1, "decorations": `[{"kind": "door", "x": 0, "y": 0, "w": 2, "h": 1, "rotation": 0}]`,
		}),
		// Another outlet's, and a deleted one's name: both free.
		insertArea(t, pool, "ok3", "o2", "Indoor", nil),
		insertArea(t, pool, "ok4", "o1", "Teras", map[string]any{"deleted_at": time.Now()}),
		insertArea(t, pool, "ok5", "o1", "Teras", nil),
	} {
		if err != nil {
			t.Fatalf("valid areas: %v", err)
		}
	}

	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"an empty name", insertArea(t, pool, "b1", "o1", "", nil), "table_areas_name_check"},
		{"a name too long", insertArea(t, pool, "b2", "o1", strings.Repeat("a", 51), nil), "table_areas_name_check"},
		{"an order below zero", insertArea(t, pool, "b3", "o1", "B3", map[string]any{"sort_order": -1}), "table_areas_sort_order_check"},
		{"decorations that aren't a list", insertArea(t, pool, "b4", "o1", "B4", map[string]any{"decorations": `{"kind": "door"}`}), "table_areas_decorations_check"},
		{"a version below zero", insertArea(t, pool, "b5", "o1", "B5", map[string]any{"layout_version": -1}), "table_areas_layout_version_check"},
		{"a name twice, in another case", insertArea(t, pool, "b6", "o1", "INDOOR", nil), "table_areas_outlet_name_key"},
		{"an outlet that doesn't exist", insertArea(t, pool, "b7", "nope", "B7", nil), "table_areas_outlet_id_fkey"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestTablesRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	tables(t, pool)
	for _, err := range []error{
		insertTable(t, pool, "ok1", "o1", "a1", "Meja 12", map[string]any{"capacity": 4, "shape": "round"}),
		// In the grid's far corner, and as large as a table goes.
		insertTable(t, pool, "ok2", "o1", "a1", "A2", box(46, 30, 2, 2)),
		insertTable(t, pool, "ok3", "o1", "a1", "Panjang", map[string]any{
			"x": 0, "y": 0, "w": 16, "h": 16, "shape": "rectangle", "rotation": 270, "capacity": 99,
		}),
		insertTable(t, pool, "ok4", "o1", "a1", "Lama", map[string]any{"is_active": false, "released_at": time.Now()}),
		// Another outlet's, and a deleted one's name: both free.
		insertTable(t, pool, "ok5", "o2", "a2", "A1", nil),
		insertTable(t, pool, "ok6", "o1", "a1", "B1", map[string]any{"deleted_at": time.Now()}),
		insertTable(t, pool, "ok7", "o1", "a1", "B1", nil),
	} {
		if err != nil {
			t.Fatalf("valid tables: %v", err)
		}
	}

	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"an empty name", insertTable(t, pool, "b1", "o1", "a1", "", nil), "tables_name_check"},
		{"a name too long", insertTable(t, pool, "b2", "o1", "a1", strings.Repeat("a", 31), nil), "tables_name_check"},
		{"no seats", insertTable(t, pool, "b3", "o1", "a1", "B3", map[string]any{"capacity": 0}), "tables_capacity_check"},
		{"a hundred seats", insertTable(t, pool, "b4", "o1", "a1", "B4", map[string]any{"capacity": 100}), "tables_capacity_check"},
		{"an unknown shape", insertTable(t, pool, "b5", "o1", "a1", "B5", map[string]any{"shape": "oval"}), "tables_shape_check"},
		{"a turn that isn't a quarter", insertTable(t, pool, "b6", "o1", "a1", "B6", map[string]any{"rotation": 45}), "tables_rotation_check"},
		{"half a box", insertTable(t, pool, "b7", "o1", "a1", "B7", map[string]any{"x": 0, "y": 0}), "tables_box_check"},
		{"a box without its height", insertTable(t, pool, "b8", "o1", "a1", "B8", map[string]any{"x": 0, "y": 0, "w": 2}), "tables_box_check"},
		{"one cell wide", insertTable(t, pool, "b9", "o1", "a1", "B9", box(0, 0, 1, 2)), "tables_box_check"},
		{"seventeen cells tall", insertTable(t, pool, "b10", "o1", "a1", "B10", box(0, 0, 2, 17)), "tables_box_check"},
		{"past the right edge", insertTable(t, pool, "b11", "o1", "a1", "B11", box(47, 0, 2, 2)), "tables_box_check"},
		{"past the bottom edge", insertTable(t, pool, "b12", "o1", "a1", "B12", box(0, 31, 2, 2)), "tables_box_check"},
		{"left of the grid", insertTable(t, pool, "b13", "o1", "a1", "B13", box(-1, 0, 2, 2)), "tables_box_check"},
		{"another outlet's area", insertTable(t, pool, "b14", "o1", "a2", "B14", nil), "tables_area_fkey"},
		{"a name twice, in another case", insertTable(t, pool, "b15", "o1", "a1", "a1", nil), "tables_outlet_name_key"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

func TestTableSchedulesRules(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	tables(t, pool)
	for _, err := range []error{
		insertSchedule(t, pool, "s1", nil),
		insertSchedule(t, pool, "s2", map[string]any{"cancelled_at": time.Now(), "cancelled_by": "u1"}),
		exec(t, pool, `INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('s1', 't1', 'o1')`),
		insertTable(t, pool, "t2", "o2", "a2", "B1", nil),
	} {
		if err != nil {
			t.Fatalf("valid schedules: %v", err)
		}
	}

	now := time.Now()
	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"an empty title", insertSchedule(t, pool, "b1", map[string]any{"title": ""}), "table_schedules_title_check"},
		{"a title too long", insertSchedule(t, pool, "b2", map[string]any{"title": strings.Repeat("a", 101)}), "table_schedules_title_check"},
		{"an end at its start", insertSchedule(t, pool, "b3", map[string]any{"start_at": now, "end_at": now}), "table_schedules_period_check"},
		{"cancelled by nobody", insertSchedule(t, pool, "b4", map[string]any{"cancelled_at": now}), "table_schedules_cancelled_check"},
		{"a canceller without a time", insertSchedule(t, pool, "b5", map[string]any{"cancelled_by": "u1"}), "table_schedules_cancelled_check"},
		{"a creator who doesn't exist", insertSchedule(t, pool, "b6", map[string]any{"created_by": "nope"}), "table_schedules_created_by_fkey"},
		{
			"a table twice",
			exec(t, pool, `INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('s1', 't1', 'o1')`),
			"table_schedule_items_pkey",
		},
		{
			"another outlet's table",
			exec(t, pool, `INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('s1', 't2', 'o1')`),
			"table_schedule_items_table_fkey",
		},
		{
			"another outlet's schedule",
			exec(t, pool, `INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('s1', 't2', 'o2')`),
			"table_schedule_items_schedule_fkey",
		},
		{
			"a table that doesn't exist",
			exec(t, pool, `INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('s1', 'nope', 'o1')`),
			"table_schedule_items_table_fkey",
		},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

// A schedule's status at each side of its start and its end.
func TestTableScheduleStatus(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	start := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	for _, tt := range []struct {
		name      string
		cancelled *time.Time
		at        time.Time
		want      string
	}{
		{"before its start", nil, start.Add(-time.Microsecond), "upcoming"},
		{"at its start", nil, start, "ongoing"},
		{"just before its end", nil, end.Add(-time.Microsecond), "ongoing"},
		{"at its end", nil, end, "finished"},
		{"cancelled before its start", &start, start.Add(-time.Hour), "cancelled"},
		{"cancelled, at what would be its end", &start, end, "cancelled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			err := pool.QueryRow(context.WithoutCancel(t.Context()), `SELECT table_schedule_status($1, $2, $3, $4)`,
				tt.cancelled, start, end, tt.at).Scan(&got)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOrdersNameTheirTable(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	tables(t, pool)
	if err := insertTable(t, pool, "t2", "o2", "a2", "B1", nil); err != nil {
		t.Fatal(err)
	}
	atTable := func(extra map[string]any) map[string]any {
		row := map[string]any{"type": "dine_in", "table_id": "t1", "table_name": "A1"}
		for k, v := range extra {
			row[k] = v
		}
		return row
	}
	for _, err := range []error{
		insertOrder(t, pool, "ok1", atTable(nil)),
		// Dine-in without a table: the table is never required.
		insertOrder(t, pool, "ok2", map[string]any{"type": "dine_in"}),
	} {
		if err != nil {
			t.Fatalf("valid orders: %v", err)
		}
	}
	for _, tt := range []struct {
		name       string
		err        error
		constraint string
	}{
		{"a takeaway at a table", insertOrder(t, pool, "b1", atTable(map[string]any{"type": "takeaway"})), "orders_table_check"},
		{"a table without its name", insertOrder(t, pool, "b2", atTable(map[string]any{"table_name": nil})), "orders_table_check"},
		{"a name without its table", insertOrder(t, pool, "b3", atTable(map[string]any{"table_id": nil})), "orders_table_check"},
		{"another outlet's table", insertOrder(t, pool, "b4", atTable(map[string]any{"table_id": "t2"})), "orders_table_fkey"},
		{"a table that doesn't exist", insertOrder(t, pool, "b5", atTable(map[string]any{"table_id": "nope"})), "orders_table_fkey"},
	} {
		t.Run(tt.name, func(t *testing.T) { violates(t, tt.err, tt.constraint) })
	}
}

// An order's created_at is when its row is written, not when its
// transaction began: a sale that waited for a table's release counts after
// it.
func TestOrdersAreTimedAsWritten(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	cashier(t, pool)
	ctx := context.WithoutCancel(t.Context())
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var began, written time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&began); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_sleep(0.05)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO orders (order_id, outlet_id, device_id, order_no, type, cashier_id,
		subtotal, service_bps, service, tax_bps, tax, total, client_created_at, request_hash)
		VALUES ('late', 'o1', 'd1', 'O1-K01-20261001-9999', 'takeaway', 'u1', 0, 0, 0, 0, 0, 0, now(), $1)`,
		sha256Hex("late")); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT created_at FROM orders WHERE order_id = 'late'`).Scan(&written); err != nil {
		t.Fatal(err)
	}
	if written.Sub(began) < 50*time.Millisecond {
		t.Errorf("created_at = %s, %s after its transaction began; want the time it was written", written, written.Sub(began))
	}
}
