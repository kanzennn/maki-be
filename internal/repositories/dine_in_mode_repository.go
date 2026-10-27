package repositories

import (
	"context"
	"fmt"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/table"
)

// DineInModeRepository owns dine_in_settings: how each outlet seats its
// dine-in customers. An outlet without a row has the table mode.
type DineInModeRepository struct {
	db *config.Database
}

// NewDineInModeRepository returns the dine-in modes' repository.
func NewDineInModeRepository(db *config.Database) *DineInModeRepository {
	return &DineInModeRepository{db: db}
}

// Get returns the outlet's dine-in mode.
func (r *DineInModeRepository) Get(ctx context.Context, outletID string) (models.DineInMode, error) {
	m, err := store.New(r.db.Conn(ctx)).GetDineInMode(ctx, outletID)
	if err != nil {
		return "", fmt.Errorf("read the dine-in mode of %s: %w", outletID, err)
	}
	return models.DineInMode(m), nil
}

// Lock locks the outlet's dine-in mode for a change: it waits for the sales
// under way, which hold it.
func (r *DineInModeRepository) Lock(ctx context.Context, outletID string) error {
	if err := store.New(r.db.Conn(ctx)).LockDineInMode(ctx, outletID); err != nil {
		return fmt.Errorf("lock the dine-in mode of %s: %w", outletID, err)
	}
	return nil
}

// Hold holds the outlet's dine-in mode for a sale until the transaction ctx
// carries ends: a change of the mode waits for it.
func (r *DineInModeRepository) Hold(ctx context.Context, outletID string) error {
	if err := store.New(r.db.Conn(ctx)).HoldDineInMode(ctx, outletID); err != nil {
		return fmt.Errorf("hold the dine-in mode of %s: %w", outletID, err)
	}
	return nil
}

// Set stores the outlet's dine-in mode; updatedBy is who chose it.
func (r *DineInModeRepository) Set(ctx context.Context, outletID string, mode models.DineInMode, updatedBy string) error {
	if err := store.New(r.db.Conn(ctx)).SetDineInMode(ctx, store.SetDineInModeParams{
		OutletID: outletID, Mode: string(mode), UpdatedBy: updatedBy,
	}); err != nil {
		return fmt.Errorf("set the dine-in mode of %s: %w", outletID, err)
	}
	return nil
}
