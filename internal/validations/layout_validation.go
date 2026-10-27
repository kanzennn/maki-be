package validations

import (
	"strconv"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// LayoutRequest is the body of PUT /outlets/{oid}/areas/{aid}/layout: the
// tables placed on it (the area's others come off it), and its
// decorations. Both lists are required, empty or not.
type LayoutRequest struct {
	Version     *int                `json:"version" binding:"required"`
	Tables      []PlacementRequest  `json:"tables" binding:"required,max=1000"`
	Decorations []DecorationRequest `json:"decorations" binding:"required,max=1000"`
}

// PlacementRequest is a table's place in a LayoutRequest.
type PlacementRequest struct {
	TableID  string `json:"table_id"`
	X        *int   `json:"x"`
	Y        *int   `json:"y"`
	W        *int   `json:"w"`
	H        *int   `json:"h"`
	Rotation *int   `json:"rotation"`
	Shape    string `json:"shape"`
}

// DecorationRequest is a decoration in a LayoutRequest.
type DecorationRequest struct {
	Kind     string `json:"kind"`
	X        *int   `json:"x"`
	Y        *int   `json:"y"`
	W        *int   `json:"w"`
	H        *int   `json:"h"`
	Rotation *int   `json:"rotation"`
}

// Input converts the request, whose places and decorations
// models.Placement and models.DecorationInput repeat without JSON tags.
func (r LayoutRequest) Input() models.LayoutInput {
	in := models.LayoutInput{
		Version: r.Version, Tables: make([]models.Placement, len(r.Tables)),
		Decorations: make([]models.DecorationInput, len(r.Decorations)),
	}
	for i, t := range r.Tables {
		in.Tables[i] = models.Placement(t)
	}
	for i, d := range r.Decorations {
		in.Decorations[i] = models.DecorationInput(d)
	}
	return in
}

// CheckLayout validates a layout as the editor sends it: every problem at
// once as validation_failed, by path (tables.0.x, decorations.2.kind). The
// tables are the area's; the layout service checks that under the area's
// lock.
func CheckLayout(in models.LayoutInput) ([]models.PlacedTable, []models.Decoration, error) {
	fields := map[string]string{}
	if in.Version == nil {
		fields["version"] = "required"
	}
	places := make([]models.PlacedTable, len(in.Tables))
	seen := map[string]bool{}
	for i, pl := range in.Tables {
		path := "tables." + strconv.Itoa(i) + "."
		switch {
		case pl.TableID == "":
			fields[path+"table_id"] = "required"
		case seen[pl.TableID]:
			fields[path+"table_id"] = "duplicate"
		}
		seen[pl.TableID] = true
		box, ok := checkBox(fields, path, pl.X, pl.Y, pl.W, pl.H, models.MinSide, models.MaxSide)
		rotation, rotated := checkRotation(fields, path, pl.Rotation)
		switch pl.Shape {
		case models.ShapeSquare, models.ShapeRound, models.ShapeRectangle:
		case "":
			fields[path+"shape"] = "required"
		default:
			fields[path+"shape"] = "oneof"
		}
		places[i] = models.PlacedTable{TableID: pl.TableID, Rotation: rotation, Shape: pl.Shape}
		if ok && rotated {
			places[i].Box = box
		}
	}
	// Tables don't overlap one another; decorations may overlap anything.
	for i := range places {
		for j := range i {
			if overlap(places[i].Box, places[j].Box) {
				fields["tables."+strconv.Itoa(j)] = "overlap"
				fields["tables."+strconv.Itoa(i)] = "overlap"
			}
		}
	}
	if len(in.Decorations) > models.MaxDecorations {
		fields["decorations"] = "max"
	}
	decorations := make([]models.Decoration, len(in.Decorations))
	for i, d := range in.Decorations {
		path := "decorations." + strconv.Itoa(i) + "."
		switch d.Kind {
		case models.KindWall, models.KindCashier, models.KindDoor:
		case "":
			fields[path+"kind"] = "required"
		default:
			fields[path+"kind"] = "oneof"
		}
		box, _ := checkBox(fields, path, d.X, d.Y, d.W, d.H, 1, max(models.GridWidth, models.GridHeight))
		rotation, _ := checkRotation(fields, path, d.Rotation)
		decorations[i] = models.Decoration{Kind: d.Kind, Box: box, Rotation: rotation}
	}
	if len(fields) > 0 {
		return nil, nil, apperror.Invalid(fields)
	}
	return places, decorations, nil
}

// checkBox checks a box on the grid, its sides from least to most cells,
// into fields under path; ok is false when it isn't a box to compare.
func checkBox(fields map[string]string, path string, x, y, w, h *int, least, most int) (b models.Box, ok bool) {
	ok = true
	for _, f := range []struct {
		name string
		v    *int
	}{{"x", x}, {"y", y}, {"w", w}, {"h", h}} {
		if f.v == nil {
			fields[path+f.name] = "required"
			ok = false
		}
	}
	if !ok {
		return models.Box{}, false
	}
	b = models.Box{X: *x, Y: *y, W: *w, H: *h}
	if b.W < least || b.W > min(most, models.GridWidth) {
		fields[path+"w"] = "range"
		ok = false
	}
	if b.H < least || b.H > min(most, models.GridHeight) {
		fields[path+"h"] = "range"
		ok = false
	}
	if b.X < 0 || b.X+b.W > models.GridWidth {
		fields[path+"x"] = "range"
		ok = false
	}
	if b.Y < 0 || b.Y+b.H > models.GridHeight {
		fields[path+"y"] = "range"
		ok = false
	}
	return b, ok
}

// checkRotation checks a turn of 0, 90, 180 or 270 degrees into fields
// under path.
func checkRotation(fields map[string]string, path string, r *int) (int, bool) {
	switch {
	case r == nil:
		fields[path+"rotation"] = "required"
	case *r == 0 || *r == 90 || *r == 180 || *r == 270:
		return *r, true
	default:
		fields[path+"rotation"] = "oneof"
	}
	return 0, false
}

// overlap reports whether two boxes share a cell. An empty box shares none.
func overlap(a, b models.Box) bool {
	return a.W > 0 && b.W > 0 && a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
