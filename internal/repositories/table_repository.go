package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/table"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// TableRepository owns tables: each outlet's tables, in their areas, and
// their places on the areas' layouts. A table's status comes from its sales,
// which the services read from the orders; the tables keep when the cashier
// last released them.
type TableRepository struct {
	db *config.Database
}

// NewTableRepository returns the tables' repository.
func NewTableRepository(db *config.Database) *TableRepository {
	return &TableRepository{db: db}
}

// List returns the outlet's tables, or one area's, as they are at the
// moment at, without their status.
func (r *TableRepository) List(ctx context.Context, outletID string, areaID *string, at time.Time) ([]models.Table, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListTables(ctx, store.ListTablesParams{OutletID: outletID, AreaID: areaID, At: at})
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	out := make([]models.Table, len(rows))
	for i, row := range rows {
		out[i] = tableFrom(store.GetTableRow(row))
	}
	return out, nil
}

// Get returns one of the outlet's tables as it is at the moment at, without
// its status: ErrTableNotFound if it has no such table.
func (r *TableRepository) Get(ctx context.Context, outletID, tableID string, at time.Time) (models.Table, error) {
	row, err := store.New(r.db.Conn(ctx)).GetTable(ctx, store.GetTableParams{TableID: tableID, OutletID: outletID, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Table{}, apperror.ErrTableNotFound
	}
	if err != nil {
		return models.Table{}, fmt.Errorf("load table %s: %w", tableID, err)
	}
	return tableFrom(row), nil
}

// Insert stores a new table of the outlet, not placed on its area's layout:
// ErrTableNameTaken if another of its tables has the name.
func (r *TableRepository) Insert(ctx context.Context, outletID, tableID string, f models.TableFields, active bool) error {
	return tableNameTaken(store.New(r.db.Conn(ctx)).InsertTable(ctx, store.InsertTableParams{
		TableID: tableID, OutletID: outletID, AreaID: f.AreaID, Name: f.Name,
		Capacity: capacity32(f.Capacity), Shape: f.Shape, IsActive: active,
	}))
}

// Update stores the table's name, area, seats and shape: ErrTableNameTaken
// if another table of its outlet has the name.
func (r *TableRepository) Update(ctx context.Context, tableID string, f models.TableFields) error {
	return tableNameTaken(store.New(r.db.Conn(ctx)).UpdateTable(ctx, store.UpdateTableParams{
		TableID: tableID, AreaID: f.AreaID, Name: f.Name, Capacity: capacity32(f.Capacity), Shape: f.Shape,
	}))
}

// SetActive turns the table on or off at the cashier.
func (r *TableRepository) SetActive(ctx context.Context, tableID string, active bool) error {
	return store.New(r.db.Conn(ctx)).SetTableActive(ctx, store.SetTableActiveParams{TableID: tableID, IsActive: active})
}

// Delete soft-deletes the table.
func (r *TableRepository) Delete(ctx context.Context, tableID string) error {
	return store.New(r.db.Conn(ctx)).DeleteTable(ctx, tableID)
}

// AreaOf returns the area of one of the outlet's tables, read before the
// table is locked: ErrTableNotFound if it has no such table.
func (r *TableRepository) AreaOf(ctx context.Context, outletID, tableID string) (string, error) {
	areaID, err := store.New(r.db.Conn(ctx)).GetTableArea(ctx, store.GetTableAreaParams{TableID: tableID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apperror.ErrTableNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find the area of table %s: %w", tableID, err)
	}
	return areaID, nil
}

// Lock locks one of the outlet's tables for a change: ErrTableNotFound if it
// has no such table.
func (r *TableRepository) Lock(ctx context.Context, outletID, tableID string) (models.TableLock, error) {
	row, err := store.New(r.db.Conn(ctx)).LockTable(ctx, store.LockTableParams{TableID: tableID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TableLock{}, apperror.ErrTableNotFound
	}
	if err != nil {
		return models.TableLock{}, fmt.Errorf("lock table %s: %w", tableID, err)
	}
	return models.TableLock{
		TableFields: models.TableFields{Name: row.Name, AreaID: row.AreaID, Capacity: intPtr(row.Capacity), Shape: row.Shape},
		TableID:     row.TableID, Active: row.IsActive, ReleasedAt: row.ReleasedAt,
	}, nil
}

// LockInArea locks the area's tables for a layout save, and returns them
// with their places: each table's ID, name, shape, box and turn.
func (r *TableRepository) LockInArea(ctx context.Context, areaID string) ([]models.Table, error) {
	rows, err := store.New(r.db.Conn(ctx)).LockAreaTables(ctx, areaID)
	if err != nil {
		return nil, fmt.Errorf("lock the tables of area %s: %w", areaID, err)
	}
	out := make([]models.Table, len(rows))
	for i, row := range rows {
		out[i] = models.Table{
			TableID: row.TableID, AreaID: areaID, Name: row.Name, Shape: row.Shape, Rotation: int(row.Rotation),
			Box: boxOf(row.X, row.Y, row.W, row.H),
		}
	}
	return out, nil
}

// Place puts the tables in their boxes, and the area's others off its
// layout.
func (r *TableRepository) Place(ctx context.Context, areaID string, places []models.PlacedTable) error {
	q := store.New(r.db.Conn(ctx))
	params := store.PlaceTablesParams{AreaID: areaID}
	placed := make([]string, len(places))
	for i, pl := range places {
		placed[i] = pl.TableID
		params.TableIds = append(params.TableIds, pl.TableID)
		params.Xs = append(params.Xs, int32(pl.Box.X))                  //nolint:gosec // checked to the grid
		params.Ys = append(params.Ys, int32(pl.Box.Y))                  //nolint:gosec // as above
		params.Ws = append(params.Ws, int32(pl.Box.W))                  //nolint:gosec // as above
		params.Hs = append(params.Hs, int32(pl.Box.H))                  //nolint:gosec // as above
		params.Rotations = append(params.Rotations, int32(pl.Rotation)) //nolint:gosec // 0 to 270
		params.Shapes = append(params.Shapes, pl.Shape)
	}
	if err := q.PlaceTables(ctx, params); err != nil {
		return fmt.Errorf("place the tables of area %s: %w", areaID, err)
	}
	if err := q.UnplaceTables(ctx, store.UnplaceTablesParams{AreaID: areaID, Placed: placed}); err != nil {
		return fmt.Errorf("take tables off the layout of area %s: %w", areaID, err)
	}
	return nil
}

// HoldForSale holds one of the outlet's tables for a sale (FOR SHARE) until
// the transaction ctx carries ends, and returns it as it is at the moment
// at, without its status: ErrTableNotFound if it has no such table.
func (r *TableRepository) HoldForSale(ctx context.Context, outletID, tableID string, at time.Time) (models.Table, error) {
	row, err := store.New(r.db.Conn(ctx)).HoldTable(ctx, store.HoldTableParams{TableID: tableID, OutletID: outletID, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Table{}, apperror.ErrTableNotFound
	}
	if err != nil {
		return models.Table{}, fmt.Errorf("hold table %s: %w", tableID, err)
	}
	return models.Table{
		TableID: row.TableID, Name: row.Name, Active: row.IsActive, ReleasedAt: row.ReleasedAt, Scheduled: row.Scheduled,
	}, nil
}

// Release notes that the table's customers left: the sales before now no
// longer seat anyone there.
func (r *TableRepository) Release(ctx context.Context, tableID string) error {
	if err := store.New(r.db.Conn(ctx)).ReleaseTable(ctx, tableID); err != nil {
		return fmt.Errorf("release table %s: %w", tableID, err)
	}
	return nil
}

// LiveNames returns the names of the tables among tableIDs that are the
// outlet's and not deleted.
func (r *TableRepository) LiveNames(ctx context.Context, outletID string, tableIDs []string) ([]string, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListLiveTables(ctx, store.ListLiveTablesParams{OutletID: outletID, TableIds: tableIDs})
	if err != nil {
		return nil, fmt.Errorf("find the tables of a schedule: %w", err)
	}
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}
	return names, nil
}

// tableNameTaken turns the name's unique violation into ErrTableNameTaken.
func tableNameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "tables_outlet_name_key" {
		return apperror.ErrTableNameTaken
	}
	return err
}

func tableFrom(row store.GetTableRow) models.Table {
	return models.Table{
		TableID: row.TableID, AreaID: row.AreaID, AreaName: row.AreaName, AreaOrder: int(row.AreaSortOrder),
		Name: row.Name, Capacity: intPtr(row.Capacity), Shape: row.Shape, Box: boxOf(row.X, row.Y, row.W, row.H),
		Rotation: int(row.Rotation), Active: row.IsActive, ReleasedAt: row.ReleasedAt, Scheduled: row.Scheduled,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// boxOf is a table's box, or nil while it isn't placed: the schema keeps
// the box whole or leaves it out.
func boxOf(x, y, w, h *int32) *models.Box {
	if x == nil || y == nil || w == nil || h == nil {
		return nil
	}
	return &models.Box{X: int(*x), Y: int(*y), W: int(*w), H: int(*h)}
}

func capacity32(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // checked to MaxCapacity
	return &n
}
