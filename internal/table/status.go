package table

import (
	"fmt"
	"time"

	"github.com/kervzent/maki-be/internal/outlet"
)

// seatedSince is the moment from which a sale at a table still seats
// customers there: the outlet's last opening or closing at or before now, in
// its time zone. So a table empties itself when the outlet closes, and a
// sale after closing keeps it only until the outlet opens again. An outlet
// without hours empties its tables at the start of its day.
func seatedSince(now time.Time, h outlet.Hours) (time.Time, error) {
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load time zone %q: %w", h.Timezone, err)
	}
	y, m, d := now.In(loc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if h.Open == nil || h.Close == nil {
		return today, nil
	}
	opens, err := clock(*h.Open)
	if err != nil {
		return time.Time{}, err
	}
	closes, err := clock(*h.Close)
	if err != nil {
		return time.Time{}, err
	}
	// The outlet opens and closes at those times every day, a closing past
	// midnight included. Yesterday's opening is always before now, so the
	// last of them is yesterday's or today's.
	var last time.Time
	for _, day := range []time.Time{today.AddDate(0, 0, -1), today} {
		for _, event := range []time.Time{at(day, opens), at(day, closes)} {
			if !event.After(now) && event.After(last) {
				last = event
			}
		}
	}
	return last, nil
}

// clock is a "15:04" time of day as the time since midnight.
func clock(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("read the time of day %q: %w", s, err)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

// at is the time of day on day, in day's zone.
func at(day time.Time, t time.Duration) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d, int(t/time.Hour), int(t%time.Hour/time.Minute), 0, 0, day.Location())
}

// status is a table's status, in this order: occupied while customers are
// seated, an inactive or a scheduled table too until it is released; then
// inactive, scheduled, and available.
func status(active, occupied, scheduled bool) Status {
	switch {
	case occupied:
		return Occupied
	case !active:
		return Inactive
	case scheduled:
		return Scheduled
	default:
		return Available
	}
}

// Seating is the customers seated at a table: since the first sale that
// seated them, and how many sales there are at the table since.
type Seating struct {
	Since  time.Time
	Orders int
}

// seating reads who is seated at a table from its sales since since (when
// each paid dine-in order there was written): those after the table was
// last released. ok is false when nobody is.
func seating(sales []time.Time, since time.Time, releasedAt *time.Time) (s Seating, ok bool) {
	from := since
	if releasedAt != nil && releasedAt.After(from) {
		from = *releasedAt
	}
	for _, sale := range sales {
		if !sale.After(from) {
			continue
		}
		if s.Orders == 0 || sale.Before(s.Since) {
			s.Since = sale
		}
		s.Orders++
	}
	return s, s.Orders > 0
}
