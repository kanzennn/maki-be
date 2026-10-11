package table

import (
	"net/http"

	"github.com/kervzent/maki-be/internal/platform/httpx"
)

// Errors of the table endpoints. Messages live in the i18n catalogs.
var (
	// ErrAreaNotFound is an area ID of no area of the outlet, or of a deleted
	// one.
	ErrAreaNotFound = httpx.NewError(http.StatusNotFound, "area_not_found")
	// ErrAreaNameTaken is a name another of the outlet's areas has.
	ErrAreaNameTaken = httpx.NewError(http.StatusConflict, "area_name_taken")
	// ErrAreaNotEmpty is deleting an area that still holds tables;
	// details.tables says how many.
	ErrAreaNotEmpty = httpx.NewError(http.StatusConflict, "area_not_empty")
	// ErrAreasChanged is putting the areas in an order that doesn't list
	// each of them once: the caller's list is out of date.
	ErrAreasChanged = httpx.NewError(http.StatusConflict, "areas_changed")
	// ErrLayoutChanged is saving a layout on a version the area has moved
	// past: its layout or one of its tables changed since the editor opened
	// it. details.version is the area's now.
	ErrLayoutChanged = httpx.NewError(http.StatusConflict, "layout_changed")

	// ErrTableNotFound is a table ID of no table of the outlet, or of a
	// deleted one.
	ErrTableNotFound = httpx.NewError(http.StatusNotFound, "table_not_found")
	// ErrTableNameTaken is a name another of the outlet's tables has.
	ErrTableNameTaken = httpx.NewError(http.StatusConflict, "table_name_taken")
	// ErrTableOccupied is deleting a table customers are seated at: the
	// cashier releases it first.
	ErrTableOccupied = httpx.NewError(http.StatusConflict, "table_occupied")
	// ErrTableUnavailable is a sale at a table the cashier can't seat it at:
	// details.status is the table's, inactive or scheduled, or missing for
	// one deleted or not the outlet's.
	ErrTableUnavailable = httpx.NewError(http.StatusConflict, "table_unavailable")

	// ErrScheduleNotFound is a schedule ID of no schedule of the outlet.
	ErrScheduleNotFound = httpx.NewError(http.StatusNotFound, "schedule_not_found")
	// ErrScheduleClosed is changing or cancelling a schedule that is over:
	// finished, or cancelled already.
	ErrScheduleClosed = httpx.NewError(http.StatusConflict, "schedule_closed")
	// ErrScheduleOverlaps is a schedule that has some of its tables out of
	// service while another has them out too; details.schedules names those,
	// and the tables they share. A warning, not a refusal: the schedule is
	// saved with confirm_overlap.
	ErrScheduleOverlaps = httpx.NewError(http.StatusConflict, "schedule_overlaps")
)
