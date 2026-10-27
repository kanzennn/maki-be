package services

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/validations"
)

// LayoutService is each area's canvas: where its tables stand, and its
// decorations — walls, the cashier, doors. The editor saves the whole
// layout at once, on the version it opened.
type LayoutService struct {
	db     *config.Database
	areas  *repositories.AreaRepository
	tables *repositories.TableRepository
	floor  *TableService
	audit  *AuditService
}

// NewLayoutService returns the layouts' service. floor gives the tables on
// a layout their statuses.
func NewLayoutService(db *config.Database, areas *repositories.AreaRepository, tables *repositories.TableRepository,
	floor *TableService, audit *AuditService,
) *LayoutService {
	return &LayoutService{db: db, areas: areas, tables: tables, floor: floor, audit: audit}
}

// Layout returns one of the outlet's areas as its canvas: its decorations,
// and its tables with their statuses now, placed or not.
func (s *LayoutService) Layout(ctx context.Context, outletID, areaID string) (models.Layout, error) {
	var out models.Layout
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		a, err := s.areas.Canvas(ctx, outletID, areaID)
		if err != nil {
			return err
		}
		tables, err := s.floor.AreaTables(ctx, outletID, areaID)
		if err != nil {
			return err
		}
		out = models.Layout{AreaID: a.AreaID, AreaName: a.Name, Version: a.Version, Decorations: a.Decorations, Tables: tables}
		return nil
	})
	return out, err
}

// SaveLayout saves an area's whole layout at once, as the editor sends it:
// the tables in it placed in their boxes, the area's others off the
// layout, and its decorations. The editor saves on the version it opened;
// one the area has moved past since is ErrLayoutChanged. Saving the layout
// as it is records nothing, and keeps its version.
func (s *LayoutService) SaveLayout(ctx context.Context, p models.Principal, ip, outletID, areaID string, in models.LayoutInput) (models.Layout, error) {
	places, decorations, err := validations.CheckLayout(in)
	if err != nil {
		return models.Layout{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		a, err := s.areas.Lock(ctx, outletID, areaID)
		if err != nil {
			return err
		}
		if a.Version != *in.Version {
			return apperror.ErrLayoutChanged.WithDetails(map[string]any{"version": a.Version})
		}
		current, err := s.tables.LockInArea(ctx, areaID)
		if err != nil {
			return err
		}
		inArea := map[string]bool{}
		for _, t := range current {
			inArea[t.TableID] = true
		}
		fields := map[string]string{}
		for i, pl := range places {
			if !inArea[pl.TableID] {
				fields["tables."+strconv.Itoa(i)+".table_id"] = "exists"
			}
		}
		if len(fields) > 0 {
			return apperror.Invalid(fields)
		}
		before, after := moves(current, places)
		redecorated := !slices.Equal(a.Decorations, decorations)
		if len(after) == 0 && !redecorated {
			return nil
		}
		if err := s.tables.Place(ctx, areaID, places); err != nil {
			return err
		}
		// Moves the layout's version on, its tables' changes or not.
		if err := s.areas.SaveDecorations(ctx, areaID, decorations); err != nil {
			return err
		}
		if redecorated {
			before["decorations"], after["decorations"] = len(a.Decorations), len(decorations)
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "area.layout_updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "area", TargetID: areaID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Layout{}, err
	}
	return s.Layout(ctx, outletID, areaID)
}

// moves returns the tables whose place changes, by name, as they were and
// as they will be: their box, turn and shape, or nil off the layout.
func moves(current []models.Table, places []models.PlacedTable) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	next := map[string]models.PlacedTable{}
	for _, pl := range places {
		next[pl.TableID] = pl
	}
	for _, t := range current {
		// Nil off the layout: a table off it and staying off isn't a move.
		var was, will any
		if t.Box != nil {
			was = spot(*t.Box, t.Rotation, t.Shape)
		}
		if pl, ok := next[t.TableID]; ok {
			will = spot(pl.Box, pl.Rotation, pl.Shape)
		}
		if fmt.Sprint(was) != fmt.Sprint(will) {
			before[t.Name], after[t.Name] = was, will
		}
	}
	return before, after
}

func spot(b models.Box, rotation int, shape string) map[string]any {
	return map[string]any{"x": b.X, "y": b.Y, "w": b.W, "h": b.H, "rotation": rotation, "shape": shape}
}
