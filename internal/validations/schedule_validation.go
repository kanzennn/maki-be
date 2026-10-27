package validations

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// ScheduleRequest is the body of POST and PUT /outlets/{oid}/table-schedules.
// ConfirmOverlap saves it though another schedule has some of its tables
// out at the same time. The caps only bound what the server reads;
// CheckSchedule applies the real rules.
type ScheduleRequest struct {
	Title          string     `json:"title" binding:"required,max=400"`
	StartAt        *time.Time `json:"start_at" binding:"required"`
	EndAt          *time.Time `json:"end_at" binding:"required"`
	TableIDs       []string   `json:"table_ids" binding:"required,max=1000,dive,max=64"`
	ConfirmOverlap bool       `json:"confirm_overlap"`
}

// Input converts the request, whose fields models.ScheduleInput repeats
// without JSON tags.
func (r ScheduleRequest) Input() models.ScheduleInput {
	return models.ScheduleInput(r)
}

// CheckScheduleFilter checks the schedule list's filter: a status is one of
// a schedule's.
func CheckScheduleFilter(f models.ScheduleFilter) (models.ScheduleFilter, error) {
	switch f.Status {
	case "", models.ScheduleUpcoming, models.ScheduleOngoing, models.ScheduleFinished, models.ScheduleCancelled:
	default:
		return models.ScheduleFilter{}, apperror.Invalid(map[string]string{"status": "oneof"})
	}
	return f, nil
}

// CheckSchedule validates a schedule as filled in, against the one it
// changes, if any (prev): every problem at once as validation_failed. A
// start, when new or changed, is no earlier than the current minute; the
// end is still to come.
func CheckSchedule(in models.ScheduleInput, prev *models.ScheduleFields, now time.Time) (models.ScheduleFields, error) {
	fields := map[string]string{}
	f := models.ScheduleFields{Title: strings.TrimSpace(in.Title)}
	switch n := utf8.RuneCountInString(f.Title); {
	case n == 0:
		fields["title"] = "required"
	case n > models.MaxScheduleTitle:
		fields["title"] = "max"
	}
	if in.StartAt == nil {
		fields["start_at"] = "required"
	} else {
		f.Start = second(*in.StartAt)
	}
	if in.EndAt == nil {
		fields["end_at"] = "required"
	} else {
		f.End = second(*in.EndAt)
	}
	if in.StartAt != nil && in.EndAt != nil {
		// A schedule that isn't over ends in the future already, so its end
		// is checked whether it changed or not; its start may be past.
		switch {
		case !f.End.After(f.Start):
			fields["end_at"] = "after"
		case !f.End.After(now):
			fields["end_at"] = "future"
		}
		if (prev == nil || !f.Start.Equal(prev.Start)) && f.Start.Before(now.Truncate(time.Minute)) {
			fields["start_at"] = "future"
		}
	}
	ids := slices.Compact(slices.Sorted(slices.Values(in.TableIDs)))
	switch {
	case len(ids) == 0:
		fields["table_ids"] = "required"
	case len(ids) > models.MaxScheduleTables:
		fields["table_ids"] = "max"
	}
	f.TableIDs = ids
	if len(fields) > 0 {
		return models.ScheduleFields{}, apperror.Invalid(fields)
	}
	return f, nil
}

// second is t in UTC, to the second, as the API speaks it.
func second(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }
