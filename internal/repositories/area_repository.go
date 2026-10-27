package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/table"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// AreaRepository owns table_areas: each outlet's dining areas, in order,
// each with its canvas — its decorations, as JSON, and the version an
// editor saves on.
type AreaRepository struct {
	db *config.Database
}

// NewAreaRepository returns the areas' repository.
func NewAreaRepository(db *config.Database) *AreaRepository {
	return &AreaRepository{db: db}
}

// List returns a page of the outlet's areas, in their order, and how many
// there are in all.
func (r *AreaRepository) List(ctx context.Context, outletID string, page pagination.Page) ([]models.Area, int, error) {
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountAreas(ctx, outletID)
	if err != nil {
		return nil, 0, fmt.Errorf("count areas: %w", err)
	}
	rows, err := q.ListAreas(ctx, store.ListAreasParams{
		OutletID:   outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list areas: %w", err)
	}
	out := make([]models.Area, len(rows))
	for i, row := range rows {
		out[i] = areaFrom(store.GetAreaRow(row))
	}
	return out, int(total), nil
}

// Get returns one of the outlet's areas: ErrAreaNotFound if it has no such
// area.
func (r *AreaRepository) Get(ctx context.Context, outletID, areaID string) (models.Area, error) {
	row, err := store.New(r.db.Conn(ctx)).GetArea(ctx, store.GetAreaParams{AreaID: areaID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Area{}, apperror.ErrAreaNotFound
	}
	if err != nil {
		return models.Area{}, fmt.Errorf("load area %s: %w", areaID, err)
	}
	return areaFrom(row), nil
}

// NextOrder returns the place after the outlet's last area.
func (r *AreaRepository) NextOrder(ctx context.Context, outletID string) (int, error) {
	n, err := store.New(r.db.Conn(ctx)).NextAreaOrder(ctx, outletID)
	if err != nil {
		return 0, fmt.Errorf("order of a new area: %w", err)
	}
	return int(n), nil
}

// Insert stores a new area of the outlet at sortOrder, with an empty
// canvas: ErrAreaNameTaken if another of its areas has the name.
func (r *AreaRepository) Insert(ctx context.Context, outletID, areaID, name string, sortOrder int) error {
	return areaNameTaken(store.New(r.db.Conn(ctx)).InsertArea(ctx, store.InsertAreaParams{
		AreaID: areaID, OutletID: outletID, Name: name,
		SortOrder: int32(sortOrder), //nolint:gosec // one more than the areas an outlet has
	}))
}

// Lock locks one of the outlet's areas for a change to it, to its layout or
// to one of its tables, and returns its canvas: ErrAreaNotFound if it has no
// such area.
func (r *AreaRepository) Lock(ctx context.Context, outletID, areaID string) (models.AreaCanvas, error) {
	row, err := store.New(r.db.Conn(ctx)).LockArea(ctx, store.LockAreaParams{AreaID: areaID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AreaCanvas{}, apperror.ErrAreaNotFound
	}
	if err != nil {
		return models.AreaCanvas{}, fmt.Errorf("lock area %s: %w", areaID, err)
	}
	return canvasFrom(row.AreaID, row.Name, int(row.LayoutVersion), row.Decorations)
}

// Canvas returns one of the outlet's areas' canvas: ErrAreaNotFound if it
// has no such area.
func (r *AreaRepository) Canvas(ctx context.Context, outletID, areaID string) (models.AreaCanvas, error) {
	row, err := store.New(r.db.Conn(ctx)).GetAreaLayout(ctx, store.GetAreaLayoutParams{AreaID: areaID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.AreaCanvas{}, apperror.ErrAreaNotFound
	}
	if err != nil {
		return models.AreaCanvas{}, fmt.Errorf("load the layout of area %s: %w", areaID, err)
	}
	return canvasFrom(row.AreaID, row.Name, int(row.LayoutVersion), row.Decorations)
}

// Update renames the area: ErrAreaNameTaken if another area of its outlet
// has the name.
func (r *AreaRepository) Update(ctx context.Context, areaID, name string) error {
	return areaNameTaken(store.New(r.db.Conn(ctx)).UpdateArea(ctx, store.UpdateAreaParams{AreaID: areaID, Name: name}))
}

// LockIDs locks the outlet's areas and returns their IDs, in order.
func (r *AreaRepository) LockIDs(ctx context.Context, outletID string) ([]string, error) {
	ids, err := store.New(r.db.Conn(ctx)).LockAreaIDs(ctx, outletID)
	if err != nil {
		return nil, fmt.Errorf("lock areas: %w", err)
	}
	return ids, nil
}

// SetOrder puts the outlet's areas in the order of areaIDs.
func (r *AreaRepository) SetOrder(ctx context.Context, outletID string, areaIDs []string) error {
	return store.New(r.db.Conn(ctx)).SetAreaOrder(ctx, store.SetAreaOrderParams{OutletID: outletID, AreaIds: areaIDs})
}

// CountTables returns how many tables the area holds that aren't deleted.
func (r *AreaRepository) CountTables(ctx context.Context, areaID string) (int, error) {
	n, err := store.New(r.db.Conn(ctx)).CountAreaTables(ctx, areaID)
	if err != nil {
		return 0, fmt.Errorf("count the tables of area %s: %w", areaID, err)
	}
	return int(n), nil
}

// Delete soft-deletes the area, and its canvas with it.
func (r *AreaRepository) Delete(ctx context.Context, areaID string) error {
	return store.New(r.db.Conn(ctx)).DeleteArea(ctx, areaID)
}

// SaveDecorations stores the area's decorations, and moves its layout's
// version on.
func (r *AreaRepository) SaveDecorations(ctx context.Context, areaID string, decorations []models.Decoration) error {
	saved, err := json.Marshal(toDecorationsJSON(decorations))
	if err != nil {
		return fmt.Errorf("write the decorations of area %s: %w", areaID, err)
	}
	if err := store.New(r.db.Conn(ctx)).SaveDecorations(ctx, store.SaveDecorationsParams{AreaID: areaID, Decorations: saved}); err != nil {
		return fmt.Errorf("save the decorations of area %s: %w", areaID, err)
	}
	return nil
}

// BumpLayoutVersion moves the area's layout version on: one of its tables
// changed.
func (r *AreaRepository) BumpLayoutVersion(ctx context.Context, areaID string) error {
	if err := store.New(r.db.Conn(ctx)).BumpLayoutVersion(ctx, areaID); err != nil {
		return fmt.Errorf("move the layout of area %s on: %w", areaID, err)
	}
	return nil
}

// Floor returns the outlet's areas, in order, with their decorations and
// without their tables.
func (r *AreaRepository) Floor(ctx context.Context, outletID string) ([]models.FloorArea, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListFloorAreas(ctx, outletID)
	if err != nil {
		return nil, fmt.Errorf("list the areas of %s: %w", outletID, err)
	}
	out := make([]models.FloorArea, len(rows))
	for i, row := range rows {
		decorations, err := readDecorations(row.Decorations)
		if err != nil {
			return nil, err
		}
		out[i] = models.FloorArea{AreaID: row.AreaID, Name: row.Name, Decorations: decorations}
	}
	return out, nil
}

// areaNameTaken turns the name's unique violation into ErrAreaNameTaken.
func areaNameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "table_areas_outlet_name_key" {
		return apperror.ErrAreaNameTaken
	}
	return err
}

func areaFrom(row store.GetAreaRow) models.Area {
	return models.Area{
		AreaID: row.AreaID, Name: row.Name, SortOrder: int(row.SortOrder), TableCount: int(row.TableCount),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func canvasFrom(areaID, name string, version int, raw []byte) (models.AreaCanvas, error) {
	decorations, err := readDecorations(raw)
	if err != nil {
		return models.AreaCanvas{}, err
	}
	return models.AreaCanvas{AreaID: areaID, Name: name, Version: version, Decorations: decorations}, nil
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

func toDecorationsJSON(ds []models.Decoration) []decorationJSON {
	out := make([]decorationJSON, len(ds))
	for i, d := range ds {
		out[i] = decorationJSON{Kind: d.Kind, X: d.Box.X, Y: d.Box.Y, W: d.Box.W, H: d.Box.H, Rotation: d.Rotation}
	}
	return out
}

func readDecorations(raw []byte) ([]models.Decoration, error) {
	var stored []decorationJSON
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("read decorations: %w", err)
	}
	out := make([]models.Decoration, len(stored))
	for i, d := range stored {
		out[i] = models.Decoration{Kind: d.Kind, Box: models.Box{X: d.X, Y: d.Y, W: d.W, H: d.H}, Rotation: d.Rotation}
	}
	return out, nil
}
