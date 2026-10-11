package menu

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/menu/store"
)

// What the cashier needs of the menu: what it sells, and the stock its sales
// and voids take and put back. The order module calls these.

// PosCategory is a category as the cashier shows it: active, and holding
// ItemCount of the cashier's menus.
type PosCategory struct {
	CategoryID string
	Name       string
	Icon       *string
	ItemCount  int
}

// PosCatalog returns what the outlet's cashier sells, in the order it shows
// it (the spec's Aturan tampilan menu): the active menus of active
// categories, those out of stock last and otherwise by category, then name;
// and the categories that hold them, in their order. Inactive and deleted
// menus aren't there at all.
func (s *Service) PosCatalog(ctx context.Context, outletID string) ([]PosCategory, []Menu, error) {
	q := store.New(s.pool)
	rows, err := q.ListPosMenus(ctx, outletID)
	if err != nil {
		return nil, nil, fmt.Errorf("list the cashier's menus: %w", err)
	}
	cats, err := q.ListPosCategories(ctx, outletID)
	if err != nil {
		return nil, nil, fmt.Errorf("list the cashier's categories: %w", err)
	}
	menus := make([]Menu, len(rows))
	counts := map[string]int{}
	for i, r := range rows {
		menus[i] = menu(store.GetMenuRow(r))
		counts[r.CategoryID]++
	}
	// Counted from the menus, so the two agree even when a menu changed
	// between the queries.
	categories := []PosCategory{}
	for _, c := range cats {
		if n := counts[c.CategoryID]; n > 0 {
			categories = append(categories, PosCategory{CategoryID: c.CategoryID, Name: c.Name, Icon: c.Icon, ItemCount: n})
		}
	}
	return categories, menus, nil
}

// SaleMenu is one of an order's menus as its sale finds it, locked until the
// sale commits.
type SaleMenu struct {
	MenuID     string
	Name       string
	Price      int64
	TrackStock bool
	StockQty   int
	// Available is false for a menu the cashier no longer shows: inactive,
	// or in an inactive category.
	Available bool
}

// Sold is how many of a menu an order sold.
type Sold struct {
	MenuID string
	Qty    int
}

// LockForSale locks the order's menus in the sale's transaction, in the
// order of their IDs, so two sales of the same menus never wait for each
// other in a circle and a menu's last stock is sold once. A deleted menu, or
// another outlet's, is missing from the map.
func (s *Service) LockForSale(ctx context.Context, tx pgx.Tx, outletID string, menuIDs []string) (map[string]SaleMenu, error) {
	rows, err := store.New(tx).LockMenusForSale(ctx, store.LockMenusForSaleParams{OutletID: outletID, MenuIds: menuIDs})
	if err != nil {
		return nil, fmt.Errorf("lock the menus of a sale: %w", err)
	}
	out := make(map[string]SaleMenu, len(rows))
	for _, r := range rows {
		out[r.MenuID] = SaleMenu{
			MenuID: r.MenuID, Name: r.Name, Price: r.Price, TrackStock: r.TrackStock, StockQty: int(r.StockQty),
			Available: r.Available,
		}
	}
	return out, nil
}

// RecordSale takes what the order sold off its tracked menus' stock, as
// sale movements naming the order, in the sale's transaction after
// LockForSale. The caller has checked there is enough: a sale taken online
// never leaves a count below zero.
func (s *Service) RecordSale(ctx context.Context, tx pgx.Tx, userID, outletID, orderID string, sold []Sold) error {
	return recordOrder(ctx, tx, "sale", userID, outletID, orderID, sold)
}

// RecordVoid puts back what a voided order sold, as void movements naming
// the order, for the menus still tracked. A deleted menu's stock stays as it
// was.
func (s *Service) RecordVoid(ctx context.Context, tx pgx.Tx, userID, outletID, orderID string, sold []Sold) error {
	return recordOrder(ctx, tx, "void", userID, outletID, orderID, sold)
}

// recordOrder writes an order's sale or void: one movement per tracked
// menu, for every line of it together.
func recordOrder(ctx context.Context, tx pgx.Tx, kind, userID, outletID, orderID string, sold []Sold) error {
	qty := map[string]int{}
	for _, l := range sold {
		qty[l.MenuID] += l.Qty
	}
	q := store.New(tx)
	// A sale's menus are locked already; a void's are locked here, in the
	// same order.
	rows, err := q.LockMenusForSale(ctx, store.LockMenusForSaleParams{
		OutletID: outletID, MenuIds: slices.Sorted(maps.Keys(qty)),
	})
	if err != nil {
		return fmt.Errorf("lock the menus of order %s: %w", orderID, err)
	}
	for _, r := range rows {
		if !r.TrackStock {
			continue
		}
		before := int(r.StockQty)
		after := before + qty[r.MenuID]
		if kind == "sale" {
			after = before - qty[r.MenuID]
			if after < 0 {
				return fmt.Errorf("order %s sells %d of menu %s, which has %d", orderID, qty[r.MenuID], r.MenuID, before)
			}
		}
		if err := recordMovement(ctx, q, userID, outletID, r.MenuID, movement{
			kind: kind, orderID: &orderID, before: before, after: after,
		}); err != nil {
			return err
		}
	}
	return nil
}
