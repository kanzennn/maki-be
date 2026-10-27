package services

import (
	"context"
	"slices"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// CategoryService is each outlet's menu categories, in the order its
// cashier shows them. Every method takes the outlet it acts in; the routes
// have checked the caller may act there.
type CategoryService struct {
	db         *config.Database
	categories *repositories.CategoryRepository
	audit      *AuditService
}

// NewCategoryService returns the categories' service.
func NewCategoryService(db *config.Database, categories *repositories.CategoryRepository, audit *AuditService) *CategoryService {
	return &CategoryService{db: db, categories: categories, audit: audit}
}

// ListCategories returns a page of the outlet's categories, in the order
// the cashier shows them, and how many there are in all.
func (s *CategoryService) ListCategories(ctx context.Context, outletID string, page pagination.Page) ([]models.Category, int, error) {
	return s.categories.List(ctx, outletID, page)
}

// Category returns one of the outlet's categories.
func (s *CategoryService) Category(ctx context.Context, outletID, categoryID string) (models.Category, error) {
	return s.categories.Get(ctx, outletID, categoryID)
}

// CreateCategory adds a category to the outlet, last in the order.
func (s *CategoryService) CreateCategory(ctx context.Context, p models.Principal, ip, outletID string, in models.CategoryInput) (models.Category, error) {
	next, err := validations.CheckCategory(in)
	if err != nil {
		return models.Category{}, err
	}
	categoryID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		order, err := s.categories.NextOrder(ctx, outletID)
		if err != nil {
			return err
		}
		if err := s.categories.Insert(ctx, outletID, categoryID, next, order); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "category.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "category", TargetID: categoryID, After: next.Snapshot(), IP: ip,
		})
	})
	if err != nil {
		return models.Category{}, err
	}
	return s.Category(ctx, outletID, categoryID)
}

// UpdateCategory changes a category's name and icon. A change of nothing
// records nothing.
func (s *CategoryService) UpdateCategory(ctx context.Context, p models.Principal, ip, outletID, categoryID string, in models.CategoryInput) (models.Category, error) {
	next, err := validations.CheckCategory(in)
	if err != nil {
		return models.Category{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.categories.Lock(ctx, outletID, categoryID)
		if err != nil {
			return err
		}
		before, after := changedAsPrinted(prev.Snapshot(), next.Snapshot())
		if len(after) == 0 {
			return nil
		}
		if err := s.categories.Update(ctx, categoryID, next); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "category.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "category", TargetID: categoryID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Category{}, err
	}
	return s.Category(ctx, outletID, categoryID)
}

// SetCategoryActive shows or hides the category, and its menus, at the
// cashier. Its menus stay in the admin area either way.
func (s *CategoryService) SetCategoryActive(ctx context.Context, p models.Principal, ip, outletID, categoryID string, active bool) (models.Category, error) {
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.categories.Lock(ctx, outletID, categoryID)
		if err != nil {
			return err
		}
		if prev.Active == active {
			return nil
		}
		if err := s.categories.SetActive(ctx, categoryID, active); err != nil {
			return err
		}
		action := "category.deactivated"
		if active {
			action = "category.activated"
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "category", TargetID: categoryID, IP: ip,
		})
	})
	if err != nil {
		return models.Category{}, err
	}
	return s.Category(ctx, outletID, categoryID)
}

// ReorderCategories puts the outlet's categories in the order of
// categoryIDs, which must list each of them once: otherwise
// ErrCategoriesChanged, since the caller's list is out of date.
func (s *CategoryService) ReorderCategories(ctx context.Context, p models.Principal, ip, outletID string, categoryIDs []string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		current, err := s.categories.LockIDs(ctx, outletID)
		if err != nil {
			return err
		}
		if !sameSet(current, categoryIDs) {
			return apperror.ErrCategoriesChanged
		}
		if slices.Equal(current, categoryIDs) {
			return nil
		}
		if err := s.categories.SetOrder(ctx, outletID, categoryIDs); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "category.reordered", UserID: p.UserID, OutletID: outletID, TargetType: "outlet", TargetID: outletID,
			Before: map[string]any{"category_ids": current}, After: map[string]any{"category_ids": categoryIDs}, IP: ip,
		})
	})
}

// DeleteCategory soft-deletes a category that holds no menu.
func (s *CategoryService) DeleteCategory(ctx context.Context, p models.Principal, ip, outletID, categoryID string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.categories.Lock(ctx, outletID, categoryID)
		if err != nil {
			return err
		}
		menus, err := s.categories.CountMenus(ctx, categoryID)
		if err != nil {
			return err
		}
		if menus > 0 {
			return apperror.ErrCategoryInUse.WithDetails(map[string]any{"menus": menus})
		}
		if err := s.categories.Delete(ctx, categoryID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "category.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "category", TargetID: categoryID, Before: prev.Snapshot(), IP: ip,
		})
	})
}

// sameSet reports whether want lists every ID in have exactly once. The IDs
// in have are distinct: they're primary keys.
func sameSet(have, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(have)), slices.Sorted(slices.Values(want)))
}
