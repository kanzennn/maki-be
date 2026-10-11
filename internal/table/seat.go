package table

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/table/store"
)

// Floor is the outlet's tables as the cashier sees them: each area that
// holds a table, in order, with its decorations and its tables.
type Floor struct {
	Areas []FloorArea
}

// FloorArea is one area of the cashier's floor.
type FloorArea struct {
	AreaID      string
	Name        string
	Decorations []Decoration
	Tables      []Table
}

// Seat is the table a sale seats its customers at, as the order keeps it.
type Seat struct {
	TableID string
	Name    string
}

// Floor returns the outlet's areas that hold tables, in order, with their
// decorations, and their tables with their statuses now. An outlet that
// seats its customers by number stands has none to pick from.
func (s *Service) Floor(ctx context.Context, outletID string) (Floor, error) {
	var out Floor
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		mode, err := store.New(tx).GetDineInMode(ctx, outletID)
		if err != nil {
			return fmt.Errorf("read the dine-in mode of %s: %w", outletID, err)
		}
		if Mode(mode) == ModeNumber {
			return nil
		}
		areas, err := store.New(tx).ListFloorAreas(ctx, outletID)
		if err != nil {
			return fmt.Errorf("list the areas of %s: %w", outletID, err)
		}
		tables, err := s.tablesIn(ctx, tx, outletID, nil)
		if err != nil {
			return err
		}
		byArea := map[string][]Table{}
		for _, t := range tables {
			byArea[t.AreaID] = append(byArea[t.AreaID], t)
		}
		for _, a := range areas {
			if len(byArea[a.AreaID]) == 0 {
				continue
			}
			decorations, err := readDecorations(a.Decorations)
			if err != nil {
				return err
			}
			out.Areas = append(out.Areas, FloorArea{
				AreaID: a.AreaID, Name: a.Name, Decorations: decorations, Tables: byArea[a.AreaID],
			})
		}
		return nil
	})
	return out, err
}

// HoldForSale holds one of the outlet's tables for a sale, in the sale's
// own transaction until it commits: other sales at the table go on, while a
// change, a release or a delete of it waits for the sale. The table is the
// outlet's, not deleted, and selectable now; otherwise ErrTableUnavailable,
// with its status, or "missing".
func (s *Service) HoldForSale(ctx context.Context, tx pgx.Tx, outletID, tableID string) (Seat, error) {
	now := s.now()
	r, err := store.New(tx).HoldTable(ctx, store.HoldTableParams{TableID: tableID, OutletID: outletID, At: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return Seat{}, ErrTableUnavailable.WithDetails(map[string]any{"status": "missing"})
	}
	if err != nil {
		return Seat{}, fmt.Errorf("hold table %s: %w", tableID, err)
	}
	since, sales, err := s.sales(ctx, tx, outletID, now)
	if err != nil {
		return Seat{}, err
	}
	_, occupied := seating(sales[tableID], since, r.ReleasedAt)
	if st := status(r.IsActive, occupied, r.Scheduled); !st.Selectable() {
		return Seat{}, ErrTableUnavailable.WithDetails(map[string]any{"status": string(st)})
	}
	return Seat{TableID: r.TableID, Name: r.Name}, nil
}

// Release empties a table its customers have left: the sales before it no
// longer seat anyone there. A sale at it under way finishes first, and is
// released too. A table nobody is seated at stays as it is, and records
// nothing.
func (s *Service) Release(ctx context.Context, p authn.Principal, ip, outletID, tableID string) (Table, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockTable(ctx, q, outletID, tableID)
		if err != nil {
			return err
		}
		since, sales, err := s.sales(ctx, tx, outletID, s.now())
		if err != nil {
			return err
		}
		seated, ok := seating(sales[tableID], since, prev.ReleasedAt)
		if !ok {
			return nil
		}
		if err := q.ReleaseTable(ctx, tableID); err != nil {
			return fmt.Errorf("release table %s: %w", tableID, err)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "table.released", UserID: p.UserID, OutletID: outletID, TargetType: "table", TargetID: tableID,
			Before: map[string]any{"since": seated.Since.UTC(), "orders": seated.Orders}, IP: ip,
		})
	})
	if err != nil {
		return Table{}, err
	}
	return s.Table(ctx, outletID, tableID)
}
