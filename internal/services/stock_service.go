package services

import (
	"context"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// StockService is the stock of each outlet's tracked menus: the
// adjustments people make, the alert at which a count is low, and the
// history of every change, which sales and voids write too.
type StockService struct {
	db    *config.Database
	menus *repositories.MenuRepository
	stock *repositories.StockRepository
	users *UserService
	audit *AuditService
}

// NewStockService returns the stock's service. users names who changed the
// stock.
func NewStockService(db *config.Database, menus *repositories.MenuRepository, stock *repositories.StockRepository,
	users *UserService, audit *AuditService,
) *StockService {
	return &StockService{db: db, menus: menus, stock: stock, users: users, audit: audit}
}

// AdjustStock changes a tracked menu's count, one adjustment at a time. The
// count never goes below zero this way (ErrStockInsufficient, with what is
// there) nor above MaxStock; an untracked menu has no count to change
// (ErrStockNotTracked). A stock take that finds the count as it was is
// recorded too.
func (s *StockService) AdjustStock(ctx context.Context, p models.Principal, ip, outletID, menuID string, in models.StockAdjustment) (models.Menu, error) {
	a, err := validations.CheckStockAdjustment(in)
	if err != nil {
		return models.Menu{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		m, err := s.menus.Lock(ctx, outletID, menuID)
		if err != nil {
			return err
		}
		if !m.TrackStock {
			return apperror.ErrStockNotTracked
		}
		after := a.Qty
		switch a.Type {
		case models.AdjustIn:
			after = m.StockQty + a.Qty
		case models.AdjustOut:
			after = m.StockQty - a.Qty
		}
		switch {
		case after < 0:
			return apperror.ErrStockInsufficient.WithDetails(map[string]any{"available": max(m.StockQty, 0)})
		case after > models.MaxStock:
			return apperror.Invalid(map[string]string{"qty": "range"})
		}
		if err := recordMovement(ctx, s.menus, s.stock, models.NewStockMovement{
			OutletID: outletID, MenuID: menuID, UserID: p.UserID, Type: a.Type, Reason: &a.Reason, Note: a.Note,
			Before: m.StockQty, After: after,
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "stock.adjusted", UserID: p.UserID, OutletID: outletID, TargetType: "menu", TargetID: menuID,
			Before: map[string]any{"stock_qty": m.StockQty},
			After:  map[string]any{"stock_qty": after, "type": a.Type, "reason": a.Reason, "note": a.Note},
			IP:     ip,
		})
	})
	if err != nil {
		return models.Menu{}, err
	}
	return s.menus.Get(ctx, outletID, menuID)
}

// SetStockAlert changes the count at or below which a tracked menu's stock
// is low; nil or 0 turns the alert off. An untracked menu has no alert
// (ErrStockNotTracked). A change of nothing records nothing.
func (s *StockService) SetStockAlert(ctx context.Context, p models.Principal, ip, outletID, menuID string, alert *int) (models.Menu, error) {
	if err := validations.CheckStockAlert(alert); err != nil {
		return models.Menu{}, err
	}
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.menus.Lock(ctx, outletID, menuID)
		if err != nil {
			return err
		}
		if !prev.TrackStock {
			return apperror.ErrStockNotTracked
		}
		before, after := models.NullableInt(prev.StockAlert), models.NullableInt(alert)
		if before == after {
			return nil
		}
		if err := s.menus.SetStockAlert(ctx, menuID, alert); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "menu.updated", UserID: p.UserID, OutletID: outletID, TargetType: "menu", TargetID: menuID,
			Before: map[string]any{"stock_alert": before}, After: map[string]any{"stock_alert": after}, IP: ip,
		})
	})
	if err != nil {
		return models.Menu{}, err
	}
	return s.menus.Get(ctx, outletID, menuID)
}

// StockMovements returns a page of the menu's stock history, newest first,
// each naming who made it, and how many there are in all. A menu no longer
// tracked keeps its history.
func (s *StockService) StockMovements(ctx context.Context, outletID, menuID string, page pagination.Page) ([]models.StockMovement, int, error) {
	if _, err := s.menus.Get(ctx, outletID, menuID); err != nil {
		return nil, 0, err
	}
	out, total, err := s.stock.List(ctx, outletID, menuID, page)
	if err != nil {
		return nil, 0, err
	}
	userIDs := make([]string, len(out))
	for i, m := range out {
		userIDs[i] = m.UserID
	}
	names, err := s.users.Names(ctx, userIDs)
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		out[i].UserName = names[out[i].UserID]
	}
	return out, total, nil
}

// recordMovement writes the movement and the menu's new count in the
// transaction ctx carries, where the menu is locked: menus.stock_qty is
// always the last movement's stock_after.
func recordMovement(ctx context.Context, menus *repositories.MenuRepository, stock *repositories.StockRepository, m models.NewStockMovement) error {
	if err := stock.Insert(ctx, m); err != nil {
		return err
	}
	return menus.SetStock(ctx, m.MenuID, m.After)
}
