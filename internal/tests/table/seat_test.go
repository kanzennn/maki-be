package table

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// floor returns o1's floor as its cashier sees it.
func (h *harness) floor() models.FloorResponse {
	h.t.Helper()
	var f models.FloorResponse
	h.do(http.MethodGet, "/api/v1/outlets/o1/pos/tables", "cashier", nil).status(http.StatusOK, "").decode(&f)
	return f
}

func TestFloor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor, terrace, empty := h.area("Indoor"), h.area("Teras"), h.area("Kosong")
	a1, a2 := h.table(indoor.AreaID, "A1"), h.table(indoor.AreaID, "A2")
	h.table(terrace.AreaID, "T1", "is_active", false)
	h.saveLayout(indoor.AreaID, h.layout(indoor.AreaID).Version, []map[string]any{placing(a1.TableID, 2, 2, 3, 3)},
		[]map[string]any{deco(models.KindDoor, 0, 31, 3, 1)}).status(http.StatusOK, "")
	h.saveLayout(empty.AreaID, h.layout(empty.AreaID).Version, nil, []map[string]any{deco(models.KindWall, 0, 0, 10, 1)}).
		status(http.StatusOK, "")
	h.orders.sell("o1", a2.TableID, h.at(0, "11:30"))

	// The areas that hold tables, in order, with their decorations; an
	// area without one is nothing to pick from.
	f := h.floor()
	if len(f.Areas) != 2 || f.Areas[0].Name != "Indoor" || f.Areas[1].Name != "Teras" {
		t.Fatalf("areas = %+v", f.Areas)
	}
	in := f.Areas[0]
	if len(in.Decorations) != 1 || in.Decorations[0].Kind != models.KindDoor || len(in.Tables) != 2 {
		t.Fatalf("Indoor = %+v", in)
	}
	if got := in.Tables[0]; got.Name != "A1" || got.Box == nil || *got.Box != (models.BoxResponse{X: 2, Y: 2, W: 3, H: 3}) ||
		got.Status != string(models.TableAvailable) {
		t.Errorf("A1 = %+v", got)
	}
	if got := in.Tables[1]; got.Name != "A2" || got.Box != nil || got.Status != string(models.TableOccupied) || got.Seated == nil ||
		got.Seated.Orders != 1 {
		t.Errorf("A2 = %+v", got)
	}
	if got := f.Areas[1].Tables[0]; got.Status != string(models.TableInactive) {
		t.Errorf("T1 = %+v", got)
	}

	// The cashier and the Manager read it; whoever doesn't sell doesn't.
	h.do(http.MethodGet, "/api/v1/outlets/o1/pos/tables", "manager", nil).status(http.StatusOK, "")
	for _, who := range []string{"owner", "viewer", "staff"} {
		h.do(http.MethodGet, "/api/v1/outlets/o1/pos/tables", who, nil).status(http.StatusForbidden, "forbidden")
	}
	var none models.FloorResponse
	h.do(http.MethodGet, "/api/v1/outlets/o2/pos/tables", "manager2", nil).status(http.StatusOK, "").decode(&none)
	if none.Areas == nil || len(none.Areas) != 0 {
		t.Errorf("an outlet without tables = %+v, want no areas, as a list", none)
	}
}

// hold holds the table for a sale in o1, as checkout does, and returns the
// seat or the error.
func (h *harness) hold(tableID string) (models.Seat, error) {
	h.t.Helper()
	var seat models.Seat
	err := h.db.InTx(h.t.Context(), func(ctx context.Context) error {
		var err error
		seat, err = h.tables.HoldForSale(ctx, "o1", tableID)
		return err
	})
	return seat, err
}

func TestHoldForSale(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	free, seated := h.table(indoor.AreaID, "A1"), h.table(indoor.AreaID, "A2")
	off, offSeated := h.table(indoor.AreaID, "A3", "is_active", false), h.table(indoor.AreaID, "A4", "is_active", false)
	scheduled, gone := h.table(indoor.AreaID, "A5"), h.table(indoor.AreaID, "A6")
	h.orders.sell("o1", seated.TableID, h.at(0, "11:00"))
	h.orders.sell("o1", offSeated.TableID, h.at(0, "11:00"))
	h.exec(`INSERT INTO table_schedules (schedule_id, outlet_id, title, start_at, end_at, created_by)
		VALUES ('s1', 'o1', 'Acara', $1, $2, 'm1')`, h.at(0, "11:00"), h.at(0, "13:00"))
	h.exec(`INSERT INTO table_schedule_items (schedule_id, table_id, outlet_id) VALUES ('s1', $1, 'o1')`, scheduled.TableID)
	h.do(http.MethodDelete, "/api/v1/outlets/o1/tables/"+gone.TableID, "manager", nil).status(http.StatusNoContent, "")
	var area models.AreaResponse
	var theirs models.TableResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/areas", "manager2", map[string]any{"name": "Indoor"}).
		status(http.StatusCreated, "").decode(&area)
	h.do(http.MethodPost, "/api/v1/outlets/o2/tables", "manager2", map[string]any{"name": "B1", "area_id": area.AreaID}).
		status(http.StatusCreated, "").decode(&theirs)

	// Free and occupied tables take the sale; one seated at, turned off
	// since, too.
	for _, tt := range []models.TableResponse{free, seated, offSeated} {
		seat, err := h.hold(tt.TableID)
		if err != nil || seat != (models.Seat{TableID: tt.TableID, Name: tt.Name}) {
			t.Errorf("%s: seat = %+v, %v", tt.Name, seat, err)
		}
	}
	for _, tt := range []struct {
		name    string
		tableID string
		status  string
	}{
		{"turned off", off.TableID, string(models.TableInactive)},
		{"scheduled off", scheduled.TableID, string(models.TableScheduled)},
		{"deleted", gone.TableID, "missing"},
		{"another outlet's", theirs.TableID, "missing"},
		{"not there", "nope", "missing"},
	} {
		_, err := h.hold(tt.tableID)
		e, ok := errors.AsType[*apperror.Error](err)
		if !ok || e.Code() != "table_unavailable" || e.Details()["status"] != tt.status {
			t.Errorf("%s: error = %v %v, want table_unavailable, %s", tt.name, err, e.Details(), tt.status)
		}
	}
}

func TestRelease(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1, a2 := h.table(indoor.AreaID, "A1"), h.table(indoor.AreaID, "A2")
	h.orders.sell("o1", a1.TableID, h.at(0, "11:00"))
	h.orders.sell("o1", a1.TableID, h.at(0, "11:30"))
	path := "/api/v1/outlets/o1/tables/" + a1.TableID + "/release"

	var released models.TableResponse
	h.do(http.MethodPost, path, "cashier", nil).status(http.StatusOK, "").decode(&released)
	if released.Status != string(models.TableAvailable) || released.Seated != nil {
		t.Errorf("released = %+v", released)
	}
	entries := h.log(a1.TableID)
	e := entries[len(entries)-1]
	if e.Action != "table.released" || e.Before["orders"] != float64(2) || e.Before["since"] != h.at(0, "11:00").UTC().Format(time.RFC3339) ||
		*e.UserID != "k1" {
		t.Errorf("release logged as %+v", e)
	}
	// Released again, or a table nobody sat at: nothing changes, nothing
	// is recorded.
	h.do(http.MethodPost, path, "cashier", nil).status(http.StatusOK, "")
	h.do(http.MethodPost, "/api/v1/outlets/o1/tables/"+a2.TableID+"/release", "cashier", nil).status(http.StatusOK, "")
	if got := actions(h.log(a1.TableID)); slices.Index(got, "table.released") != len(got)-1 || len(h.log(a2.TableID)) != 1 {
		t.Errorf("logs = %v and %v", got, actions(h.log(a2.TableID)))
	}
	// A sale after the release seats customers again. The release is timed
	// by the database's clock, as a sale is: this one is sold now.
	h.orders.sell("o1", a1.TableID, time.Now().Add(time.Minute))
	if got := h.get(a1.TableID).Status; got != string(models.TableOccupied) {
		t.Errorf("sold at after the release = %s", got)
	}

	h.do(http.MethodPost, "/api/v1/outlets/o1/tables/nope/release", "cashier", nil).status(http.StatusNotFound, "table_not_found")
	h.do(http.MethodPost, "/api/v1/outlets/o2/tables/"+a1.TableID+"/release", "manager2", nil).
		status(http.StatusNotFound, "table_not_found")
	h.do(http.MethodPost, path, "manager", nil).status(http.StatusOK, "")
	for _, who := range []string{"owner", "viewer", "staff"} {
		h.do(http.MethodPost, path, who, nil).status(http.StatusForbidden, "forbidden")
	}
}
