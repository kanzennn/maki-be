package services

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/natural"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// ScheduleService is the times each outlet's tables are out of service: an
// event, a renovation, the terrace in the rainy season. A schedule's status
// is the one it has now.
type ScheduleService struct {
	db        *config.Database
	schedules *repositories.ScheduleRepository
	tables    *repositories.TableRepository
	users     *UserService
	audit     *AuditService
	now       func() time.Time
}

// NewScheduleService returns the schedules' service. users names who made
// and who cancelled a schedule; now is the clock its status is read at.
func NewScheduleService(db *config.Database, schedules *repositories.ScheduleRepository, tables *repositories.TableRepository,
	users *UserService, audit *AuditService, now func() time.Time,
) *ScheduleService {
	return &ScheduleService{db: db, schedules: schedules, tables: tables, users: users, audit: audit, now: now}
}

// ListSchedules returns a page of the outlet's schedules, the latest first,
// and how many match in all.
func (s *ScheduleService) ListSchedules(ctx context.Context, outletID string, f models.ScheduleFilter, page pagination.Page) ([]models.Schedule, int, error) {
	out, total, err := s.schedules.List(ctx, outletID, f, s.now(), page)
	if err != nil {
		return nil, 0, err
	}
	if err := s.fill(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// Schedule returns one of the outlet's schedules.
func (s *ScheduleService) Schedule(ctx context.Context, outletID, scheduleID string) (models.Schedule, error) {
	sc, err := s.schedules.Get(ctx, outletID, scheduleID, s.now())
	if err != nil {
		return models.Schedule{}, err
	}
	out := []models.Schedule{sc}
	if err := s.fill(ctx, out); err != nil {
		return models.Schedule{}, err
	}
	return out[0], nil
}

// fill gives the schedules their tables, by area and name, and their
// users' names.
func (s *ScheduleService) fill(ctx context.Context, schedules []models.Schedule) error {
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
	rows, err := s.schedules.Tables(ctx, ids)
	if err != nil {
		return err
	}
	tables := map[string][]models.ScheduleTable{}
	for _, t := range rows {
		tables[t.ScheduleID] = append(tables[t.ScheduleID], t)
	}
	names, err := s.users.Names(ctx, slices.Compact(slices.Sorted(slices.Values(userIDs))))
	if err != nil {
		return err
	}
	for i := range schedules {
		sc := &schedules[i]
		sc.Tables = tables[sc.ScheduleID]
		slices.SortFunc(sc.Tables, func(a, b models.ScheduleTable) int {
			return cmp.Or(cmp.Compare(a.AreaOrder, b.AreaOrder), strings.Compare(a.AreaID, b.AreaID),
				natural.Compare(a.Name, b.Name), strings.Compare(a.TableID, b.TableID))
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
func (s *ScheduleService) CreateSchedule(ctx context.Context, p models.Principal, ip, outletID string, in models.ScheduleInput) (models.Schedule, error) {
	next, err := validations.CheckSchedule(in, nil, s.now())
	if err != nil {
		return models.Schedule{}, err
	}
	scheduleID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		names, err := s.scheduleTables(ctx, outletID, "", next, in.ConfirmOverlap)
		if err != nil {
			return err
		}
		if err := s.schedules.Insert(ctx, outletID, scheduleID, next, p.UserID); err != nil {
			return err
		}
		if err := s.schedules.AddTables(ctx, outletID, scheduleID, next.TableIDs); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "table_schedule.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "table_schedule", TargetID: scheduleID, After: next.Snapshot(names), IP: ip,
		})
	})
	if err != nil {
		return models.Schedule{}, err
	}
	return s.Schedule(ctx, outletID, scheduleID)
}

// UpdateSchedule changes a schedule that isn't over (ErrScheduleClosed): a
// start or an end that changes can't be in the past, so one under way
// keeps the start it has. Overlaps are as CreateSchedule's. A change of
// nothing records nothing.
func (s *ScheduleService) UpdateSchedule(ctx context.Context, p models.Principal, ip, outletID, scheduleID string, in models.ScheduleInput) (models.Schedule, error) {
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		now := s.now()
		prev, err := s.lockOpen(ctx, outletID, scheduleID, now)
		if err != nil {
			return err
		}
		next, err := validations.CheckSchedule(in, &prev, now)
		if err != nil {
			return err
		}
		names, err := s.scheduleTables(ctx, outletID, scheduleID, next, in.ConfirmOverlap)
		if err != nil {
			return err
		}
		prevTables, err := s.schedules.Tables(ctx, []string{scheduleID})
		if err != nil {
			return err
		}
		was := make([]string, len(prevTables))
		for i, t := range prevTables {
			was[i] = t.Name
		}
		before, after := changedAsPrinted(prev.Snapshot(was), next.Snapshot(names))
		if len(after) == 0 {
			return nil
		}
		if err := s.schedules.Update(ctx, scheduleID, next); err != nil {
			return err
		}
		if err := s.schedules.KeepTables(ctx, scheduleID, next.TableIDs); err != nil {
			return err
		}
		if err := s.schedules.AddTables(ctx, outletID, scheduleID, next.TableIDs); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "table_schedule.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "table_schedule", TargetID: scheduleID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Schedule{}, err
	}
	return s.Schedule(ctx, outletID, scheduleID)
}

// CancelSchedule ends a schedule that isn't over (ErrScheduleClosed): its
// tables are back in service, and it stays as a record.
func (s *ScheduleService) CancelSchedule(ctx context.Context, p models.Principal, ip, outletID, scheduleID string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := s.lockOpen(ctx, outletID, scheduleID, s.now()); err != nil {
			return err
		}
		if err := s.schedules.Cancel(ctx, scheduleID, p.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "table_schedule.cancelled", UserID: p.UserID, OutletID: outletID,
			TargetType: "table_schedule", TargetID: scheduleID, IP: ip,
		})
	})
}

// lockOpen locks one of the outlet's schedules for a change, which only one
// that isn't over takes: ErrScheduleClosed otherwise.
func (s *ScheduleService) lockOpen(ctx context.Context, outletID, scheduleID string, now time.Time) (models.ScheduleFields, error) {
	f, err := s.schedules.Lock(ctx, outletID, scheduleID, now)
	if err != nil {
		return models.ScheduleFields{}, err
	}
	if f.Status == models.ScheduleFinished || f.Status == models.ScheduleCancelled {
		return models.ScheduleFields{}, apperror.ErrScheduleClosed
	}
	return f, nil
}

// scheduleTables checks a schedule's tables: the outlet's, not deleted (a
// field error otherwise), and, unless confirmed, at no time another schedule
// but scheduleID has any of them out. It returns their names.
func (s *ScheduleService) scheduleTables(ctx context.Context, outletID, scheduleID string, next models.ScheduleFields, confirmed bool) ([]string, error) {
	names, err := s.tables.LiveNames(ctx, outletID, next.TableIDs)
	if err != nil {
		return nil, err
	}
	if len(names) != len(next.TableIDs) {
		return nil, apperror.Invalid(map[string]string{"table_ids": "exists"})
	}
	if !confirmed {
		overlaps, err := s.schedules.Overlapping(ctx, outletID, scheduleID, s.now(), next)
		if err != nil {
			return nil, err
		}
		if len(overlaps) > 0 {
			details := make([]map[string]any, len(overlaps))
			for i, o := range overlaps {
				slices.SortFunc(o.TableNames, natural.Compare)
				details[i] = map[string]any{
					"schedule_id": o.ScheduleID, "title": o.Title, "start_at": o.StartAt.UTC(), "end_at": o.EndAt.UTC(),
					"tables": o.TableNames,
				}
			}
			return nil, apperror.ErrScheduleOverlaps.WithDetails(map[string]any{"schedules": details})
		}
	}
	return names, nil
}
