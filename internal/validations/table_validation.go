package validations

import (
	"strings"
	"unicode/utf8"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// TableRequest is the body of POST and PUT on a table. The caps only bound
// what the server reads; CheckTable applies the real rules.
type TableRequest struct {
	Name     string `json:"name" binding:"required,max=200"`
	AreaID   string `json:"area_id" binding:"required,max=64"`
	Capacity *int   `json:"capacity"`
	Shape    string `json:"shape" binding:"max=16"`
	// IsActive is for a new table, active when left out; an existing one
	// changes it with PATCH …/status.
	IsActive *bool `json:"is_active"`
}

// Input converts the request.
func (r TableRequest) Input() models.TableInput {
	return models.TableInput{Name: r.Name, AreaID: r.AreaID, Capacity: r.Capacity, Shape: r.Shape, Active: r.IsActive}
}

// CheckTableFilter checks the table list's filters: a status is one of a
// table's.
func CheckTableFilter(f models.TableFilter) (models.TableFilter, error) {
	switch f.Status {
	case "", models.TableAvailable, models.TableOccupied, models.TableInactive, models.TableScheduled:
	default:
		return models.TableFilter{}, apperror.Invalid(map[string]string{"status": "oneof"})
	}
	return f, nil
}

// CheckTable validates a table as filled in: every problem at once as
// validation_failed.
func CheckTable(in models.TableInput) (models.TableFields, error) {
	fields := map[string]string{}
	t := models.TableFields{Name: strings.TrimSpace(in.Name), AreaID: in.AreaID, Capacity: in.Capacity, Shape: in.Shape}
	switch n := utf8.RuneCountInString(t.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > models.MaxTableName:
		fields["name"] = "max"
	}
	if t.AreaID == "" {
		fields["area_id"] = "required"
	}
	if c := t.Capacity; c != nil && (*c < 1 || *c > models.MaxCapacity) {
		fields["capacity"] = "range"
	}
	switch t.Shape {
	case "":
		t.Shape = models.ShapeSquare
	case models.ShapeSquare, models.ShapeRound, models.ShapeRectangle:
	default:
		fields["shape"] = "oneof"
	}
	if len(fields) > 0 {
		return models.TableFields{}, apperror.Invalid(fields)
	}
	return t, nil
}
