package services

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// MenuService is each outlet's menus, and the cashier's side of them: what
// it sells, and the stock its sales take and its voids put back, which the
// checkout and the orders ask for. Every method takes the outlet it acts
// in; the routes have checked the caller may act there.
type MenuService struct {
	db         *config.Database
	menus      *repositories.MenuRepository
	categories *repositories.CategoryRepository
	stock      *repositories.StockRepository
	audit      *AuditService
}

// NewMenuService returns the menus' service.
func NewMenuService(db *config.Database, menus *repositories.MenuRepository, categories *repositories.CategoryRepository,
	stock *repositories.StockRepository, audit *AuditService,
) *MenuService {
	return &MenuService{db: db, menus: menus, categories: categories, stock: stock, audit: audit}
}

// ListMenus returns a page of the outlet's menus, in the cashier's order,
// and how many match in all.
func (s *MenuService) ListMenus(ctx context.Context, outletID string, f models.MenuFilter, page pagination.Page) ([]models.Menu, int, error) {
	return s.menus.List(ctx, outletID, f, page)
}

// Menu returns one of the outlet's menus.
func (s *MenuService) Menu(ctx context.Context, outletID, menuID string) (models.Menu, error) {
	return s.menus.Get(ctx, outletID, menuID)
}

// CreateMenu adds a menu to the outlet. A tracked menu starts with its
// InitialStock, recorded as its first stock movement.
func (s *MenuService) CreateMenu(ctx context.Context, p models.Principal, ip, outletID string, in models.MenuInput) (models.Menu, error) {
	next, err := validations.CheckMenu(in)
	if err != nil {
		return models.Menu{}, err
	}
	if next.TrackStock && in.InitialStock == nil {
		return models.Menu{}, apperror.Invalid(map[string]string{"initial_stock": "required"})
	}
	menuID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.categoryOfOutlet(ctx, outletID, next.CategoryID); err != nil {
			return err
		}
		if err := s.menus.Insert(ctx, outletID, menuID, next, in.Active == nil || *in.Active); err != nil {
			return err
		}
		after := next.Snapshot()
		if next.TrackStock {
			stock := *in.InitialStock
			if err := recordMovement(ctx, s.menus, s.stock, models.NewStockMovement{
				OutletID: outletID, MenuID: menuID, UserID: p.UserID, Type: "initial", Reason: new("initial"), After: stock,
			}); err != nil {
				return err
			}
			after["initial_stock"] = stock
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "menu.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "menu", TargetID: menuID, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Menu{}, err
	}
	return s.Menu(ctx, outletID, menuID)
}

// UpdateMenu changes every field of the menu but its stock and status.
// Turning tracking on needs InitialStock, the count it starts from; turning
// it off keeps the count and its history, unused. A change of nothing
// records nothing.
func (s *MenuService) UpdateMenu(ctx context.Context, p models.Principal, ip, outletID, menuID string, in models.MenuInput) (models.Menu, error) {
	next, err := validations.CheckMenu(in)
	if err != nil {
		return models.Menu{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.menus.Lock(ctx, outletID, menuID)
		if err != nil {
			return err
		}
		starting := next.TrackStock && !prev.TrackStock
		if starting && in.InitialStock == nil {
			return apperror.Invalid(map[string]string{"initial_stock": "required"})
		}
		before, after := changedAsPrinted(prev.Snapshot(), next.Snapshot())
		if len(after) == 0 {
			return nil
		}
		if next.CategoryID != prev.CategoryID {
			if err := s.categoryOfOutlet(ctx, outletID, next.CategoryID); err != nil {
				return err
			}
		}
		if err := s.menus.Update(ctx, menuID, next); err != nil {
			return err
		}
		if starting {
			stock := *in.InitialStock
			if err := recordMovement(ctx, s.menus, s.stock, models.NewStockMovement{
				OutletID: outletID, MenuID: menuID, UserID: p.UserID, Type: "initial", Reason: new("initial"),
				Before: prev.StockQty, After: stock,
			}); err != nil {
				return err
			}
			after["initial_stock"] = stock
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "menu.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "menu", TargetID: menuID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Menu{}, err
	}
	return s.Menu(ctx, outletID, menuID)
}

// SetMenuActive shows or hides the menu at the cashier; it stays in the
// admin area and the reports either way.
func (s *MenuService) SetMenuActive(ctx context.Context, p models.Principal, ip, outletID, menuID string, active bool) (models.Menu, error) {
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.menus.Lock(ctx, outletID, menuID)
		if err != nil {
			return err
		}
		if prev.Active == active {
			return nil
		}
		if err := s.menus.SetActive(ctx, menuID, active); err != nil {
			return err
		}
		action := "menu.deactivated"
		if active {
			action = "menu.activated"
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "menu", TargetID: menuID, IP: ip,
		})
	})
	if err != nil {
		return models.Menu{}, err
	}
	return s.Menu(ctx, outletID, menuID)
}

// DeleteMenu soft-deletes the menu. Sales keep its name and price as they
// were; its SKU is free again.
func (s *MenuService) DeleteMenu(ctx context.Context, p models.Principal, ip, outletID, menuID string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.menus.Lock(ctx, outletID, menuID)
		if err != nil {
			return err
		}
		if err := s.menus.Delete(ctx, menuID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "menu.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "menu", TargetID: menuID, Before: prev.Snapshot(), IP: ip,
		})
	})
}

// categoryOfOutlet checks that the category is the outlet's and not
// deleted, and holds it so it can't be deleted until the menu is saved.
func (s *MenuService) categoryOfOutlet(ctx context.Context, outletID, categoryID string) error {
	err := s.categories.Hold(ctx, outletID, categoryID)
	if errors.Is(err, apperror.ErrCategoryNotFound) {
		return apperror.Invalid(map[string]string{"category_id": "exists"})
	}
	return err
}

// PosCatalog returns what the outlet's cashier sells, in the order it shows
// it (the spec's Aturan tampilan menu): the active menus of active
// categories, those out of stock last and otherwise by category, then name;
// and the categories that hold them, in their order. Inactive and deleted
// menus aren't there at all.
func (s *MenuService) PosCatalog(ctx context.Context, outletID string) ([]models.PosCategory, []models.Menu, error) {
	menus, err := s.menus.ListForCashier(ctx, outletID)
	if err != nil {
		return nil, nil, err
	}
	active, err := s.categories.ListActive(ctx, outletID)
	if err != nil {
		return nil, nil, err
	}
	counts := map[string]int{}
	for _, m := range menus {
		counts[m.Category.CategoryID]++
	}
	// Counted from the menus, so the two agree even when a menu changed
	// between the queries.
	categories := []models.PosCategory{}
	for _, c := range active {
		if n := counts[c.CategoryID]; n > 0 {
			c.ItemCount = n
			categories = append(categories, c)
		}
	}
	return categories, menus, nil
}

// LockForSale locks the order's menus in the sale's transaction, the one
// ctx carries, in the order of their IDs, so two sales of the same menus
// never wait for each other in a circle and a menu's last stock is sold
// once. A deleted menu, or another outlet's, is missing from the map.
func (s *MenuService) LockForSale(ctx context.Context, outletID string, menuIDs []string) (map[string]models.SaleMenu, error) {
	menus, err := s.menus.LockForSale(ctx, outletID, menuIDs)
	if err != nil {
		return nil, fmt.Errorf("lock the menus of a sale: %w", err)
	}
	out := make(map[string]models.SaleMenu, len(menus))
	for _, m := range menus {
		out[m.MenuID] = m
	}
	return out, nil
}

// RecordSale takes what the order sold off its tracked menus' stock, as
// sale movements naming the order, in the sale's transaction after
// LockForSale. The caller has checked there is enough: a sale taken online
// never leaves a count below zero.
func (s *MenuService) RecordSale(ctx context.Context, userID, outletID, orderID string, sold []models.Sold) error {
	return s.recordOrder(ctx, "sale", userID, outletID, orderID, sold)
}

// RecordVoid puts back what a voided order sold, as void movements naming
// the order, for the menus still tracked, in the void's transaction. A
// deleted menu's stock stays as it was.
func (s *MenuService) RecordVoid(ctx context.Context, userID, outletID, orderID string, sold []models.Sold) error {
	return s.recordOrder(ctx, "void", userID, outletID, orderID, sold)
}

// recordOrder writes an order's sale or void: one movement per tracked
// menu, for every line of it together.
func (s *MenuService) recordOrder(ctx context.Context, kind, userID, outletID, orderID string, sold []models.Sold) error {
	qty := map[string]int{}
	for _, l := range sold {
		qty[l.MenuID] += l.Qty
	}
	// A sale's menus are locked already; a void's are locked here, in the
	// same order.
	menus, err := s.menus.LockForSale(ctx, outletID, slices.Sorted(maps.Keys(qty)))
	if err != nil {
		return fmt.Errorf("lock the menus of order %s: %w", orderID, err)
	}
	for _, m := range menus {
		if !m.TrackStock {
			continue
		}
		before := m.StockQty
		after := before + qty[m.MenuID]
		if kind == "sale" {
			after = before - qty[m.MenuID]
			if after < 0 {
				return fmt.Errorf("order %s sells %d of menu %s, which has %d", orderID, qty[m.MenuID], m.MenuID, before)
			}
		}
		if err := recordMovement(ctx, s.menus, s.stock, models.NewStockMovement{
			OutletID: outletID, MenuID: m.MenuID, UserID: userID, Type: kind, OrderID: &orderID, Before: before, After: after,
		}); err != nil {
			return err
		}
	}
	return nil
}
