package services

import (
	"context"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/validations"
)

// DineInModeService is how each outlet seats its dine-in customers: at the
// tables of its layout, or by number stands.
type DineInModeService struct {
	db    *config.Database
	modes *repositories.DineInModeRepository
	audit *AuditService
}

// NewDineInModeService returns the dine-in modes' service.
func NewDineInModeService(db *config.Database, modes *repositories.DineInModeRepository, audit *AuditService) *DineInModeService {
	return &DineInModeService{db: db, modes: modes, audit: audit}
}

// DineInMode returns the outlet's dine-in mode.
func (s *DineInModeService) DineInMode(ctx context.Context, outletID string) (models.DineInMode, error) {
	return s.modes.Get(ctx, outletID)
}

// SetDineInMode sets the outlet's dine-in mode, from its next sale on, and
// returns it. A sale under way finishes first, under the mode it began
// with. Setting the mode the outlet has changes and records nothing.
func (s *DineInModeService) SetDineInMode(ctx context.Context, p models.Principal, ip, outletID string, mode models.DineInMode) (models.DineInMode, error) {
	if err := validations.CheckDineInMode(mode); err != nil {
		return "", err
	}
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.modes.Lock(ctx, outletID); err != nil {
			return err
		}
		current, err := s.modes.Get(ctx, outletID)
		if err != nil {
			return err
		}
		if current == mode {
			return nil
		}
		if err := s.modes.Set(ctx, outletID, mode, p.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "dine_in_mode.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "outlet", TargetID: outletID,
			Before: map[string]any{"mode": string(current)}, After: map[string]any{"mode": string(mode)}, IP: ip,
		})
	})
	if err != nil {
		return "", err
	}
	return mode, nil
}

// HoldDineInMode returns the outlet's dine-in mode for a sale, in the sale's
// own transaction, the one ctx carries, until it commits: a change of the
// mode waits for the sale, so the sale is decided under the mode it is saved
// under.
func (s *DineInModeService) HoldDineInMode(ctx context.Context, outletID string) (models.DineInMode, error) {
	var mode models.DineInMode
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.modes.Hold(ctx, outletID); err != nil {
			return err
		}
		var err error
		mode, err = s.modes.Get(ctx, outletID)
		return err
	})
	return mode, err
}
