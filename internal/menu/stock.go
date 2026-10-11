package menu

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/menu/store"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
)

// Kinds of stock adjustment.
const (
	AdjustIn  = "in"  // stock added
	AdjustOut = "out" // stock taken away
	AdjustSet = "set" // a stock take: the count found
)

// adjustReasons are the reasons an adjustment gives. Other says what in
// its note.
var adjustReasons = []string{"restock", "damaged", "correction", "other"}

// Adjustment is a change a person makes to a tracked menu's count: Qty
// added (in) or taken away (out), or the count found (set). Reason is one
// of restock, damaged, correction or other.
type Adjustment struct {
	Type   string
	Qty    *int
	Reason string
	Note   *string
}

// StockMovement is one entry of a menu's stock history.
type StockMovement struct {
	StockMovementID string
	Type            string // initial, in, out, set, sale or void
	QtyChange       int
	StockAfter      int
	Reason          *string
	Note            *string
	UserID          string
	UserName        string
	OrderID         *string // a sale's or void's
	CreatedAt       time.Time
}

// AdjustStock changes a tracked menu's count, one adjustment at a time. The
// count never goes below zero this way (ErrStockInsufficient, with what is
// there) nor above MaxStock; an untracked menu has no count to change
// (ErrStockNotTracked). A stock take that finds the count as it was is
// recorded too.
func (s *Service) AdjustStock(ctx context.Context, p authn.Principal, ip, outletID, menuID string, in Adjustment) (Menu, error) {
	a, err := checkAdjustment(in)
	if err != nil {
		return Menu{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		m, err := lockMenu(ctx, q, outletID, menuID)
		if err != nil {
			return err
		}
		if !m.TrackStock {
			return ErrStockNotTracked
		}
		after := a.qty
		switch a.kind {
		case AdjustIn:
			after = m.StockQty + a.qty
		case AdjustOut:
			after = m.StockQty - a.qty
		}
		switch {
		case after < 0:
			return ErrStockInsufficient.WithDetails(map[string]any{"available": max(m.StockQty, 0)})
		case after > MaxStock:
			return httpx.Invalid(map[string]string{"qty": "range"})
		}
		if err := recordMovement(ctx, q, p.UserID, outletID, menuID, movement{
			kind: a.kind, reason: &a.reason, note: a.note, before: m.StockQty, after: after,
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "stock.adjusted", UserID: p.UserID, OutletID: outletID, TargetType: "menu", TargetID: menuID,
			Before: map[string]any{"stock_qty": m.StockQty},
			After:  map[string]any{"stock_qty": after, "type": a.kind, "reason": a.reason, "note": a.note},
			IP:     ip,
		})
	})
	if err != nil {
		return Menu{}, err
	}
	return s.Menu(ctx, outletID, menuID)
}

// SetStockAlert changes the count at or below which a tracked menu's stock
// is low; nil or 0 turns the alert off. An untracked menu has no alert
// (ErrStockNotTracked). A change of nothing records nothing.
func (s *Service) SetStockAlert(ctx context.Context, p authn.Principal, ip, outletID, menuID string, alert *int) (Menu, error) {
	if alert != nil && (*alert < 0 || *alert > MaxStock) {
		return Menu{}, httpx.Invalid(map[string]string{"stock_alert": "range"})
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockMenu(ctx, q, outletID, menuID)
		if err != nil {
			return err
		}
		if !prev.TrackStock {
			return ErrStockNotTracked
		}
		before, after := intText(prev.StockAlert), intText(alert)
		if before == after {
			return nil
		}
		if err := q.SetMenuStockAlert(ctx, store.SetMenuStockAlertParams{MenuID: menuID, StockAlert: int32Ptr(alert)}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "menu.updated", UserID: p.UserID, OutletID: outletID, TargetType: "menu", TargetID: menuID,
			Before: map[string]any{"stock_alert": before}, After: map[string]any{"stock_alert": after}, IP: ip,
		})
	})
	if err != nil {
		return Menu{}, err
	}
	return s.Menu(ctx, outletID, menuID)
}

// StockMovements returns a page of the menu's stock history, newest first,
// each naming who made it, and how many there are in all. A menu no longer
// tracked keeps its history.
func (s *Service) StockMovements(ctx context.Context, outletID, menuID string, page httpx.Page) ([]StockMovement, int, error) {
	if _, err := s.Menu(ctx, outletID, menuID); err != nil {
		return nil, 0, err
	}
	q := store.New(s.pool)
	total, err := q.CountStockMovements(ctx, store.CountStockMovementsParams{MenuID: menuID, OutletID: outletID})
	if err != nil {
		return nil, 0, fmt.Errorf("count stock movements: %w", err)
	}
	rows, err := q.ListStockMovements(ctx, store.ListStockMovementsParams{
		MenuID: menuID, OutletID: outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list stock movements: %w", err)
	}
	userIDs := make([]string, len(rows))
	for i, r := range rows {
		userIDs[i] = r.UserID
	}
	names, err := s.users.Names(ctx, userIDs)
	if err != nil {
		return nil, 0, err
	}
	out := make([]StockMovement, len(rows))
	for i, r := range rows {
		out[i] = StockMovement{
			StockMovementID: r.StockMovementID, Type: r.Type, QtyChange: int(r.QtyChange), StockAfter: int(r.StockAfter),
			Reason: r.Reason, Note: r.Note, UserID: r.UserID, UserName: names[r.UserID], OrderID: r.OrderID,
			CreatedAt: r.CreatedAt,
		}
	}
	return out, int(total), nil
}

// adjustment is an Adjustment, checked.
type adjustment struct {
	kind, reason string
	qty          int
	note         *string
}

// checkAdjustment validates an adjustment as filled in: every problem at
// once as validation_failed. Only a stock take may count 0, and a reason of
// other needs a note saying what it was.
func checkAdjustment(in Adjustment) (adjustment, error) {
	fields := map[string]string{}
	a := adjustment{kind: in.Type, reason: in.Reason, note: trimmed(in.Note)}
	least := 1
	switch in.Type {
	case AdjustIn, AdjustOut:
	case AdjustSet:
		least = 0
	default:
		fields["type"] = "oneof"
	}
	switch {
	case in.Qty == nil:
		fields["qty"] = "required"
	case *in.Qty < least || *in.Qty > MaxStock:
		fields["qty"] = "range"
	default:
		a.qty = *in.Qty
	}
	if !slices.Contains(adjustReasons, in.Reason) {
		fields["reason"] = "oneof"
	}
	switch {
	case a.note != nil && utf8.RuneCountInString(*a.note) > 200:
		fields["note"] = "max"
	case a.note == nil && in.Reason == "other":
		fields["note"] = "required"
	}
	if len(fields) > 0 {
		return adjustment{}, httpx.Invalid(fields)
	}
	return a, nil
}

// movement is one change to a tracked menu's stock, from before to after.
type movement struct {
	kind    string  // initial, in, out, set, sale or void
	reason  *string // an adjustment's reason, or initial; a sale or void has none
	note    *string
	orderID *string // a sale's or void's
	before  int
	after   int
}

// recordMovement writes the movement and the menu's new count in the
// caller's transaction, where the menu is locked: menus.stock_qty is always
// the last movement's stock_after.
func recordMovement(ctx context.Context, q *store.Queries, userID, outletID, menuID string, m movement) error {
	if err := q.InsertStockMovement(ctx, store.InsertStockMovementParams{
		StockMovementID: id.New(), OutletID: outletID, MenuID: menuID, Type: m.kind,
		QtyChange: int32(m.after - m.before), StockAfter: int32(m.after), //nolint:gosec // a count stays near MaxStock
		Reason: m.reason, Note: m.note, UserID: userID, OrderID: m.orderID,
	}); err != nil {
		return fmt.Errorf("record stock movement of menu %s: %w", menuID, err)
	}
	return q.SetMenuStock(ctx, store.SetMenuStockParams{MenuID: menuID, StockQty: int32(m.after)}) //nolint:gosec // as above
}
