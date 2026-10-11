package menu

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
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

// Limits of a menu's fields, the schema's own.
const (
	MaxPrice = 100_000_000 // Rupiah
	MaxStock = 1_000_000   // the most a count, an alert or one adjustment may be
)

// Stock conditions, as menu_stock_status computes them.
const (
	StockUntracked = "untracked" // always available
	StockOK        = "ok"
	StockLow       = "low" // at or below the alert
	StockOut       = "out" // none left
)

// Menu is one of an outlet's menus.
type Menu struct {
	MenuID      string
	Name        string
	Description *string
	SKU         *string
	Price       int64 // Rupiah
	ImageKey    *string
	TrackStock  bool
	StockQty    int
	StockAlert  *int
	StockStatus string
	Active      bool
	Category    CategorySummary
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CategorySummary is the category a menu is in.
type CategorySummary struct {
	CategoryID string
	Name       string
	Icon       *string
	Active     bool
}

// MenuInput is a menu as a Manager fills it in. InitialStock counts only
// when tracking starts: for a new tracked menu, or when TrackStock is turned
// on. Otherwise stock changes through adjustments. Active is for a new menu
// (nil: active); an existing one changes it with SetMenuActive.
type MenuInput struct {
	Name         string
	CategoryID   string
	Price        *int64
	Description  *string
	SKU          *string
	TrackStock   bool
	InitialStock *int
	StockAlert   *int
	Active       *bool
}

// MenuFilter narrows a list of menus; empty fields don't.
type MenuFilter struct {
	Search     string // part of the name or the SKU
	CategoryID string
	Status     string // active or inactive
	Stock      string // one of the Stock conditions
}

// ListMenus returns a page of the outlet's menus, in the cashier's order,
// and how many match in all.
func (s *Service) ListMenus(ctx context.Context, outletID string, f MenuFilter, page httpx.Page) ([]Menu, int, error) {
	var active *bool
	if f.Status != "" {
		a := f.Status == "active"
		active = &a
	}
	var search *string
	if q := strings.TrimSpace(f.Search); q != "" {
		escaped := likeEscaper.Replace(q)
		search = &escaped
	}
	q := store.New(s.pool)
	total, err := q.CountMenus(ctx, store.CountMenusParams{
		OutletID: outletID, CategoryID: optional(f.CategoryID), Active: active, StockStatus: optional(f.Stock), Search: search,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count menus: %w", err)
	}
	rows, err := q.ListMenus(ctx, store.ListMenusParams{
		OutletID: outletID, CategoryID: optional(f.CategoryID), Active: active, StockStatus: optional(f.Stock), Search: search,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list menus: %w", err)
	}
	out := make([]Menu, len(rows))
	for i, r := range rows {
		out[i] = menu(store.GetMenuRow(r))
	}
	return out, int(total), nil
}

// likeEscaper keeps LIKE's wildcards in a search literal.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Menu returns one of the outlet's menus.
func (s *Service) Menu(ctx context.Context, outletID, menuID string) (Menu, error) {
	r, err := store.New(s.pool).GetMenu(ctx, store.GetMenuParams{MenuID: menuID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Menu{}, ErrMenuNotFound
	}
	if err != nil {
		return Menu{}, fmt.Errorf("load menu %s: %w", menuID, err)
	}
	return menu(r), nil
}

func menu(r store.GetMenuRow) Menu {
	return Menu{
		MenuID: r.MenuID, Name: r.Name, Description: r.Description, SKU: r.Sku, Price: r.Price, ImageKey: r.ImageKey,
		TrackStock: r.TrackStock, StockQty: int(r.StockQty), StockAlert: intPtr(r.StockAlert), StockStatus: r.StockStatus,
		Active: r.IsActive, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Category: CategorySummary{CategoryID: r.CategoryID, Name: r.CategoryName, Icon: r.CategoryIcon, Active: r.CategoryActive},
	}
}

// CreateMenu adds a menu to the outlet. A tracked menu starts with its
// InitialStock, recorded as its first stock movement.
func (s *Service) CreateMenu(ctx context.Context, p authn.Principal, ip, outletID string, in MenuInput) (Menu, error) {
	next, err := checkMenu(in)
	if err != nil {
		return Menu{}, err
	}
	if next.TrackStock && in.InitialStock == nil {
		return Menu{}, httpx.Invalid(map[string]string{"initial_stock": "required"})
	}
	menuID := id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := categoryOfOutlet(ctx, q, outletID, next.CategoryID); err != nil {
			return err
		}
		if err := skuTaken(q.InsertMenu(ctx, store.InsertMenuParams{
			MenuID: menuID, OutletID: outletID, CategoryID: next.CategoryID, Name: next.Name,
			Description: next.Description, Sku: next.SKU, Price: next.Price, TrackStock: next.TrackStock,
			StockAlert: int32Ptr(next.StockAlert), IsActive: in.Active == nil || *in.Active,
		})); err != nil {
			return err
		}
		after := next.snapshot()
		if next.TrackStock {
			stock := *in.InitialStock
			if err := recordMovement(ctx, q, p.UserID, outletID, menuID, movement{kind: "initial", reason: new("initial"), after: stock}); err != nil {
				return err
			}
			after["initial_stock"] = stock
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "menu.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "menu", TargetID: menuID, After: after, IP: ip,
		})
	})
	if err != nil {
		return Menu{}, err
	}
	return s.Menu(ctx, outletID, menuID)
}

// UpdateMenu changes every field of the menu but its stock and status.
// Turning tracking on needs InitialStock, the count it starts from; turning
// it off keeps the count and its history, unused. A change of nothing
// records nothing.
func (s *Service) UpdateMenu(ctx context.Context, p authn.Principal, ip, outletID, menuID string, in MenuInput) (Menu, error) {
	next, err := checkMenu(in)
	if err != nil {
		return Menu{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockMenu(ctx, q, outletID, menuID)
		if err != nil {
			return err
		}
		starting := next.TrackStock && !prev.TrackStock
		if starting && in.InitialStock == nil {
			return httpx.Invalid(map[string]string{"initial_stock": "required"})
		}
		before, after := changes(prev.snapshot(), next.snapshot())
		if len(after) == 0 {
			return nil
		}
		if next.CategoryID != prev.CategoryID {
			if err := categoryOfOutlet(ctx, q, outletID, next.CategoryID); err != nil {
				return err
			}
		}
		if err := skuTaken(q.UpdateMenu(ctx, store.UpdateMenuParams{
			MenuID: menuID, CategoryID: next.CategoryID, Name: next.Name, Description: next.Description,
			Sku: next.SKU, Price: next.Price, TrackStock: next.TrackStock, StockAlert: int32Ptr(next.StockAlert),
		})); err != nil {
			return err
		}
		if starting {
			stock := *in.InitialStock
			if err := recordMovement(ctx, q, p.UserID, outletID, menuID, movement{kind: "initial", reason: new("initial"), before: prev.StockQty, after: stock}); err != nil {
				return err
			}
			after["initial_stock"] = stock
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "menu.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "menu", TargetID: menuID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return Menu{}, err
	}
	return s.Menu(ctx, outletID, menuID)
}

// SetMenuActive shows or hides the menu at the cashier; it stays in the
// admin area and the reports either way.
func (s *Service) SetMenuActive(ctx context.Context, p authn.Principal, ip, outletID, menuID string, active bool) (Menu, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockMenu(ctx, q, outletID, menuID)
		if err != nil {
			return err
		}
		if prev.Active == active {
			return nil
		}
		if err := q.SetMenuActive(ctx, store.SetMenuActiveParams{MenuID: menuID, IsActive: active}); err != nil {
			return err
		}
		action := "menu.deactivated"
		if active {
			action = "menu.activated"
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "menu", TargetID: menuID, IP: ip,
		})
	})
	if err != nil {
		return Menu{}, err
	}
	return s.Menu(ctx, outletID, menuID)
}

// DeleteMenu soft-deletes the menu. Sales keep its name and price as they
// were; its SKU is free again.
func (s *Service) DeleteMenu(ctx context.Context, p authn.Principal, ip, outletID, menuID string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockMenu(ctx, q, outletID, menuID)
		if err != nil {
			return err
		}
		if err := q.DeleteMenu(ctx, menuID); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "menu.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "menu", TargetID: menuID, Before: prev.snapshot(), IP: ip,
		})
	})
}

// menuFields is a menu's own fields, checked, as the Log Action stores
// them. StockQty and Active are only read from the locked row.
type menuFields struct {
	CategoryID  string
	Name        string
	Description *string
	SKU         *string
	Price       int64
	TrackStock  bool
	StockAlert  *int
	StockQty    int
	Active      bool
}

func (m menuFields) snapshot() map[string]any {
	return map[string]any{
		"category_id": m.CategoryID, "name": m.Name, "description": m.Description, "sku": m.SKU,
		"price": m.Price, "track_stock": m.TrackStock, "stock_alert": intText(m.StockAlert),
	}
}

// lockMenu locks one of the outlet's menus for a change to it or its
// stock.
func lockMenu(ctx context.Context, q *store.Queries, outletID, menuID string) (menuFields, error) {
	r, err := q.LockMenu(ctx, store.LockMenuParams{MenuID: menuID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return menuFields{}, ErrMenuNotFound
	}
	if err != nil {
		return menuFields{}, fmt.Errorf("lock menu %s: %w", menuID, err)
	}
	return menuFields{
		CategoryID: r.CategoryID, Name: r.Name, Description: r.Description, SKU: r.Sku, Price: r.Price,
		TrackStock: r.TrackStock, StockAlert: intPtr(r.StockAlert), StockQty: int(r.StockQty), Active: r.IsActive,
	}, nil
}

// categoryOfOutlet checks that the category is the outlet's and not
// deleted, and holds it (FOR SHARE) so it can't be deleted until the menu
// is saved.
func categoryOfOutlet(ctx context.Context, q *store.Queries, outletID, categoryID string) error {
	_, err := q.LockCategoryShared(ctx, store.LockCategorySharedParams{CategoryID: categoryID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.Invalid(map[string]string{"category_id": "exists"})
	}
	return err
}

// skuPattern is the menus_sku_check constraint's rule.
var skuPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// checkMenu validates a menu as filled in: every problem at once as
// validation_failed. An untracked menu has no alert.
func checkMenu(in MenuInput) (menuFields, error) {
	fields := map[string]string{}
	m := menuFields{
		CategoryID: strings.TrimSpace(in.CategoryID), Name: strings.TrimSpace(in.Name),
		Description: trimmed(in.Description), SKU: trimmed(in.SKU), TrackStock: in.TrackStock,
	}
	switch n := utf8.RuneCountInString(m.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 100:
		fields["name"] = "max"
	}
	if m.CategoryID == "" {
		fields["category_id"] = "required"
	}
	switch {
	case in.Price == nil:
		fields["price"] = "required"
	case *in.Price < 0 || *in.Price > MaxPrice:
		fields["price"] = "range"
	default:
		m.Price = *in.Price
	}
	if m.Description != nil && utf8.RuneCountInString(*m.Description) > 500 {
		fields["description"] = "max"
	}
	if m.SKU != nil && !skuPattern.MatchString(*m.SKU) {
		fields["sku"] = "format"
	}
	if in.InitialStock != nil && (*in.InitialStock < 0 || *in.InitialStock > MaxStock) {
		fields["initial_stock"] = "range"
	}
	if m.TrackStock && in.StockAlert != nil {
		if *in.StockAlert < 0 || *in.StockAlert > MaxStock {
			fields["stock_alert"] = "range"
		}
		m.StockAlert = in.StockAlert
	}
	if len(fields) > 0 {
		return menuFields{}, httpx.Invalid(fields)
	}
	return m, nil
}

// skuTaken turns the SKU's unique violation into ErrMenuSKUTaken.
func skuTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "menus_outlet_sku_key" {
		return ErrMenuSKUTaken
	}
	return err
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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

// intText shows an optional number in the Log Action as the number or null.
func intText(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
