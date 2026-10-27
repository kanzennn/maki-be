package services

import (
	"context"
	"slices"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// AreaService is each outlet's dining areas, in order: the rooms or
// terraces its tables stand in, each with a canvas of its own.
type AreaService struct {
	db    *config.Database
	areas *repositories.AreaRepository
	audit *AuditService
}

// NewAreaService returns the areas' service.
func NewAreaService(db *config.Database, areas *repositories.AreaRepository, audit *AuditService) *AreaService {
	return &AreaService{db: db, areas: areas, audit: audit}
}

// ListAreas returns a page of the outlet's areas, in their order, and how
// many there are in all.
func (s *AreaService) ListAreas(ctx context.Context, outletID string, page pagination.Page) ([]models.Area, int, error) {
	return s.areas.List(ctx, outletID, page)
}

// Area returns one of the outlet's areas.
func (s *AreaService) Area(ctx context.Context, outletID, areaID string) (models.Area, error) {
	return s.areas.Get(ctx, outletID, areaID)
}

// CreateArea adds an area to the outlet, last in the order, with an empty
// canvas.
func (s *AreaService) CreateArea(ctx context.Context, p models.Principal, ip, outletID string, in models.AreaInput) (models.Area, error) {
	name, err := validations.CheckArea(in)
	if err != nil {
		return models.Area{}, err
	}
	areaID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		order, err := s.areas.NextOrder(ctx, outletID)
		if err != nil {
			return err
		}
		if err := s.areas.Insert(ctx, outletID, areaID, name, order); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "area.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "area", TargetID: areaID, After: map[string]any{"name": name}, IP: ip,
		})
	})
	if err != nil {
		return models.Area{}, err
	}
	return s.Area(ctx, outletID, areaID)
}

// UpdateArea renames an area. A change of nothing records nothing.
func (s *AreaService) UpdateArea(ctx context.Context, p models.Principal, ip, outletID, areaID string, in models.AreaInput) (models.Area, error) {
	name, err := validations.CheckArea(in)
	if err != nil {
		return models.Area{}, err
	}
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.areas.Lock(ctx, outletID, areaID)
		if err != nil {
			return err
		}
		if prev.Name == name {
			return nil
		}
		if err := s.areas.Update(ctx, areaID, name); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "area.updated", UserID: p.UserID, OutletID: outletID, TargetType: "area", TargetID: areaID,
			Before: map[string]any{"name": prev.Name}, After: map[string]any{"name": name}, IP: ip,
		})
	})
	if err != nil {
		return models.Area{}, err
	}
	return s.Area(ctx, outletID, areaID)
}

// ReorderAreas puts the outlet's areas in the order of areaIDs, which must
// list each of them once: otherwise ErrAreasChanged, since the caller's list
// is out of date.
func (s *AreaService) ReorderAreas(ctx context.Context, p models.Principal, ip, outletID string, areaIDs []string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		current, err := s.areas.LockIDs(ctx, outletID)
		if err != nil {
			return err
		}
		if !sameSet(current, areaIDs) {
			return apperror.ErrAreasChanged
		}
		if slices.Equal(current, areaIDs) {
			return nil
		}
		if err := s.areas.SetOrder(ctx, outletID, areaIDs); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "area.reordered", UserID: p.UserID, OutletID: outletID, TargetType: "outlet", TargetID: outletID,
			Before: map[string]any{"area_ids": current}, After: map[string]any{"area_ids": areaIDs}, IP: ip,
		})
	})
}

// DeleteArea soft-deletes an area that holds no table, and its canvas with
// it.
func (s *AreaService) DeleteArea(ctx context.Context, p models.Principal, ip, outletID, areaID string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.areas.Lock(ctx, outletID, areaID)
		if err != nil {
			return err
		}
		tables, err := s.areas.CountTables(ctx, areaID)
		if err != nil {
			return err
		}
		if tables > 0 {
			return apperror.ErrAreaNotEmpty.WithDetails(map[string]any{"tables": tables})
		}
		if err := s.areas.Delete(ctx, areaID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "area.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "area", TargetID: areaID, Before: map[string]any{"name": prev.Name}, IP: ip,
		})
	})
}
