package menu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/menu/store"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
)

// Category is one of an outlet's menu categories.
type Category struct {
	CategoryID string
	Name       string
	Icon       *string // an emoji
	SortOrder  int
	Active     bool
	MenuCount  int // menus in it that aren't deleted
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CategoryInput is a category as a Manager fills it in.
type CategoryInput struct {
	Name string
	Icon *string
}

// ListCategories returns a page of the outlet's categories, in the order
// the cashier shows them, and how many there are in all.
func (s *Service) ListCategories(ctx context.Context, outletID string, page httpx.Page) ([]Category, int, error) {
	q := store.New(s.pool)
	total, err := q.CountCategories(ctx, outletID)
	if err != nil {
		return nil, 0, fmt.Errorf("count categories: %w", err)
	}
	rows, err := q.ListCategories(ctx, store.ListCategoriesParams{
		OutletID:   outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list categories: %w", err)
	}
	out := make([]Category, len(rows))
	for i, r := range rows {
		out[i] = category(store.GetCategoryRow(r))
	}
	return out, int(total), nil
}

// Category returns one of the outlet's categories.
func (s *Service) Category(ctx context.Context, outletID, categoryID string) (Category, error) {
	r, err := store.New(s.pool).GetCategory(ctx, store.GetCategoryParams{CategoryID: categoryID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Category{}, ErrCategoryNotFound
	}
	if err != nil {
		return Category{}, fmt.Errorf("load category %s: %w", categoryID, err)
	}
	return category(r), nil
}

func category(r store.GetCategoryRow) Category {
	return Category{
		CategoryID: r.CategoryID, Name: r.Name, Icon: r.Icon, SortOrder: int(r.SortOrder), Active: r.IsActive,
		MenuCount: int(r.MenuCount), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// CreateCategory adds a category to the outlet, last in the order.
func (s *Service) CreateCategory(ctx context.Context, p authn.Principal, ip, outletID string, in CategoryInput) (Category, error) {
	next, err := checkCategory(in)
	if err != nil {
		return Category{}, err
	}
	categoryID := id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		order, err := q.NextCategoryOrder(ctx, outletID)
		if err != nil {
			return fmt.Errorf("order of a new category: %w", err)
		}
		if err := categoryNameTaken(q.InsertCategory(ctx, store.InsertCategoryParams{
			CategoryID: categoryID, OutletID: outletID, Name: next.Name, Icon: next.Icon, SortOrder: order,
		})); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "category.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "category", TargetID: categoryID, After: next.snapshot(), IP: ip,
		})
	})
	if err != nil {
		return Category{}, err
	}
	return s.Category(ctx, outletID, categoryID)
}

// UpdateCategory changes a category's name and icon. A change of nothing
// records nothing.
func (s *Service) UpdateCategory(ctx context.Context, p authn.Principal, ip, outletID, categoryID string, in CategoryInput) (Category, error) {
	next, err := checkCategory(in)
	if err != nil {
		return Category{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockCategory(ctx, q, outletID, categoryID)
		if err != nil {
			return err
		}
		before, after := changes(prev.snapshot(), next.snapshot())
		if len(after) == 0 {
			return nil
		}
		if err := categoryNameTaken(q.UpdateCategory(ctx, store.UpdateCategoryParams{
			CategoryID: categoryID, Name: next.Name, Icon: next.Icon,
		})); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "category.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "category", TargetID: categoryID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return Category{}, err
	}
	return s.Category(ctx, outletID, categoryID)
}

// SetCategoryActive shows or hides the category, and its menus, at the
// cashier. Its menus stay in the admin area either way.
func (s *Service) SetCategoryActive(ctx context.Context, p authn.Principal, ip, outletID, categoryID string, active bool) (Category, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockCategory(ctx, q, outletID, categoryID)
		if err != nil {
			return err
		}
		if prev.Active == active {
			return nil
		}
		if err := q.SetCategoryActive(ctx, store.SetCategoryActiveParams{CategoryID: categoryID, IsActive: active}); err != nil {
			return err
		}
		action := "category.deactivated"
		if active {
			action = "category.activated"
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "category", TargetID: categoryID, IP: ip,
		})
	})
	if err != nil {
		return Category{}, err
	}
	return s.Category(ctx, outletID, categoryID)
}

// ReorderCategories puts the outlet's categories in the order of
// categoryIDs, which must list each of them once: otherwise
// ErrCategoriesChanged, since the caller's list is out of date.
func (s *Service) ReorderCategories(ctx context.Context, p authn.Principal, ip, outletID string, categoryIDs []string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockCategoryIDs(ctx, outletID)
		if err != nil {
			return fmt.Errorf("lock categories: %w", err)
		}
		if !sameSet(current, categoryIDs) {
			return ErrCategoriesChanged
		}
		if slices.Equal(current, categoryIDs) {
			return nil
		}
		if err := q.SetCategoryOrder(ctx, store.SetCategoryOrderParams{OutletID: outletID, CategoryIds: categoryIDs}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "category.reordered", UserID: p.UserID, OutletID: outletID, TargetType: "outlet", TargetID: outletID,
			Before: map[string]any{"category_ids": current}, After: map[string]any{"category_ids": categoryIDs}, IP: ip,
		})
	})
}

// DeleteCategory soft-deletes a category that holds no menu.
func (s *Service) DeleteCategory(ctx context.Context, p authn.Principal, ip, outletID, categoryID string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockCategory(ctx, q, outletID, categoryID)
		if err != nil {
			return err
		}
		menus, err := q.CountCategoryMenus(ctx, categoryID)
		if err != nil {
			return fmt.Errorf("count menus of category %s: %w", categoryID, err)
		}
		if menus > 0 {
			return ErrCategoryInUse.WithDetails(map[string]any{"menus": menus})
		}
		if err := q.DeleteCategory(ctx, categoryID); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "category.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "category", TargetID: categoryID, Before: prev.snapshot(), IP: ip,
		})
	})
}

// categoryFields is a category's own fields, checked, as the Log Action
// stores them.
type categoryFields struct {
	Name   string
	Icon   *string
	Active bool
}

func (c categoryFields) snapshot() map[string]any {
	return map[string]any{"name": c.Name, "icon": c.Icon}
}

// lockCategory locks one of the outlet's categories for a change.
func lockCategory(ctx context.Context, q *store.Queries, outletID, categoryID string) (categoryFields, error) {
	r, err := q.LockCategory(ctx, store.LockCategoryParams{CategoryID: categoryID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return categoryFields{}, ErrCategoryNotFound
	}
	if err != nil {
		return categoryFields{}, fmt.Errorf("lock category %s: %w", categoryID, err)
	}
	return categoryFields{Name: r.Name, Icon: r.Icon, Active: r.IsActive}, nil
}

// checkCategory validates a category as filled in: every problem at once as
// validation_failed.
func checkCategory(in CategoryInput) (categoryFields, error) {
	fields := map[string]string{}
	c := categoryFields{Name: strings.TrimSpace(in.Name), Icon: trimmed(in.Icon)}
	switch n := utf8.RuneCountInString(c.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 50:
		fields["name"] = "max"
	}
	if c.Icon != nil {
		switch {
		case utf8.RuneCountInString(*c.Icon) > 8:
			fields["icon"] = "max"
		case !emojiLike(*c.Icon):
			fields["icon"] = "format"
		}
	}
	if len(fields) > 0 {
		return categoryFields{}, httpx.Invalid(fields)
	}
	return c, nil
}

// emojiLike reports whether s is a pictograph rather than a word: at least
// one pictographic symbol, or a keycap as in 1️⃣, and no letters, spaces or
// control characters. Emoji are such symbols, joined by zero-width joiners
// and variation selectors; "??" or "12" isn't one.
func emojiLike(s string) bool {
	pictograph := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsSpace(r) || unicode.IsControl(r):
			return false
		case unicode.Is(unicode.So, r) || r == '\u20E3':
			pictograph = true
		}
	}
	return pictograph
}

// sameSet reports whether want lists every ID in have exactly once. The IDs
// in have are distinct: they're primary keys.
func sameSet(have, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(have)), slices.Sorted(slices.Values(want)))
}

// categoryNameTaken turns the name's unique violation into
// ErrCategoryNameTaken.
func categoryNameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "menu_categories_outlet_name_key" {
		return ErrCategoryNameTaken
	}
	return err
}
