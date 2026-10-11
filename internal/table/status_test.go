package table

import (
	"testing"
	"time"

	"github.com/kervzent/maki-be/internal/outlet"
)

// local is a "2006-01-02 15:04:05" time in the zone.
func local(t *testing.T, zone, s string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.ParseInLocation("2006-01-02 15:04:05", s, loc)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func ptr[T any](v T) *T { return &v }

func hours(zone string, open, close string) outlet.Hours {
	return outlet.Hours{Timezone: zone, Open: &open, Close: &close}
}

func TestSeatedSince(t *testing.T) {
	t.Parallel()
	const jakarta, jayapura = "Asia/Jakarta", "Asia/Jayapura"
	day := hours(jakarta, "10:00", "22:00")
	// Open in the evening until one at night.
	night := hours(jakarta, "18:00", "01:00")
	none := outlet.Hours{Timezone: jakarta}
	for _, tt := range []struct {
		name  string
		hours outlet.Hours
		now   string
		want  string
	}{
		{"before opening: since last night's closing", day, "2026-10-05 09:59:59", "2026-10-04 22:00:00"},
		{"at opening", day, "2026-10-05 10:00:00", "2026-10-05 10:00:00"},
		{"open: since the opening", day, "2026-10-05 15:00:00", "2026-10-05 10:00:00"},
		{"just before closing", day, "2026-10-05 21:59:59", "2026-10-05 10:00:00"},
		{"at closing", day, "2026-10-05 22:00:00", "2026-10-05 22:00:00"},
		{"after closing", day, "2026-10-05 23:30:00", "2026-10-05 22:00:00"},
		{"past midnight, closed", day, "2026-10-06 00:30:00", "2026-10-05 22:00:00"},

		{"open past midnight: since the evening's opening", night, "2026-10-05 00:30:00", "2026-10-04 18:00:00"},
		{"at the night's closing", night, "2026-10-05 01:00:00", "2026-10-05 01:00:00"},
		{"closed in the morning", night, "2026-10-05 09:00:00", "2026-10-05 01:00:00"},
		{"just before the evening's opening", night, "2026-10-05 17:59:59", "2026-10-05 01:00:00"},
		{"at the evening's opening", night, "2026-10-05 18:00:00", "2026-10-05 18:00:00"},
		{"open before midnight", night, "2026-10-05 23:00:00", "2026-10-05 18:00:00"},

		{"no hours: since midnight", none, "2026-10-05 23:59:59", "2026-10-05 00:00:00"},
		{"no hours, at midnight", none, "2026-10-05 00:00:00", "2026-10-05 00:00:00"},
		{"no hours, just before midnight", none, "2026-10-04 23:59:59", "2026-10-04 00:00:00"},

		{"in Jayapura's zone", hours(jayapura, "10:00", "22:00"), "2026-10-05 10:30:00", "2026-10-05 10:00:00"},
		{"hours with minutes, just before closing", hours(jakarta, "10:30", "21:45"), "2026-10-05 21:44:59", "2026-10-05 10:30:00"},
		{"hours with minutes, at closing", hours(jakarta, "10:30", "21:45"), "2026-10-05 21:45:00", "2026-10-05 21:45:00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			now := local(t, tt.hours.Timezone, tt.now)
			got, err := seatedSince(now, tt.hours)
			if err != nil {
				t.Fatal(err)
			}
			if want := local(t, tt.hours.Timezone, tt.want); !got.Equal(want) {
				t.Errorf("seatedSince(%s) = %s, want %s", tt.now, got.In(want.Location()), want)
			}
		})
	}
}

// The same moment is before opening in Jakarta and after it in Jayapura,
// two hours ahead.
func TestSeatedSinceIsInTheOutletsZone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 1, 30, 0, 0, time.UTC) // 08:30 in Jakarta, 10:30 in Jayapura
	for zone, want := range map[string]time.Time{
		"Asia/Jakarta":  time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC), // 22:00 the night before
		"Asia/Jayapura": time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC),  // 10:00 that morning
	} {
		got, err := seatedSince(now, hours(zone, "10:00", "22:00"))
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(want) {
			t.Errorf("%s: seatedSince = %s, want %s", zone, got.UTC(), want)
		}
	}
}

func TestSeatedSinceRefusesWhatItCantRead(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 1, 30, 0, 0, time.UTC)
	if _, err := seatedSince(now, outlet.Hours{Timezone: "Asia/Atlantis"}); err == nil {
		t.Error("an unknown zone: want an error")
	}
	if _, err := seatedSince(now, hours("Asia/Jakarta", "10", "22:00")); err == nil {
		t.Error("an opening without its minutes: want an error")
	}
	if _, err := seatedSince(now, hours("Asia/Jakarta", "10:00", "25:00")); err == nil {
		t.Error("a closing past the day: want an error")
	}
}

func TestStatusOrder(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		active, occupied, scheduled bool
		want                        Status
	}{
		{true, false, false, Available},
		{true, true, false, Occupied},
		// Customers seated keep the table occupied until it is released.
		{false, true, false, Occupied},
		{true, true, true, Occupied},
		{false, true, true, Occupied},
		{false, false, false, Inactive},
		// Turned off and scheduled off: turned off.
		{false, false, true, Inactive},
		{true, false, true, Scheduled},
	} {
		if got := status(tt.active, tt.occupied, tt.scheduled); got != tt.want {
			t.Errorf("status(active %v, occupied %v, scheduled %v) = %s, want %s",
				tt.active, tt.occupied, tt.scheduled, got, tt.want)
		}
	}
	for s, want := range map[Status]bool{Available: true, Occupied: true, Inactive: false, Scheduled: false} {
		if got := s.Selectable(); got != want {
			t.Errorf("%s.Selectable() = %v, want %v", s, got, want)
		}
	}
}

func TestSeating(t *testing.T) {
	t.Parallel()
	since := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC) // 10:00 in Jakarta
	at := func(minutes int) time.Time { return since.Add(time.Duration(minutes) * time.Minute) }
	for _, tt := range []struct {
		name     string
		sales    []time.Time
		released *time.Time
		want     Seating
		ok       bool
	}{
		{"no sales", nil, nil, Seating{}, false},
		{"two sales, in any order", []time.Time{at(90), at(30)}, nil, Seating{Since: at(30), Orders: 2}, true},
		{"released between them: the later one", []time.Time{at(30), at(90)}, ptr(at(60)), Seating{Since: at(90), Orders: 1}, true},
		{"released after them all", []time.Time{at(30), at(90)}, ptr(at(120)), Seating{}, false},
		{"released before the outlet opened", []time.Time{at(30)}, ptr(at(-60)), Seating{Since: at(30), Orders: 1}, true},
		{"a sale before the outlet opened, after an older release", []time.Time{at(-30)}, ptr(at(-60)), Seating{}, false},
		{"a sale at the moment of its release doesn't seat anyone", []time.Time{at(60)}, ptr(at(60)), Seating{}, false},
		{"a sale at the moment since doesn't seat anyone", []time.Time{since}, nil, Seating{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := seating(tt.sales, since, tt.released)
			if ok != tt.ok || !got.Since.Equal(tt.want.Since) || got.Orders != tt.want.Orders {
				t.Errorf("seating = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
