package repositories

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/table"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// ScheduleRepository owns table_schedules and table_schedule_items: the
// times each outlet's tables are out of service, and which tables each
// takes out. A schedule's status is the one it has at a moment.
type ScheduleRepository struct {
	db *config.Database
}

// NewScheduleRepository returns the schedules' repository.
func NewScheduleRepository(db *config.Database) *ScheduleRepository {
	return &ScheduleRepository{db: db}
}

// List returns a page of the outlet's schedules, the latest first, with
// their statuses at the moment at, and how many match in all. Their tables
// and their users' names are left out.
func (r *ScheduleRepository) List(ctx context.Context, outletID string, f models.ScheduleFilter, at time.Time, page pagination.Page) ([]models.Schedule, int, error) {
	status := optional(f.Status)
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountSchedules(ctx, store.CountSchedulesParams{OutletID: outletID, Status: status, At: at})
	if err != nil {
		return nil, 0, fmt.Errorf("count schedules: %w", err)
	}
	rows, err := q.ListSchedules(ctx, store.ListSchedulesParams{
		OutletID: outletID, Status: status, At: at,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list schedules: %w", err)
	}
	out := make([]models.Schedule, len(rows))
	for i, row := range rows {
		out[i] = scheduleFrom(store.GetScheduleRow(row))
	}
	return out, int(total), nil
}

// Get returns one of the outlet's schedules, with its status at the moment
// at: ErrScheduleNotFound if it has no such schedule. Its tables and its
// users' names are left out.
func (r *ScheduleRepository) Get(ctx context.Context, outletID, scheduleID string, at time.Time) (models.Schedule, error) {
	row, err := store.New(r.db.Conn(ctx)).GetSchedule(ctx, store.GetScheduleParams{ScheduleID: scheduleID, OutletID: outletID, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Schedule{}, apperror.ErrScheduleNotFound
	}
	if err != nil {
		return models.Schedule{}, fmt.Errorf("load schedule %s: %w", scheduleID, err)
	}
	return scheduleFrom(row), nil
}

// Tables returns the tables of the schedules, deleted since or not.
func (r *ScheduleRepository) Tables(ctx context.Context, scheduleIDs []string) ([]models.ScheduleTable, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListScheduleTables(ctx, scheduleIDs)
	if err != nil {
		return nil, fmt.Errorf("list the tables of schedules: %w", err)
	}
	out := make([]models.ScheduleTable, len(rows))
	for i, row := range rows {
		out[i] = models.ScheduleTable{
			ScheduleID: row.ScheduleID, TableID: row.TableID, Name: row.Name, AreaID: row.AreaID, AreaName: row.AreaName,
			AreaOrder: int(row.AreaSortOrder), Deleted: row.Deleted,
		}
	}
	return out, nil
}

// Insert stores a new schedule of the outlet, without its tables.
func (r *ScheduleRepository) Insert(ctx context.Context, outletID, scheduleID string, f models.ScheduleFields, createdBy string) error {
	if err := store.New(r.db.Conn(ctx)).InsertSchedule(ctx, store.InsertScheduleParams{
		ScheduleID: scheduleID, OutletID: outletID, Title: f.Title, StartAt: f.Start, EndAt: f.End, CreatedBy: createdBy,
	}); err != nil {
		return fmt.Errorf("insert schedule: %w", err)
	}
	return nil
}

// AddTables puts the tables on the schedule; those on it already stay.
func (r *ScheduleRepository) AddTables(ctx context.Context, outletID, scheduleID string, tableIDs []string) error {
	if err := store.New(r.db.Conn(ctx)).InsertScheduleItems(ctx, store.InsertScheduleItemsParams{
		ScheduleID: scheduleID, OutletID: outletID, TableIds: tableIDs,
	}); err != nil {
		return fmt.Errorf("put tables on schedule %s: %w", scheduleID, err)
	}
	return nil
}

// KeepTables takes the tables not among tableIDs off the schedule.
func (r *ScheduleRepository) KeepTables(ctx context.Context, scheduleID string, tableIDs []string) error {
	if err := store.New(r.db.Conn(ctx)).DeleteScheduleItems(ctx, store.DeleteScheduleItemsParams{
		ScheduleID: scheduleID, TableIds: tableIDs,
	}); err != nil {
		return fmt.Errorf("take tables off schedule %s: %w", scheduleID, err)
	}
	return nil
}

// Update stores the schedule's title and times.
func (r *ScheduleRepository) Update(ctx context.Context, scheduleID string, f models.ScheduleFields) error {
	if err := store.New(r.db.Conn(ctx)).UpdateSchedule(ctx, store.UpdateScheduleParams{
		ScheduleID: scheduleID, Title: f.Title, StartAt: f.Start, EndAt: f.End,
	}); err != nil {
		return fmt.Errorf("update schedule %s: %w", scheduleID, err)
	}
	return nil
}

// Cancel ends the schedule; cancelledBy is who did it.
func (r *ScheduleRepository) Cancel(ctx context.Context, scheduleID, cancelledBy string) error {
	if err := store.New(r.db.Conn(ctx)).CancelSchedule(ctx, store.CancelScheduleParams{
		ScheduleID: scheduleID, CancelledBy: &cancelledBy,
	}); err != nil {
		return fmt.Errorf("cancel schedule %s: %w", scheduleID, err)
	}
	return nil
}

// Lock locks one of the outlet's schedules for a change, and returns its
// fields with its status at the moment at: ErrScheduleNotFound if it has no
// such schedule.
func (r *ScheduleRepository) Lock(ctx context.Context, outletID, scheduleID string, at time.Time) (models.ScheduleFields, error) {
	row, err := store.New(r.db.Conn(ctx)).LockSchedule(ctx, store.LockScheduleParams{ScheduleID: scheduleID, OutletID: outletID, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ScheduleFields{}, apperror.ErrScheduleNotFound
	}
	if err != nil {
		return models.ScheduleFields{}, fmt.Errorf("lock schedule %s: %w", scheduleID, err)
	}
	return models.ScheduleFields{Title: row.Title, Start: row.StartAt, End: row.EndAt, Status: row.Status}, nil
}

// Overlapping returns the outlet's schedules but scheduleID, neither
// cancelled nor over at the moment at, whose time crosses f's at any of
// its tables; with those tables' names.
func (r *ScheduleRepository) Overlapping(ctx context.Context, outletID, scheduleID string, at time.Time, f models.ScheduleFields) ([]models.OverlappingSchedule, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListOverlappingSchedules(ctx, store.ListOverlappingSchedulesParams{
		OutletID: outletID, ScheduleID: scheduleID, At: at, StartAt: f.Start, EndAt: f.End, TableIds: f.TableIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("find overlapping schedules: %w", err)
	}
	out := make([]models.OverlappingSchedule, len(rows))
	for i, row := range rows {
		out[i] = models.OverlappingSchedule(row)
	}
	return out, nil
}

func scheduleFrom(row store.GetScheduleRow) models.Schedule {
	out := models.Schedule{
		ScheduleID: row.ScheduleID, Title: row.Title, StartAt: row.StartAt, EndAt: row.EndAt, Status: row.Status,
		CreatedBy: models.Person{UserID: row.CreatedBy}, CancelledAt: row.CancelledAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.CancelledBy != nil {
		out.CancelledBy = &models.Person{UserID: *row.CancelledBy}
	}
	return out
}
