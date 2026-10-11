package table

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/table/store"
)

// Layout returns one of the outlet's areas as its canvas: its decorations,
// and its tables with their statuses now, placed or not.
func (s *Service) Layout(ctx context.Context, outletID, areaID string) (Layout, error) {
	var out Layout
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := store.New(tx).GetAreaLayout(ctx, store.GetAreaLayoutParams{AreaID: areaID, OutletID: outletID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAreaNotFound
		}
		if err != nil {
			return fmt.Errorf("load the layout of area %s: %w", areaID, err)
		}
		decorations, err := readDecorations(a.Decorations)
		if err != nil {
			return err
		}
		tables, err := s.tablesIn(ctx, tx, outletID, &areaID)
		if err != nil {
			return err
		}
		out = Layout{
			AreaID: a.AreaID, AreaName: a.Name, Version: int(a.LayoutVersion), Decorations: decorations, Tables: tables,
		}
		return nil
	})
	return out, err
}

// SaveLayout saves an area's whole layout at once, as the editor sends it:
// the tables in it placed in their boxes, the area's others off the
// layout, and its decorations. The editor saves on the version it opened;
// one the area has moved past since is ErrLayoutChanged. Saving the layout
// as it is records nothing, and keeps its version.
func (s *Service) SaveLayout(ctx context.Context, p authn.Principal, ip, outletID, areaID string, in LayoutInput) (Layout, error) {
	places, decorations, err := checkLayout(in)
	if err != nil {
		return Layout{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		a, err := lockArea(ctx, q, outletID, areaID)
		if err != nil {
			return err
		}
		if int(a.LayoutVersion) != *in.Version {
			return ErrLayoutChanged.WithDetails(map[string]any{"version": a.LayoutVersion})
		}
		rows, err := q.LockAreaTables(ctx, areaID)
		if err != nil {
			return fmt.Errorf("lock the tables of area %s: %w", areaID, err)
		}
		current := map[string]store.LockAreaTablesRow{}
		for _, r := range rows {
			current[r.TableID] = r
		}
		fields := map[string]string{}
		for i, pl := range places {
			if _, ok := current[pl.tableID]; !ok {
				fields["tables."+strconv.Itoa(i)+".table_id"] = "exists"
			}
		}
		if len(fields) > 0 {
			return httpx.Invalid(fields)
		}
		before, after := moves(rows, places)
		decorationsBefore, err := readDecorations(a.Decorations)
		if err != nil {
			return err
		}
		redecorated := !slices.Equal(decorationsBefore, decorations)
		if len(after) == 0 && !redecorated {
			return nil
		}
		if err := place(ctx, q, areaID, places); err != nil {
			return err
		}
		saved, err := json.Marshal(toDecorationsJSON(decorations))
		if err != nil {
			return fmt.Errorf("write the decorations of area %s: %w", areaID, err)
		}
		// Moves the layout's version on, its tables' changes or not.
		if err := q.SaveDecorations(ctx, store.SaveDecorationsParams{AreaID: areaID, Decorations: saved}); err != nil {
			return fmt.Errorf("save the decorations of area %s: %w", areaID, err)
		}
		if redecorated {
			before["decorations"], after["decorations"] = len(decorationsBefore), len(decorations)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "area.layout_updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "area", TargetID: areaID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return Layout{}, err
	}
	return s.Layout(ctx, outletID, areaID)
}

// placement is a Placement that passed checkLayout.
type placement struct {
	tableID  string
	box      Box
	rotation int
	shape    string
}

// place puts the tables in their boxes, and the area's others off its
// layout.
func place(ctx context.Context, q *store.Queries, areaID string, places []placement) error {
	params := store.PlaceTablesParams{AreaID: areaID}
	placed := make([]string, len(places))
	for i, pl := range places {
		placed[i] = pl.tableID
		params.TableIds = append(params.TableIds, pl.tableID)
		params.Xs = append(params.Xs, int32(pl.box.X))                  //nolint:gosec // checked to the grid
		params.Ys = append(params.Ys, int32(pl.box.Y))                  //nolint:gosec // as above
		params.Ws = append(params.Ws, int32(pl.box.W))                  //nolint:gosec // as above
		params.Hs = append(params.Hs, int32(pl.box.H))                  //nolint:gosec // as above
		params.Rotations = append(params.Rotations, int32(pl.rotation)) //nolint:gosec // 0 to 270
		params.Shapes = append(params.Shapes, pl.shape)
	}
	if err := q.PlaceTables(ctx, params); err != nil {
		return fmt.Errorf("place the tables of area %s: %w", areaID, err)
	}
	if err := q.UnplaceTables(ctx, store.UnplaceTablesParams{AreaID: areaID, Placed: placed}); err != nil {
		return fmt.Errorf("take tables off the layout of area %s: %w", areaID, err)
	}
	return nil
}

// moves returns the tables whose place changes, by name, as they were and
// as they will be: their box, turn and shape, or nil off the layout.
func moves(rows []store.LockAreaTablesRow, places []placement) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	next := map[string]placement{}
	for _, pl := range places {
		next[pl.tableID] = pl
	}
	for _, r := range rows {
		// Nil off the layout: a table off it and staying off isn't a move.
		var was, will any
		if r.X != nil && r.Y != nil && r.W != nil && r.H != nil {
			was = spot(Box{X: int(*r.X), Y: int(*r.Y), W: int(*r.W), H: int(*r.H)}, int(r.Rotation), r.Shape)
		}
		if pl, ok := next[r.TableID]; ok {
			will = spot(pl.box, pl.rotation, pl.shape)
		}
		if fmt.Sprint(was) != fmt.Sprint(will) {
			before[r.Name], after[r.Name] = was, will
		}
	}
	return before, after
}

func spot(b Box, rotation int, shape string) map[string]any {
	return map[string]any{"x": b.X, "y": b.Y, "w": b.W, "h": b.H, "rotation": rotation, "shape": shape}
}

// checkLayout validates a layout as the editor sends it: every problem at
// once as validation_failed, by path (tables.0.x, decorations.2.kind). The
// tables are the area's; SaveLayout checks that under the area's lock.
func checkLayout(in LayoutInput) ([]placement, []Decoration, error) {
	fields := map[string]string{}
	if in.Version == nil {
		fields["version"] = "required"
	}
	places := make([]placement, len(in.Tables))
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
		box, ok := checkBox(fields, path, pl.X, pl.Y, pl.W, pl.H, MinSide, MaxSide)
		rotation, rotated := checkRotation(fields, path, pl.Rotation)
		switch pl.Shape {
		case ShapeSquare, ShapeRound, ShapeRectangle:
		case "":
			fields[path+"shape"] = "required"
		default:
			fields[path+"shape"] = "oneof"
		}
		places[i] = placement{tableID: pl.TableID, rotation: rotation, shape: pl.Shape}
		if ok && rotated {
			places[i].box = box
		}
	}
	// Tables don't overlap one another; decorations may overlap anything.
	for i := range places {
		for j := range i {
			if overlap(places[i].box, places[j].box) {
				fields["tables."+strconv.Itoa(j)] = "overlap"
				fields["tables."+strconv.Itoa(i)] = "overlap"
			}
		}
	}
	if len(in.Decorations) > MaxDecorations {
		fields["decorations"] = "max"
	}
	decorations := make([]Decoration, len(in.Decorations))
	for i, d := range in.Decorations {
		path := "decorations." + strconv.Itoa(i) + "."
		switch d.Kind {
		case KindWall, KindCashier, KindDoor:
		case "":
			fields[path+"kind"] = "required"
		default:
			fields[path+"kind"] = "oneof"
		}
		box, _ := checkBox(fields, path, d.X, d.Y, d.W, d.H, 1, max(GridWidth, GridHeight))
		rotation, _ := checkRotation(fields, path, d.Rotation)
		decorations[i] = Decoration{Kind: d.Kind, Box: box, Rotation: rotation}
	}
	if len(fields) > 0 {
		return nil, nil, httpx.Invalid(fields)
	}
	return places, decorations, nil
}

// checkBox checks a box on the grid, its sides from least to most cells,
// into fields under path; ok is false when it isn't a box to compare.
func checkBox(fields map[string]string, path string, x, y, w, h *int, least, most int) (b Box, ok bool) {
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
		return Box{}, false
	}
	b = Box{X: *x, Y: *y, W: *w, H: *h}
	if b.W < least || b.W > min(most, GridWidth) {
		fields[path+"w"] = "range"
		ok = false
	}
	if b.H < least || b.H > min(most, GridHeight) {
		fields[path+"h"] = "range"
		ok = false
	}
	if b.X < 0 || b.X+b.W > GridWidth {
		fields[path+"x"] = "range"
		ok = false
	}
	if b.Y < 0 || b.Y+b.H > GridHeight {
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
func overlap(a, b Box) bool {
	return a.W > 0 && b.W > 0 && a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// decorationJSON is a decoration as table_areas.decorations stores it.
type decorationJSON struct {
	Kind     string `json:"kind"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	Rotation int    `json:"rotation"`
}

func toDecorationsJSON(ds []Decoration) []decorationJSON {
	out := make([]decorationJSON, len(ds))
	for i, d := range ds {
		out[i] = decorationJSON{Kind: d.Kind, X: d.Box.X, Y: d.Box.Y, W: d.Box.W, H: d.Box.H, Rotation: d.Rotation}
	}
	return out
}

func readDecorations(raw []byte) ([]Decoration, error) {
	var stored []decorationJSON
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("read decorations: %w", err)
	}
	out := make([]Decoration, len(stored))
	for i, d := range stored {
		out[i] = Decoration{Kind: d.Kind, Box: Box{X: d.X, Y: d.Y, W: d.W, H: d.H}, Rotation: d.Rotation}
	}
	return out, nil
}
