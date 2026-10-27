package repositories

import (
	"context"
	"fmt"
	"math"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/menu"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// StockRepository owns stock_movements: the history of each tracked menu's
// count, of which the menu keeps the latest.
type StockRepository struct {
	db *config.Database
}

// NewStockRepository returns the stock history's repository.
func NewStockRepository(db *config.Database) *StockRepository {
	return &StockRepository{db: db}
}

// Insert records a change to a menu's stock.
func (r *StockRepository) Insert(ctx context.Context, m models.NewStockMovement) error {
	if err := store.New(r.db.Conn(ctx)).InsertStockMovement(ctx, store.InsertStockMovementParams{
		StockMovementID: id.New(), OutletID: m.OutletID, MenuID: m.MenuID, Type: m.Type,
		QtyChange: int32(m.After - m.Before), StockAfter: int32(m.After), //nolint:gosec // a count stays near MaxStock
		Reason: m.Reason, Note: m.Note, UserID: m.UserID, OrderID: m.OrderID,
	}); err != nil {
		return fmt.Errorf("record stock movement of menu %s: %w", m.MenuID, err)
	}
	return nil
}

// List returns a page of the menu's stock history, newest first, and how
// many entries there are in all. Who made each comes without a name.
func (r *StockRepository) List(ctx context.Context, outletID, menuID string, page pagination.Page) ([]models.StockMovement, int, error) {
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountStockMovements(ctx, store.CountStockMovementsParams{MenuID: menuID, OutletID: outletID})
	if err != nil {
		return nil, 0, fmt.Errorf("count stock movements: %w", err)
	}
	rows, err := q.ListStockMovements(ctx, store.ListStockMovementsParams{
		MenuID: menuID, OutletID: outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list stock movements: %w", err)
	}
	out := make([]models.StockMovement, len(rows))
	for i, row := range rows {
		out[i] = models.StockMovement{
			StockMovementID: row.StockMovementID, Type: row.Type, QtyChange: int(row.QtyChange), StockAfter: int(row.StockAfter),
			Reason: row.Reason, Note: row.Note, UserID: row.UserID, OrderID: row.OrderID, CreatedAt: row.CreatedAt,
		}
	}
	return out, int(total), nil
}
