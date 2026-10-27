package models

import (
	"slices"
	"time"

	"github.com/kervzent/maki-be/internal/utils/natural"
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
	CreatedBy   Person
	CancelledAt *time.Time
	CancelledBy *Person
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ScheduleTable is a table a schedule takes out of service, deleted since
// or not.
type ScheduleTable struct {
	ScheduleID string
	TableID    string
	Name       string
	AreaID     string
	AreaName   string
	// AreaOrder is its area's place in the outlet's order, for sorting.
	AreaOrder int
	Deleted   bool
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

// ScheduleFields are a schedule's own fields, checked.
type ScheduleFields struct {
	Title      string
	Start, End time.Time
	TableIDs   []string // sorted, each once
	Status     string   // an existing schedule's, when read
}

// Snapshot is the fields as the Log Action stores them, the tables by their
// names.
func (f ScheduleFields) Snapshot(tableNames []string) map[string]any {
	names := slices.Clone(tableNames)
	slices.SortFunc(names, natural.Compare)
	return map[string]any{"title": f.Title, "start_at": f.Start.UTC(), "end_at": f.End.UTC(), "tables": names}
}

// OverlappingSchedule is another schedule that has some of the same tables
// out of service at the same time.
type OverlappingSchedule struct {
	ScheduleID string
	Title      string
	StartAt    time.Time
	EndAt      time.Time
	TableNames []string // the tables they share
}

// ScheduleTableResponse is a table a schedule takes out of service.
type ScheduleTableResponse struct {
	TableID string          `json:"table_id"`
	Name    string          `json:"name"`
	Area    AreaRefResponse `json:"area"`
	// Deleted is whether the table was deleted since.
	Deleted bool `json:"deleted"`
}

// ScheduleResponse is a schedule as the schedule endpoints send it.
type ScheduleResponse struct {
	ScheduleID string    `json:"schedule_id"`
	Title      string    `json:"title"`
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`
	// Status is the schedule's now: upcoming, ongoing, finished or
	// cancelled.
	Status      string                  `json:"status"`
	Tables      []ScheduleTableResponse `json:"tables"`
	CreatedBy   PersonResponse          `json:"created_by"`
	CancelledAt *time.Time              `json:"cancelled_at"`
	CancelledBy *PersonResponse         `json:"cancelled_by"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

// NewScheduleResponse returns the schedule's response, its times in UTC.
func NewScheduleResponse(s Schedule) ScheduleResponse {
	out := ScheduleResponse{
		ScheduleID: s.ScheduleID, Title: s.Title, StartAt: s.StartAt.UTC(), EndAt: s.EndAt.UTC(), Status: s.Status,
		Tables:    make([]ScheduleTableResponse, len(s.Tables)),
		CreatedBy: NewPersonResponse(s.CreatedBy), CreatedAt: s.CreatedAt.UTC(), UpdatedAt: s.UpdatedAt.UTC(),
	}
	if s.CancelledAt != nil {
		at := s.CancelledAt.UTC()
		out.CancelledAt = &at
	}
	if s.CancelledBy != nil {
		by := NewPersonResponse(*s.CancelledBy)
		out.CancelledBy = &by
	}
	for i, t := range s.Tables {
		out.Tables[i] = ScheduleTableResponse{
			TableID: t.TableID, Name: t.Name, Area: AreaRefResponse{AreaID: t.AreaID, Name: t.AreaName}, Deleted: t.Deleted,
		}
	}
	return out
}
