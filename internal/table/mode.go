package table

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/table/store"
)

// Mode is how an outlet seats its dine-in customers. One at a time: the
// Manager picks it, and it holds from the next sale on.
type Mode string

const (
	// ModeTable seats them at the tables of the outlet's layout, which the
	// cashier may pick. An outlet that never picked a mode has it.
	ModeTable Mode = "table"
	// ModeNumber seats them wherever they set down the number stand the
	// cashier gave them, where a waiter brings the order: a dine-in sale
	// carries the stand's number, and no table.
	ModeNumber Mode = "number"
)

// Valid reports whether m is one of the modes.
func (m Mode) Valid() bool { return m == ModeTable || m == ModeNumber }

// DineInMode returns the outlet's dine-in mode.
func (s *Service) DineInMode(ctx context.Context, outletID string) (Mode, error) {
	m, err := store.New(s.pool).GetDineInMode(ctx, outletID)
	if err != nil {
		return "", fmt.Errorf("read the dine-in mode of %s: %w", outletID, err)
	}
	return Mode(m), nil
}

// SetDineInMode sets the outlet's dine-in mode, from its next sale on, and
// returns it. A sale under way finishes first, under the mode it began
// with. Setting the mode the outlet has changes and records nothing.
func (s *Service) SetDineInMode(ctx context.Context, p authn.Principal, ip, outletID string, mode Mode) (Mode, error) {
	if !mode.Valid() {
		return "", httpx.Invalid(map[string]string{"mode": "oneof"})
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockDineInMode(ctx, outletID); err != nil {
			return fmt.Errorf("lock the dine-in mode of %s: %w", outletID, err)
		}
		current, err := q.GetDineInMode(ctx, outletID)
		if err != nil {
			return fmt.Errorf("read the dine-in mode of %s: %w", outletID, err)
		}
		if Mode(current) == mode {
			return nil
		}
		if err := q.SetDineInMode(ctx, store.SetDineInModeParams{
			OutletID: outletID, Mode: string(mode), UpdatedBy: p.UserID,
		}); err != nil {
			return fmt.Errorf("set the dine-in mode of %s: %w", outletID, err)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "dine_in_mode.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "outlet", TargetID: outletID,
			Before: map[string]any{"mode": current}, After: map[string]any{"mode": string(mode)}, IP: ip,
		})
	})
	if err != nil {
		return "", err
	}
	return mode, nil
}

// HoldDineInMode returns the outlet's dine-in mode for a sale, in the
// sale's own transaction until it commits: a change of the mode waits for
// the sale, so the sale is decided under the mode it is saved under.
func (s *Service) HoldDineInMode(ctx context.Context, tx pgx.Tx, outletID string) (Mode, error) {
	q := store.New(tx)
	if err := q.HoldDineInMode(ctx, outletID); err != nil {
		return "", fmt.Errorf("hold the dine-in mode of %s: %w", outletID, err)
	}
	m, err := q.GetDineInMode(ctx, outletID)
	if err != nil {
		return "", fmt.Errorf("read the dine-in mode of %s: %w", outletID, err)
	}
	return Mode(m), nil
}
