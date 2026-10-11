package table

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// scheduleBody is a schedule as a Manager fills it in; no table is an
// empty list.
func scheduleBody(title string, start, end time.Time, tableIDs ...string) map[string]any {
	if tableIDs == nil {
		tableIDs = []string{}
	}
	return map[string]any{"title": title, "start_at": start, "end_at": end, "table_ids": tableIDs}
}

// schedule has o1's Manager create a schedule, and returns it.
func (h *harness) schedule(body map[string]any) scheduleResponse {
	h.t.Helper()
	var s scheduleResponse
	h.do(http.MethodPost, "/api/v1/outlets/o1/table-schedules", "manager", body).status(http.StatusCreated, "").decode(&s)
	return s
}

// getSchedule returns o1's schedule as its Manager sees it.
func (h *harness) getSchedule(scheduleID string) scheduleResponse {
	h.t.Helper()
	var s scheduleResponse
	h.do(http.MethodGet, "/api/v1/outlets/o1/table-schedules/"+scheduleID, "manager", nil).
		status(http.StatusOK, "").decode(&s)
	return s
}

// schedules lists o1's schedules, with the query.
func (h *harness) schedules(query string) httpx.List[scheduleResponse] {
	h.t.Helper()
	var l httpx.List[scheduleResponse]
	h.do(http.MethodGet, "/api/v1/outlets/o1/table-schedules"+query, "manager", nil).status(http.StatusOK, "").decode(&l)
	return l
}

func scheduleTables(s scheduleResponse) []string {
	out := make([]string, len(s.Tables))
	for i, t := range s.Tables {
		out[i] = t.Name
	}
	return out
}

func titles(ss []scheduleResponse) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Title
	}
	return out
}

func TestSchedules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor, terrace := h.area("Indoor"), h.area("Teras")
	a1, a2, t1 := h.table(indoor.AreaID, "A1"), h.table(indoor.AreaID, "A2"), h.table(terrace.AreaID, "T1")
	// Every time first: h.at counts from the clock, which moves below.
	two, threeish, four, five, past := h.at(0, "14:00"), h.at(0, "15:30"), h.at(0, "16:00"), h.at(0, "17:00"), h.at(0, "13:00")

	event := h.schedule(scheduleBody("  Acara kantor ", two, four, t1.TableID, a2.TableID, a1.TableID, a1.TableID))
	if event.Title != "Acara kantor" || event.Status != ScheduleUpcoming || event.CreatedBy.Name != "Rina" ||
		!event.StartAt.Equal(two) || !event.EndAt.Equal(four) || event.CancelledAt != nil {
		t.Fatalf("a new schedule = %+v", event)
	}
	// By area, then by name, each once.
	if got, want := scheduleTables(event), []string{"A1", "A2", "T1"}; !slices.Equal(got, want) {
		t.Errorf("tables = %v, want %v", got, want)
	}
	if got := h.get(a1.TableID).Status; got != string(Available) {
		t.Errorf("before the schedule, A1 = %s", got)
	}

	// Under way: its tables are out of service.
	h.clock = two
	if got := h.get(a1.TableID).Status; got != string(Scheduled) {
		t.Errorf("under way, A1 = %s", got)
	}
	if got := h.getSchedule(event.ScheduleID).Status; got != ScheduleOngoing {
		t.Errorf("under way, the schedule = %s", got)
	}

	// Changed under way: its start stays as it is, though past; its end
	// moves later, and the terrace comes off it.
	path := "/api/v1/outlets/o1/table-schedules/" + event.ScheduleID
	h.clock = threeish
	var changed scheduleResponse
	h.do(http.MethodPut, path, "manager", scheduleBody("Acara kantor", two, five, a1.TableID, a2.TableID)).
		status(http.StatusOK, "").decode(&changed)
	if !changed.EndAt.Equal(five) || !slices.Equal(scheduleTables(changed), []string{"A1", "A2"}) {
		t.Errorf("changed = %+v", changed)
	}
	if got := h.get(t1.TableID).Status; got != string(Available) {
		t.Errorf("T1, off the schedule = %s", got)
	}
	// Changed to what it is: nothing recorded.
	h.do(http.MethodPut, path, "manager", scheduleBody("Acara kantor ", two, five, a2.TableID, a1.TableID)).
		status(http.StatusOK, "")
	// A start moved into the past is refused.
	r := h.do(http.MethodPut, path, "manager", scheduleBody("Acara kantor", past, five, a1.TableID))
	r.status(http.StatusUnprocessableEntity, "validation_failed")
	if r.fields()["start_at"] != "future" {
		t.Errorf("a start moved into the past: %v", r.fields())
	}

	// Cancelled: its tables are back in service, and it stays as a record.
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusNoContent, "")
	cancelled := h.getSchedule(event.ScheduleID)
	if cancelled.Status != ScheduleCancelled || cancelled.CancelledAt == nil || cancelled.CancelledBy == nil ||
		cancelled.CancelledBy.Name != "Rina" {
		t.Errorf("cancelled = %+v", cancelled)
	}
	if got := h.get(a1.TableID).Status; got != string(Available) {
		t.Errorf("after the cancel, A1 = %s", got)
	}
	h.do(http.MethodDelete, path, "manager", nil).status(http.StatusConflict, "schedule_closed")
	h.do(http.MethodPut, path, "manager", scheduleBody("X", four, five, a1.TableID)).status(http.StatusConflict, "schedule_closed")

	entries := h.log(event.ScheduleID)
	if want := []string{"table_schedule.created", "table_schedule.updated", "table_schedule.cancelled"}; !slices.Equal(actions(entries), want) {
		t.Fatalf("log = %v, want %v", actions(entries), want)
	}
	if after, _ := entries[0].After["tables"].([]any); len(after) != 3 || after[0] != "A1" || entries[0].After["title"] != "Acara kantor" {
		t.Errorf("created logged as %+v", entries[0])
	}
	if u := entries[1]; u.After["end_at"] == nil || u.After["title"] != nil || u.After["start_at"] != nil {
		t.Errorf("updated logged as %+v", u)
	}

	// Over: closed too, and no longer under way.
	renovation := h.schedule(scheduleBody("Renovasi", threeish, four, a1.TableID))
	h.clock = four
	if got := h.getSchedule(renovation.ScheduleID).Status; got != ScheduleFinished {
		t.Errorf("past its end = %s", got)
	}
	if got := h.get(a1.TableID).Status; got != string(Available) {
		t.Errorf("after the schedule, A1 = %s", got)
	}
	renovationPath := "/api/v1/outlets/o1/table-schedules/" + renovation.ScheduleID
	h.do(http.MethodDelete, renovationPath, "manager", nil).status(http.StatusConflict, "schedule_closed")
	h.do(http.MethodPut, renovationPath, "manager", scheduleBody("Renovasi", four, five, a1.TableID)).
		status(http.StatusConflict, "schedule_closed")
}

func TestScheduleRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1 := h.table(indoor.AreaID, "A1").TableID
	gone := h.table(indoor.AreaID, "Lama").TableID
	h.do(http.MethodDelete, "/api/v1/outlets/o1/tables/"+gone, "manager", nil).status(http.StatusNoContent, "")
	var area areaResponse
	var theirs tableResponse
	h.do(http.MethodPost, "/api/v1/outlets/o2/areas", "manager2", map[string]any{"name": "Indoor"}).
		status(http.StatusCreated, "").decode(&area)
	h.do(http.MethodPost, "/api/v1/outlets/o2/tables", "manager2", map[string]any{"name": "B1", "area_id": area.AreaID}).
		status(http.StatusCreated, "").decode(&theirs)
	noon, one, two, ten, eleven := h.at(0, "12:00"), h.at(0, "13:00"), h.at(0, "14:00"), h.at(0, "10:00"), h.at(0, "11:00")
	tooMany := make([]string, MaxScheduleTables+1)
	for i := range tooMany {
		tooMany[i] = strings.Repeat("x", 3) + string(rune('a'+i%26)) + string(rune('a'+i/26%26))
	}

	for _, tt := range []struct {
		name string
		body map[string]any
		want map[string]string
	}{
		{"no title", scheduleBody("   ", one, two, a1), map[string]string{"title": "required"}},
		{"a title too long", scheduleBody(strings.Repeat("a", 101), one, two, a1), map[string]string{"title": "max"}},
		{"an end before the start", scheduleBody("X", two, one, a1), map[string]string{"end_at": "after"}},
		{"an end at the start", scheduleBody("X", one, one, a1), map[string]string{"end_at": "after"}},
		{"a start in the past", scheduleBody("X", eleven, two, a1), map[string]string{"start_at": "future"}},
		{"over before it is made", scheduleBody("X", ten, eleven, a1), map[string]string{"start_at": "future", "end_at": "future"}},
		{"no table", scheduleBody("X", one, two), map[string]string{"table_ids": "required"}},
		{"a table that isn't there", scheduleBody("X", one, two, a1, "nope"), map[string]string{"table_ids": "exists"}},
		{"a deleted table", scheduleBody("X", one, two, gone), map[string]string{"table_ids": "exists"}},
		{"another outlet's table", scheduleBody("X", one, two, theirs.TableID), map[string]string{"table_ids": "exists"}},
		{"too many tables", scheduleBody("X", one, two, tooMany...), map[string]string{"table_ids": "max"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := h.do(http.MethodPost, "/api/v1/outlets/o1/table-schedules", "manager", tt.body)
			r.status(http.StatusUnprocessableEntity, "validation_failed")
			if got := r.fields(); len(got) != len(tt.want) {
				t.Errorf("fields = %v, want %v", got, tt.want)
			}
			for field, rule := range tt.want {
				if r.fields()[field] != rule {
					t.Errorf("fields = %v, want %s: %s", r.fields(), field, rule)
				}
			}
		})
	}
	r := h.do(http.MethodPost, "/api/v1/outlets/o1/table-schedules", "manager", map[string]any{"title": "X", "table_ids": []string{a1}})
	r.status(http.StatusUnprocessableEntity, "validation_failed")
	if f := r.fields(); f["start_at"] != "required" || f["end_at"] != "required" {
		t.Errorf("times left out: %v", f)
	}
	r = h.do(http.MethodPost, "/api/v1/outlets/o1/table-schedules", "manager", map[string]any{
		"title": "X", "start_at": one, "end_at": two,
	})
	if r.fields()["table_ids"] != "required" {
		t.Errorf("tables left out: %s", r.Body)
	}

	// The current minute isn't the past: a start a few seconds ago is now.
	h.clock = noon.Add(40 * time.Second)
	h.schedule(scheduleBody("Sekarang", noon.Add(10*time.Second), two, a1))
	var count int
	h.scalar(&count, `SELECT count(*) FROM table_schedules`)
	if count != 1 {
		t.Errorf("schedules = %d, want only the valid one", count)
	}
}

func TestScheduleOverlaps(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1, a2, a3 := h.table(indoor.AreaID, "A1").TableID, h.table(indoor.AreaID, "A2").TableID, h.table(indoor.AreaID, "A3").TableID
	a4 := h.table(indoor.AreaID, "A4").TableID
	one, half, two, three := h.at(0, "13:00"), h.at(0, "14:30"), h.at(0, "14:00"), h.at(0, "15:00")
	four, five := h.at(0, "16:00"), h.at(0, "17:00")
	first := h.schedule(scheduleBody("Acara", two, four, a1, a2))

	// Another with A2 out from three to five: a warning, naming the first
	// and the table they share.
	clash := scheduleBody("Rapat", three, five, a2, a3)
	r := h.do(http.MethodPost, "/api/v1/outlets/o1/table-schedules", "manager", clash)
	r.status(http.StatusConflict, "schedule_overlaps")
	overlaps, _ := r.envelope().Error.Details["schedules"].([]any)
	if len(overlaps) != 1 {
		t.Fatalf("overlaps = %v", r.envelope().Error.Details)
	}
	o := overlaps[0].(map[string]any)
	if o["schedule_id"] != first.ScheduleID || o["title"] != "Acara" || o["start_at"] != two.UTC().Format(time.RFC3339) ||
		!slices.Equal(anyStrings(o["tables"]), []string{"A2"}) {
		t.Errorf("overlap = %v", o)
	}
	// Confirmed, it is saved.
	clash["confirm_overlap"] = true
	second := h.schedule(clash)

	// One ending as another starts doesn't overlap it.
	h.schedule(scheduleBody("Sore", four, five, a1))
	// Changing a schedule doesn't warn of itself; a cancelled one warns of
	// nothing.
	h.do(http.MethodDelete, "/api/v1/outlets/o1/table-schedules/"+first.ScheduleID, "manager", nil).status(http.StatusNoContent, "")
	h.do(http.MethodPut, "/api/v1/outlets/o1/table-schedules/"+second.ScheduleID, "manager", scheduleBody("Rapat", two, five, a2, a3)).
		status(http.StatusOK, "")
	// A finished one warns of nothing either: a schedule under way, changed,
	// may cross one that ended since it began.
	h.schedule(scheduleBody("Pagi", one, half, a4))
	h.clock = three
	h.do(http.MethodPut, "/api/v1/outlets/o1/table-schedules/"+second.ScheduleID, "manager",
		scheduleBody("Rapat", two, five, a2, a3, a4)).status(http.StatusOK, "")
}

func anyStrings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, len(items))
	for i, item := range items {
		out[i], _ = item.(string)
	}
	return out
}

func TestSchedulesList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1 := h.table(indoor.AreaID, "A1").TableID
	one, two, three, four, five := h.at(0, "13:00"), h.at(0, "14:00"), h.at(0, "15:00"), h.at(0, "16:00"), h.at(0, "17:00")
	h.schedule(scheduleBody("Siang", one, two, a1))
	h.schedule(scheduleBody("Sore", three, four, a1))
	cancelled := h.schedule(scheduleBody("Batal", four, five, a1))
	h.do(http.MethodDelete, "/api/v1/outlets/o1/table-schedules/"+cancelled.ScheduleID, "manager", nil).status(http.StatusNoContent, "")
	h.clock = three

	if got := h.schedules(""); !slices.Equal(titles(got.Data), []string{"Batal", "Sore", "Siang"}) || got.Meta.Total != 3 {
		t.Errorf("schedules = %v (%d), want the latest first", titles(got.Data), got.Meta.Total)
	}
	for _, tt := range []struct {
		query string
		want  []string
	}{
		{"?status=ongoing", []string{"Sore"}},
		{"?status=finished", []string{"Siang"}},
		{"?status=cancelled", []string{"Batal"}},
		{"?status=upcoming", []string{}},
		{"?page=2&page_size=2", []string{"Siang"}},
	} {
		if got := h.schedules(tt.query); !slices.Equal(titles(got.Data), tt.want) {
			t.Errorf("%s = %v, want %v", tt.query, titles(got.Data), tt.want)
		}
	}
	h.do(http.MethodGet, "/api/v1/outlets/o1/table-schedules?status=later", "manager", nil).
		status(http.StatusUnprocessableEntity, "validation_failed")
	if got := h.schedules("?status=ongoing"); len(got.Data) == 1 && !slices.Equal(scheduleTables(got.Data[0]), []string{"A1"}) {
		t.Errorf("a listed schedule's tables = %v", scheduleTables(got.Data[0]))
	}
}

func TestSchedulesAccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	indoor := h.area("Indoor")
	a1 := h.table(indoor.AreaID, "A1").TableID
	one, two := h.at(0, "13:00"), h.at(0, "14:00")
	s := h.schedule(scheduleBody("Acara", one, two, a1))
	path := "/api/v1/outlets/o1/table-schedules/" + s.ScheduleID

	for _, who := range []string{"owner", "viewer"} {
		h.do(http.MethodGet, "/api/v1/outlets/o1/table-schedules", who, nil).status(http.StatusOK, "")
		h.do(http.MethodGet, path, who, nil).status(http.StatusOK, "")
		h.do(http.MethodPost, "/api/v1/outlets/o1/table-schedules", who, scheduleBody("X", one, two, a1)).
			status(http.StatusForbidden, "forbidden")
		h.do(http.MethodPut, path, who, scheduleBody("X", one, two, a1)).status(http.StatusForbidden, "forbidden")
		h.do(http.MethodDelete, path, who, nil).status(http.StatusForbidden, "forbidden")
	}
	for _, who := range []string{"cashier", "staff"} {
		h.do(http.MethodGet, "/api/v1/outlets/o1/table-schedules", who, nil).status(http.StatusForbidden, "forbidden")
	}
	h.do(http.MethodGet, path, "manager2", nil).status(http.StatusNotFound, "outlet_not_found")
	h.do(http.MethodGet, "/api/v1/outlets/o2/table-schedules/"+s.ScheduleID, "manager2", nil).
		status(http.StatusNotFound, "schedule_not_found")
	h.do(http.MethodDelete, "/api/v1/outlets/o2/table-schedules/"+s.ScheduleID, "manager2", nil).
		status(http.StatusNotFound, "schedule_not_found")
	if got := h.getSchedule(s.ScheduleID).Status; got != ScheduleUpcoming {
		t.Errorf("after another outlet's cancel = %s", got)
	}
}
