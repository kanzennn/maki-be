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

// CategoryRepository owns menu_categories, each outlet's menu categories,
// in the order its cashier shows them.
type CategoryRepository struct {
	db *config.Database
}

// NewCategoryRepository returns the categories' repository.
func NewCategoryRepository(db *config.Database) *CategoryRepository {
	return &CategoryRepository{db: db}
}

// List returns a page of the outlet's categories, in the cashier's order,
// and how many there are in all.
func (r *CategoryRepository) List(ctx context.Context, outletID string, page pagination.Page) ([]models.Category, int, error) {
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountCategories(ctx, outletID)
	if err != nil {
		return nil, 0, fmt.Errorf("count categories: %w", err)
	}
	rows, err := q.ListCategories(ctx, store.ListCategoriesParams{
		OutletID:   outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list categories: %w", err)
	}
	out := make([]models.Category, len(rows))
	for i, row := range rows {
		out[i] = categoryFrom(store.GetCategoryRow(row))
	}
	return out, int(total), nil
}

// Get returns one of the outlet's categories: ErrCategoryNotFound if it has
// no such category.
func (r *CategoryRepository) Get(ctx context.Context, outletID, categoryID string) (models.Category, error) {
	row, err := store.New(r.db.Conn(ctx)).GetCategory(ctx, store.GetCategoryParams{CategoryID: categoryID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Category{}, apperror.ErrCategoryNotFound
	}
	if err != nil {
		return models.Category{}, fmt.Errorf("load category %s: %w", categoryID, err)
	}
	return categoryFrom(row), nil
}

// NextOrder returns the place in the cashier's order after the outlet's
// last category.
func (r *CategoryRepository) NextOrder(ctx context.Context, outletID string) (int, error) {
	n, err := store.New(r.db.Conn(ctx)).NextCategoryOrder(ctx, outletID)
	if err != nil {
		return 0, fmt.Errorf("order of a new category: %w", err)
	}
	return int(n), nil
}

// Insert stores a new category of the outlet at sortOrder:
// ErrCategoryNameTaken if another of its categories has the name.
func (r *CategoryRepository) Insert(ctx context.Context, outletID, categoryID string, f models.CategoryFields, sortOrder int) error {
	return categoryNameTaken(store.New(r.db.Conn(ctx)).InsertCategory(ctx, store.InsertCategoryParams{
		CategoryID: categoryID, OutletID: outletID, Name: f.Name, Icon: f.Icon,
		SortOrder: int32(sortOrder), //nolint:gosec // one more than the categories an outlet has
	}))
}

// Lock locks one of the outlet's categories for a change or a delete: menus
// being added to it wait, so none slips into a category being deleted.
// ErrCategoryNotFound if it has no such category.
func (r *CategoryRepository) Lock(ctx context.Context, outletID, categoryID string) (models.CategoryFields, error) {
	row, err := store.New(r.db.Conn(ctx)).LockCategory(ctx, store.LockCategoryParams{CategoryID: categoryID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.CategoryFields{}, apperror.ErrCategoryNotFound
	}
	if err != nil {
		return models.CategoryFields{}, fmt.Errorf("lock category %s: %w", categoryID, err)
	}
	return models.CategoryFields{Name: row.Name, Icon: row.Icon, Active: row.IsActive}, nil
}

// Hold holds one of the outlet's categories (FOR SHARE) for a menu joining
// it, so it can't be deleted until that commits: ErrCategoryNotFound if it
// has no such category.
func (r *CategoryRepository) Hold(ctx context.Context, outletID, categoryID string) error {
	_, err := store.New(r.db.Conn(ctx)).LockCategoryShared(ctx, store.LockCategorySharedParams{CategoryID: categoryID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return apperror.ErrCategoryNotFound
	}
	return err
}

// Update stores the category's name and icon: ErrCategoryNameTaken if
// another category of its outlet has the name.
func (r *CategoryRepository) Update(ctx context.Context, categoryID string, f models.CategoryFields) error {
	return categoryNameTaken(store.New(r.db.Conn(ctx)).UpdateCategory(ctx, store.UpdateCategoryParams{
		CategoryID: categoryID, Name: f.Name, Icon: f.Icon,
	}))
}

// SetActive shows or hides the category at the cashier.
func (r *CategoryRepository) SetActive(ctx context.Context, categoryID string, active bool) error {
	return store.New(r.db.Conn(ctx)).SetCategoryActive(ctx, store.SetCategoryActiveParams{CategoryID: categoryID, IsActive: active})
}

// LockIDs locks the outlet's categories and returns their IDs, in the
// cashier's order.
func (r *CategoryRepository) LockIDs(ctx context.Context, outletID string) ([]string, error) {
	ids, err := store.New(r.db.Conn(ctx)).LockCategoryIDs(ctx, outletID)
	if err != nil {
		return nil, fmt.Errorf("lock categories: %w", err)
	}
	return ids, nil
}

// SetOrder puts the outlet's categories in the order of categoryIDs.
func (r *CategoryRepository) SetOrder(ctx context.Context, outletID string, categoryIDs []string) error {
	return store.New(r.db.Conn(ctx)).SetCategoryOrder(ctx, store.SetCategoryOrderParams{OutletID: outletID, CategoryIds: categoryIDs})
}

// CountMenus returns how many menus the category holds that aren't
// deleted.
func (r *CategoryRepository) CountMenus(ctx context.Context, categoryID string) (int, error) {
	n, err := store.New(r.db.Conn(ctx)).CountCategoryMenus(ctx, categoryID)
	if err != nil {
		return 0, fmt.Errorf("count menus of category %s: %w", categoryID, err)
	}
	return int(n), nil
}

// Delete soft-deletes the category.
func (r *CategoryRepository) Delete(ctx context.Context, categoryID string) error {
	return store.New(r.db.Conn(ctx)).DeleteCategory(ctx, categoryID)
}

// ListActive returns the outlet's active categories, in the cashier's
// order, without their counts.
func (r *CategoryRepository) ListActive(ctx context.Context, outletID string) ([]models.PosCategory, error) {
	rows, err := store.New(r.db.Conn(ctx)).ListPosCategories(ctx, outletID)
	if err != nil {
		return nil, fmt.Errorf("list the cashier's categories: %w", err)
	}
	out := make([]models.PosCategory, len(rows))
	for i, row := range rows {
		out[i] = models.PosCategory{CategoryID: row.CategoryID, Name: row.Name, Icon: row.Icon}
	}
	return out, nil
}

// categoryNameTaken turns the name's unique violation into
// ErrCategoryNameTaken.
func categoryNameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "menu_categories_outlet_name_key" {
		return apperror.ErrCategoryNameTaken
	}
	return err
}

func categoryFrom(row store.GetCategoryRow) models.Category {
	return models.Category{
		CategoryID: row.CategoryID, Name: row.Name, Icon: row.Icon, SortOrder: int(row.SortOrder), Active: row.IsActive,
		MenuCount: int(row.MenuCount), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
