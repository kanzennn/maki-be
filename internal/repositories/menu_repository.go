package repositories

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/menu"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// MenuRepository owns menus, each outlet's menus, with their stock counts.
type MenuRepository struct {
	db *config.Database
}

// NewMenuRepository returns the menus' repository.
func NewMenuRepository(db *config.Database) *MenuRepository {
	return &MenuRepository{db: db}
}

// List returns a page of the outlet's menus, in the cashier's order, and
// how many match in all.
func (r *MenuRepository) List(ctx context.Context, outletID string, f models.MenuFilter, page pagination.Page) ([]models.Menu, int, error) {
	var active *bool
	if f.Status != "" {
		a := f.Status == "active"
		active = &a
	}
	search := optional(f.Search)
	if search != nil {
		*search = likeEscaper.Replace(*search)
	}
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountMenus(ctx, store.CountMenusParams{
		OutletID: outletID, CategoryID: optional(f.CategoryID), Active: active, StockStatus: optional(f.Stock), Search: search,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count menus: %w", err)
	}
	rows, err := q.ListMenus(ctx, store.ListMenusParams{
		OutletID: outletID, CategoryID: optional(f.CategoryID), Active: active, StockStatus: optional(f.Stock), Search: search,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list menus: %w", err)
	}
	out := make([]models.Menu, len(rows))
	for i, row := range rows {
		out[i] = menuFrom(store.GetMenuRow(row))
	}
	return out, int(total), nil
}

// Get returns one of the outlet's menus: ErrMenuNotFound if it has no such
// menu.
func (r *MenuRepository) Get(ctx context.Context, outletID, menuID string) (models.Menu, error) {
	row, err := store.New(r.db.Conn(ctx)).GetMenu(ctx, store.GetMenuParams{MenuID: menuID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Menu{}, apperror.ErrMenuNotFound
	}
	if err != nil {
		return models.Menu{}, fmt.Errorf("load menu %s: %w", menuID, err)
	}
	return menuFrom(row), nil
}

// Insert stores a new menu of the outlet, its stock at 0: ErrMenuSKUTaken
// if another of its menus has the SKU.
func (r *MenuRepository) Insert(ctx context.Context, outletID, menuID string, f models.MenuFields, active bool) error {
	return skuTaken(store.New(r.db.Conn(ctx)).InsertMenu(ctx, store.InsertMenuParams{
		MenuID: menuID, OutletID: outletID, CategoryID: f.CategoryID, Name: f.Name,
		Description: f.Description, Sku: f.SKU, Price: f.Price, TrackStock: f.TrackStock,
		StockAlert: int32Ptr(f.StockAlert), IsActive: active,
	}))
}

// Lock locks one of the outlet's menus for a change to it or its stock, one
// at a time, so no two adjustments read the same count: ErrMenuNotFound if
// it has no such menu.
func (r *MenuRepository) Lock(ctx context.Context, outletID, menuID string) (models.MenuFields, error) {
	row, err := store.New(r.db.Conn(ctx)).LockMenu(ctx, store.LockMenuParams{MenuID: menuID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.MenuFields{}, apperror.ErrMenuNotFound
	}
	if err != nil {
		return models.MenuFields{}, fmt.Errorf("lock menu %s: %w", menuID, err)
	}
	return models.MenuFields{
		CategoryID: row.CategoryID, Name: row.Name, Description: row.Description, SKU: row.Sku, Price: row.Price,
		TrackStock: row.TrackStock, StockAlert: intPtr(row.StockAlert), StockQty: int(row.StockQty), Active: row.IsActive,
	}, nil
}

// Update stores every field of the menu but its stock and status:
// ErrMenuSKUTaken if another menu of its outlet has the SKU.
func (r *MenuRepository) Update(ctx context.Context, menuID string, f models.MenuFields) error {
	return skuTaken(store.New(r.db.Conn(ctx)).UpdateMenu(ctx, store.UpdateMenuParams{
		MenuID: menuID, CategoryID: f.CategoryID, Name: f.Name, Description: f.Description,
		Sku: f.SKU, Price: f.Price, TrackStock: f.TrackStock, StockAlert: int32Ptr(f.StockAlert),
	}))
}

// SetActive shows or hides the menu at the cashier.
func (r *MenuRepository) SetActive(ctx context.Context, menuID string, active bool) error {
	return store.New(r.db.Conn(ctx)).SetMenuActive(ctx, store.SetMenuActiveParams{MenuID: menuID, IsActive: active})
}

// SetStockAlert stores the count at or below which the menu's stock is low;
// nil turns the alert off.
func (r *MenuRepository) SetStockAlert(ctx context.Context, menuID string, alert *int) error {
	return store.New(r.db.Conn(ctx)).SetMenuStockAlert(ctx, store.SetMenuStockAlertParams{MenuID: menuID, StockAlert: int32Ptr(alert)})
}

// SetStock stores the menu's count.
func (r *MenuRepository) SetStock(ctx context.Context, menuID string, qty int) error {
	return store.New(r.db.Conn(ctx)).SetMenuStock(ctx, store.SetMenuStockParams{
		MenuID: menuID, StockQty: int32(qty), //nolint:gosec // a count stays near MaxStock
	})
}

// Delete soft-deletes the menu. Its SKU is free again.
func (r *MenuRepository) Delete(ctx context.Context, menuID string) error {
	return store.New(r.db.Conn(ctx)).DeleteMenu(ctx, menuID)
}

// ListForCashier returns what the outlet's cashier sells, in the order it
// shows it: the active menus of active categories, those out of stock last
// and otherwise by category, then name.
func (r *MenuRepository) ListForCashier(ctx context.Context, outletID string) ([]models.Menu, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListPosMenus(ctx, outletID)
	if err != nil {
		return nil, fmt.Errorf("list the cashier's menus: %w", err)
	}
	out := make([]models.Menu, len(rows))
	for i, row := range rows {
		out[i] = menuFrom(store.GetMenuRow(row))
	}
	return out, nil
}

// LockForSale locks the outlet's menus of menuIDs, in the order of their
// IDs, until the transaction ctx carries ends. A deleted menu, or another
// outlet's, is left out.
func (r *MenuRepository) LockForSale(ctx context.Context, outletID string, menuIDs []string) ([]models.SaleMenu, error) {
	rows, err := store.New(r.db.Conn(ctx)).LockMenusForSale(ctx, store.LockMenusForSaleParams{OutletID: outletID, MenuIds: menuIDs})
	if err != nil {
		return nil, err
	}
	out := make([]models.SaleMenu, len(rows))
	for i, row := range rows {
		out[i] = models.SaleMenu{
			MenuID: row.MenuID, Name: row.Name, Price: row.Price, TrackStock: row.TrackStock, StockQty: int(row.StockQty),
			Available: row.Available,
		}
	}
	return out, nil
}

// skuTaken turns the SKU's unique violation into ErrMenuSKUTaken.
func skuTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "menus_outlet_sku_key" {
		return apperror.ErrMenuSKUTaken
	}
	return err
}

func menuFrom(row store.GetMenuRow) models.Menu {
	return models.Menu{
		MenuID: row.MenuID, Name: row.Name, Description: row.Description, SKU: row.Sku, Price: row.Price, ImageKey: row.ImageKey,
		TrackStock: row.TrackStock, StockQty: int(row.StockQty), StockAlert: intPtr(row.StockAlert), StockStatus: row.StockStatus,
		Active: row.IsActive, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Category: models.MenuCategory{
			CategoryID: row.CategoryID, Name: row.CategoryName, Icon: row.CategoryIcon, Active: row.CategoryActive,
		},
	}
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // callers cap it at MaxStock
	return &n
}
