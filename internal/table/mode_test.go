package table

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/table/store"
)

func TestDineInMode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	path := "/api/v1/outlets/o1/dine-in-mode"
	mode := func(who string) string {
		t.Helper()
		var m dineInModeResponse
		h.do(http.MethodGet, path, who, nil).status(http.StatusOK, "").decode(&m)
		return m.Mode
	}
	set := func(m string) {
		t.Helper()
		var got dineInModeResponse
		h.do(http.MethodPut, path, "manager", map[string]any{"mode": m}).status(http.StatusOK, "").decode(&got)
		if got.Mode != m {
			t.Errorf("set %s = %s", m, got.Mode)
		}
	}

	// An outlet that never picked a mode seats its customers at tables, and
	// saving that changes and records nothing.
	if got := mode("manager"); got != "table" {
		t.Errorf("a new outlet's mode = %s, want table", got)
	}
	set("table")
	if e := h.log("o1"); len(e) != 0 {
		t.Errorf("the mode it has, saved = %+v, want nothing logged", e)
	}

	// Number stands, as the Owner and whoever has table.view read it, with
	// what it was before in the log.
	set("number")
	for _, who := range []string{"manager", "owner", "viewer"} {
		if got := mode(who); got != "number" {
			t.Errorf("%s reads %s, want number", who, got)
		}
	}
	set("number")
	set("table")
	if got := mode("viewer"); got != "table" {
		t.Errorf("back to tables, the mode = %s", got)
	}
	e := h.log("o1")
	if len(e) != 2 {
		t.Fatalf("log = %+v, want the two changes", e)
	}
	for i, want := range [][2]string{{"table", "number"}, {"number", "table"}} {
		if e[i].Action != "dine_in_mode.updated" || e[i].Before["mode"] != want[0] || e[i].After["mode"] != want[1] ||
			e[i].UserID == nil || *e[i].UserID != "m1" || e[i].OutletID == nil || *e[i].OutletID != "o1" {
			t.Errorf("log[%d] = %+v, want %s to %s by m1 in o1", i, e[i], want[0], want[1])
		}
	}

	// Each outlet has its own.
	set("number")
	var other dineInModeResponse
	h.do(http.MethodGet, "/api/v1/outlets/o2/dine-in-mode", "manager2", nil).status(http.StatusOK, "").decode(&other)
	if other.Mode != "table" {
		t.Errorf("o2's mode = %s, want table", other.Mode)
	}

	// Only the two modes.
	for _, body := range []map[string]any{{"mode": "queue"}, {"mode": "Number"}} {
		r := h.do(http.MethodPut, path, "manager", body).status(http.StatusUnprocessableEntity, "validation_failed")
		if f := r.fields(); f["mode"] != "oneof" {
			t.Errorf("%v: fields = %v", body, f)
		}
	}
	r := h.do(http.MethodPut, path, "manager", map[string]any{}).status(http.StatusUnprocessableEntity, "validation_failed")
	if f := r.fields(); f["mode"] != "required" {
		t.Errorf("no mode: fields = %v", f)
	}
	if got := mode("manager"); got != "number" {
		t.Errorf("after the refusals = %s, want number", got)
	}
}

func TestDineInModeAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	path := "/api/v1/outlets/o1/dine-in-mode"

	// The Owner and a user with table.view read it; neither changes it.
	for _, who := range []string{"owner", "viewer"} {
		h.do(http.MethodGet, path, who, nil).status(http.StatusOK, "")
		h.do(http.MethodPut, path, who, map[string]any{"mode": "number"}).status(http.StatusForbidden, "forbidden")
	}
	// A cashier reads it with the catalog, not here; staff without
	// table.view don't read it either.
	for _, who := range []string{"cashier", "staff"} {
		h.do(http.MethodGet, path, who, nil).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPut, path, who, map[string]any{"mode": "number"}).status(http.StatusForbidden, "forbidden")
	}
	// Another outlet's Manager finds no such outlet.
	h.do(http.MethodGet, path, "manager2", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodPut, path, "manager2", map[string]any{"mode": "number"}).status(http.StatusNotFound, "outlet_not_found")
}

// An outlet that seats its customers by number stands shows the cashier no
// tables to pick; its tables come back with the table mode.
func TestFloorByMode(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	h.table(indoor.AreaID, "A1")
	areas := func() int {
		t.Helper()
		var f floorResponse
		h.do(http.MethodGet, "/api/v1/outlets/o1/pos/tables", "cashier", nil).status(http.StatusOK, "").decode(&f)
		if f.Areas == nil {
			t.Error("areas = null, want a list")
		}
		return len(f.Areas)
	}
	if got := areas(); got != 1 {
		t.Fatalf("at tables = %d areas, want 1", got)
	}
	h.do(http.MethodPut, "/api/v1/outlets/o1/dine-in-mode", "manager", map[string]any{"mode": "number"}).
		status(http.StatusOK, "")
	if got := areas(); got != 0 {
		t.Errorf("by number stands = %d areas, want none", got)
	}
	h.do(http.MethodPut, "/api/v1/outlets/o1/dine-in-mode", "manager", map[string]any{"mode": "table"}).
		status(http.StatusOK, "")
	if got := areas(); got != 1 {
		t.Errorf("back at tables = %d areas, want 1", got)
	}
}

// Two changes of the mode at once: the second waits for the first, finds
// the mode it set, and records nothing.
func TestModeChangesAtOnceLogOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	if _, err := h.svc.SetDineInMode(ctx, principals["manager"], "", "o1", ModeNumber); err != nil {
		t.Fatal(err)
	}
	// Something holds the outlet's mode meanwhile, so both changes are on
	// their way at once when it lets go.
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hold.Rollback(ctx) })
	if _, err := hold.Exec(ctx, `SELECT 1 FROM dine_in_settings WHERE outlet_id = 'o1' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := h.svc.SetDineInMode(ctx, principals["manager"], "", "o1", ModeTable)
			done <- err
		}()
	}
	h.waitForLock(2, `SELECT count(*) FROM pg_stat_activity WHERE cardinality(pg_blocking_pids(pid)) > 0`)
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if e := h.log("o1"); len(e) != 2 {
		t.Errorf("log = %+v, want the change to number stands and one back", e)
	}
}

// An outlet's first change of mode, before it has a setting saved to lock,
// is one at a time too.
func TestFirstModeChangesAtOnceLogOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	// Something writes the outlet's first setting meanwhile, so both
	// changes are on their way at once when it lets go.
	hold, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hold.Rollback(ctx) })
	if _, err := hold.Exec(ctx, `INSERT INTO dine_in_settings (outlet_id, mode, updated_by) VALUES ('o1', 'table', $1)`,
		principals["manager"].UserID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := h.svc.SetDineInMode(ctx, principals["manager"], "", "o1", ModeNumber)
			done <- err
		}()
	}
	h.waitForLock(2, `SELECT count(*) FROM pg_stat_activity WHERE cardinality(pg_blocking_pids(pid)) > 0`)
	if err := hold.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if e := h.log("o1"); len(e) != 1 {
		t.Errorf("log = %+v, want the one change to number stands", e)
	}
}

// The mode's lock is its schema's, as the rows it guards are: an outlet of
// the same ID held in another schema keeps no change here waiting.
func TestModeLockIsTheSchemas(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	other := newHarness(t)
	ctx := context.WithoutCancel(t.Context())
	hold, err := other.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hold.Rollback(ctx) })
	if err := store.New(hold).LockDineInMode(ctx, "o1"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := h.svc.SetDineInMode(ctx, principals["manager"], "", "o1", ModeNumber)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a change waited for another schema's lock")
	}
}
