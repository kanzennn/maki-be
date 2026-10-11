package table

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/table/store"
)

// A schedule's statuses, as table_schedule_status computes them at a
// moment.
const (
	ScheduleUpcoming  = "upcoming"
	ScheduleOngoing   = "ongoing"
	ScheduleFinished  = "finished"
	ScheduleCancelled = "cancelled"
)

// Limits of a schedule.
const (
	MaxScheduleTitle = 100
	// MaxScheduleTables is how many tables one schedule takes out of
	// service.
	MaxScheduleTables = 500
)

// Schedule is a time some of the outlet's tables are out of service: an
// event, a renovation, the terrace in the rainy season.
type Schedule struct {
	ScheduleID string
	Title      string
	StartAt    time.Time
	EndAt      time.Time
	// Status is the schedule's at the moment it was read.
	Status      string
	Tables      []ScheduleTable
	CreatedBy   UserRef
	CancelledAt *time.Time
	CancelledBy *UserRef
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ScheduleTable is a table a schedule takes out of service, deleted since
// or not.
type ScheduleTable struct {
	TableID  string
	Name     string
	AreaID   string
	AreaName string
	Deleted  bool

	areaOrder int
}

// UserRef names a user.
type UserRef struct {
	UserID string
	Name   string
}

// ScheduleInput is a schedule as a Manager fills it in. ConfirmOverlap
// saves it though another schedule has some of its tables out of service
// at the same time.
type ScheduleInput struct {
	Title          string
	StartAt        *time.Time
	EndAt          *time.Time
	TableIDs       []string
	ConfirmOverlap bool
}

// ScheduleFilter narrows the schedule list; an empty status doesn't.
type ScheduleFilter struct {
	Status string
}

// ListSchedules returns a page of the outlet's schedules, the latest first,
// and how many match in all.
func (s *Service) ListSchedules(ctx context.Context, outletID string, f ScheduleFilter, page httpx.Page) ([]Schedule, int, error) {
	var status *string
	if f.Status != "" {
		status = &f.Status
	}
	now := s.now()
	q := store.New(s.pool)
	total, err := q.CountSchedules(ctx, store.CountSchedulesParams{OutletID: outletID, Status: status, At: now})
	if err != nil {
		return nil, 0, fmt.Errorf("count schedules: %w", err)
	}
	rows, err := q.ListSchedules(ctx, store.ListSchedulesParams{
		OutletID: outletID, Status: status, At: now,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list schedules: %w", err)
	}
	out := make([]Schedule, len(rows))
	for i, r := range rows {
		out[i] = schedule(store.GetScheduleRow(r))
	}
	if err := s.fill(ctx, q, out); err != nil {
		return nil, 0, err
	}
	return out, int(total), nil
}

// Schedule returns one of the outlet's schedules.
func (s *Service) Schedule(ctx context.Context, outletID, scheduleID string) (Schedule, error) {
	q := store.New(s.pool)
	r, err := q.GetSchedule(ctx, store.GetScheduleParams{ScheduleID: scheduleID, OutletID: outletID, At: s.now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, ErrScheduleNotFound
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("load schedule %s: %w", scheduleID, err)
	}
	out := []Schedule{schedule(r)}
	if err := s.fill(ctx, q, out); err != nil {
		return Schedule{}, err
	}
	return out[0], nil
}

func schedule(r store.GetScheduleRow) Schedule {
	out := Schedule{
		ScheduleID: r.ScheduleID, Title: r.Title, StartAt: r.StartAt, EndAt: r.EndAt, Status: r.Status,
		CreatedBy: UserRef{UserID: r.CreatedBy}, CancelledAt: r.CancelledAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.CancelledBy != nil {
		out.CancelledBy = &UserRef{UserID: *r.CancelledBy}
	}
	return out
}

// fill gives the schedules their tables, by area and name, and their
// users' names.
func (s *Service) fill(ctx context.Context, q *store.Queries, schedules []Schedule) error {
	if len(schedules) == 0 {
		return nil
	}
	ids := make([]string, len(schedules))
	var userIDs []string
	for i, sc := range schedules {
		ids[i] = sc.ScheduleID
		userIDs = append(userIDs, sc.CreatedBy.UserID)
		if sc.CancelledBy != nil {
			userIDs = append(userIDs, sc.CancelledBy.UserID)
		}
	}
	rows, err := q.ListScheduleTables(ctx, ids)
	if err != nil {
		return fmt.Errorf("list the tables of schedules: %w", err)
	}
	tables := map[string][]ScheduleTable{}
	for _, r := range rows {
		tables[r.ScheduleID] = append(tables[r.ScheduleID], ScheduleTable{
			TableID: r.TableID, Name: r.Name, AreaID: r.AreaID, AreaName: r.AreaName, Deleted: r.Deleted,
			areaOrder: int(r.AreaSortOrder),
		})
	}
	names, err := s.users.Names(ctx, slices.Compact(slices.Sorted(slices.Values(userIDs))))
	if err != nil {
		return err
	}
	for i := range schedules {
		sc := &schedules[i]
		sc.Tables = tables[sc.ScheduleID]
		slices.SortFunc(sc.Tables, func(a, b ScheduleTable) int {
			return cmp.Or(cmp.Compare(a.areaOrder, b.areaOrder), strings.Compare(a.AreaID, b.AreaID),
				natural(a.Name, b.Name), strings.Compare(a.TableID, b.TableID))
		})
		sc.CreatedBy.Name = names[sc.CreatedBy.UserID]
		if sc.CancelledBy != nil {
			sc.CancelledBy.Name = names[sc.CancelledBy.UserID]
		}
	}
	return nil
}

// CreateSchedule takes some of the outlet's tables out of service for a
// time. One that overlaps another schedule at any of its tables is
// ErrScheduleOverlaps, naming them, unless in.ConfirmOverlap: a warning,
// not a refusal.
func (s *Service) CreateSchedule(ctx context.Context, p authn.Principal, ip, outletID string, in ScheduleInput) (Schedule, error) {
	next, err := checkSchedule(in, nil, s.now())
	if err != nil {
		return Schedule{}, err
	}
	scheduleID := id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		names, err := s.scheduleTables(ctx, q, outletID, "", next, in.ConfirmOverlap)
		if err != nil {
			return err
		}
		if err := q.InsertSchedule(ctx, store.InsertScheduleParams{
			ScheduleID: scheduleID, OutletID: outletID, Title: next.title, StartAt: next.start, EndAt: next.end,
			CreatedBy: p.UserID,
		}); err != nil {
			return fmt.Errorf("insert schedule: %w", err)
		}
		if err := q.InsertScheduleItems(ctx, store.InsertScheduleItemsParams{
			ScheduleID: scheduleID, OutletID: outletID, TableIds: next.tableIDs,
		}); err != nil {
			return fmt.Errorf("insert the tables of schedule %s: %w", scheduleID, err)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "table_schedule.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "table_schedule", TargetID: scheduleID, After: next.snapshot(names), IP: ip,
		})
	})
	if err != nil {
		return Schedule{}, err
	}
	return s.Schedule(ctx, outletID, scheduleID)
}

// UpdateSchedule changes a schedule that isn't over (ErrScheduleClosed): a
// start or an end that changes can't be in the past, so one under way
// keeps the start it has. Overlaps are as CreateSchedule's. A change of
// nothing records nothing.
func (s *Service) UpdateSchedule(ctx context.Context, p authn.Principal, ip, outletID, scheduleID string, in ScheduleInput) (Schedule, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := s.now()
		prev, err := lockSchedule(ctx, q, outletID, scheduleID, now)
		if err != nil {
			return err
		}
		next, err := checkSchedule(in, &prev, now)
		if err != nil {
			return err
		}
		names, err := s.scheduleTables(ctx, q, outletID, scheduleID, next, in.ConfirmOverlap)
		if err != nil {
			return err
		}
		prevNames, err := q.ListScheduleTables(ctx, []string{scheduleID})
		if err != nil {
			return fmt.Errorf("list the tables of schedule %s: %w", scheduleID, err)
		}
		was := make([]string, len(prevNames))
		for i, r := range prevNames {
			was[i] = r.Name
		}
		before, after := changes(prev.snapshot(was), next.snapshot(names))
		if len(after) == 0 {
			return nil
		}
		if err := q.UpdateSchedule(ctx, store.UpdateScheduleParams{
			ScheduleID: scheduleID, Title: next.title, StartAt: next.start, EndAt: next.end,
		}); err != nil {
			return fmt.Errorf("update schedule %s: %w", scheduleID, err)
		}
		if err := q.DeleteScheduleItems(ctx, store.DeleteScheduleItemsParams{ScheduleID: scheduleID, TableIds: next.tableIDs}); err != nil {
			return fmt.Errorf("take tables off schedule %s: %w", scheduleID, err)
		}
		if err := q.InsertScheduleItems(ctx, store.InsertScheduleItemsParams{
			ScheduleID: scheduleID, OutletID: outletID, TableIds: next.tableIDs,
		}); err != nil {
			return fmt.Errorf("put tables on schedule %s: %w", scheduleID, err)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "table_schedule.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "table_schedule", TargetID: scheduleID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return Schedule{}, err
	}
	return s.Schedule(ctx, outletID, scheduleID)
}

// CancelSchedule ends a schedule that isn't over (ErrScheduleClosed): its
// tables are back in service, and it stays as a record.
func (s *Service) CancelSchedule(ctx context.Context, p authn.Principal, ip, outletID, scheduleID string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := lockSchedule(ctx, q, outletID, scheduleID, s.now()); err != nil {
			return err
		}
		if err := q.CancelSchedule(ctx, store.CancelScheduleParams{ScheduleID: scheduleID, CancelledBy: &p.UserID}); err != nil {
			return fmt.Errorf("cancel schedule %s: %w", scheduleID, err)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "table_schedule.cancelled", UserID: p.UserID, OutletID: outletID,
			TargetType: "table_schedule", TargetID: scheduleID, IP: ip,
		})
	})
}

// scheduleTables checks a schedule's tables: the outlet's, not deleted
// (a field error otherwise), and, unless confirmed, at no time another
// schedule but scheduleID has any of them out. It returns their names.
func (s *Service) scheduleTables(ctx context.Context, q *store.Queries, outletID, scheduleID string, next scheduleFields,
	confirmed bool,
) ([]string, error) {
	live, err := q.ListLiveTables(ctx, store.ListLiveTablesParams{OutletID: outletID, TableIds: next.tableIDs})
	if err != nil {
		return nil, fmt.Errorf("find the tables of a schedule: %w", err)
	}
	if len(live) != len(next.tableIDs) {
		return nil, httpx.Invalid(map[string]string{"table_ids": "exists"})
	}
	if !confirmed {
		rows, err := q.ListOverlappingSchedules(ctx, store.ListOverlappingSchedulesParams{
			OutletID: outletID, ScheduleID: scheduleID, At: s.now(), StartAt: next.start, EndAt: next.end,
			TableIds: next.tableIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("find overlapping schedules: %w", err)
		}
		if len(rows) > 0 {
			overlaps := make([]map[string]any, len(rows))
			for i, r := range rows {
				slices.SortFunc(r.TableNames, natural)
				overlaps[i] = map[string]any{
					"schedule_id": r.ScheduleID, "title": r.Title, "start_at": r.StartAt.UTC(), "end_at": r.EndAt.UTC(),
					"tables": r.TableNames,
				}
			}
			return nil, ErrScheduleOverlaps.WithDetails(map[string]any{"schedules": overlaps})
		}
	}
	names := make([]string, len(live))
	for i, r := range live {
		names[i] = r.Name
	}
	return names, nil
}

// scheduleFields is a schedule's own fields, checked, as the Log Action
// stores them.
type scheduleFields struct {
	title      string
	start, end time.Time
	tableIDs   []string // sorted, each once
	status     string   // an existing schedule's, when read
}

func (f scheduleFields) snapshot(tableNames []string) map[string]any {
	names := slices.Clone(tableNames)
	slices.SortFunc(names, natural)
	return map[string]any{"title": f.title, "start_at": f.start.UTC(), "end_at": f.end.UTC(), "tables": names}
}

// lockSchedule locks one of the outlet's schedules for a change, which only
// one that isn't over takes: ErrScheduleClosed otherwise.
func lockSchedule(ctx context.Context, q *store.Queries, outletID, scheduleID string, now time.Time) (scheduleFields, error) {
	r, err := q.LockSchedule(ctx, store.LockScheduleParams{ScheduleID: scheduleID, OutletID: outletID, At: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return scheduleFields{}, ErrScheduleNotFound
	}
	if err != nil {
		return scheduleFields{}, fmt.Errorf("lock schedule %s: %w", scheduleID, err)
	}
	if r.Status == ScheduleFinished || r.Status == ScheduleCancelled {
		return scheduleFields{}, ErrScheduleClosed
	}
	return scheduleFields{title: r.Title, start: r.StartAt, end: r.EndAt, status: r.Status}, nil
}

// checkSchedule validates a schedule as filled in, against the one it
// changes, if any (prev): every problem at once as validation_failed. A
// start, when new or changed, is no earlier than the current minute; the
// end is still to come.
func checkSchedule(in ScheduleInput, prev *scheduleFields, now time.Time) (scheduleFields, error) {
	fields := map[string]string{}
	f := scheduleFields{title: strings.TrimSpace(in.Title)}
	switch n := utf8.RuneCountInString(f.title); {
	case n == 0:
		fields["title"] = "required"
	case n > MaxScheduleTitle:
		fields["title"] = "max"
	}
	if in.StartAt == nil {
		fields["start_at"] = "required"
	} else {
		f.start = second(*in.StartAt)
	}
	if in.EndAt == nil {
		fields["end_at"] = "required"
	} else {
		f.end = second(*in.EndAt)
	}
	if in.StartAt != nil && in.EndAt != nil {
		// A schedule that isn't over ends in the future already, so its end
		// is checked whether it changed or not; its start may be past.
		switch {
		case !f.end.After(f.start):
			fields["end_at"] = "after"
		case !f.end.After(now):
			fields["end_at"] = "future"
		}
		if (prev == nil || !f.start.Equal(prev.start)) && f.start.Before(now.Truncate(time.Minute)) {
			fields["start_at"] = "future"
		}
	}
	ids := slices.Compact(slices.Sorted(slices.Values(in.TableIDs)))
	switch {
	case len(ids) == 0:
		fields["table_ids"] = "required"
	case len(ids) > MaxScheduleTables:
		fields["table_ids"] = "max"
	}
	f.tableIDs = ids
	if len(fields) > 0 {
		return scheduleFields{}, httpx.Invalid(fields)
	}
	return f, nil
}

// second is t in UTC, to the second, as the API speaks it.
func second(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }
