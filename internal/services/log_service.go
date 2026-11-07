package services

import (
	"context"
	"maps"
	"slices"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// LogService reads the Log Action back: every outlet's for the Owner, one
// outlet's for those who may see it. AuditService is what writes it.
type LogService struct {
	logs    *repositories.AuditRepository
	users   *repositories.UserRepository
	devices *repositories.DeviceRepository
	outlets *OutletService
}

// NewLogService returns the Log Action reader.
func NewLogService(logs *repositories.AuditRepository, users *repositories.UserRepository, devices *repositories.DeviceRepository, outlets *OutletService) *LogService {
	return &LogService{logs: logs, users: users, devices: devices, outlets: outlets}
}

// List returns a page of the entries the filter leaves, newest first, each
// naming who, where and which device, and how many there are.
func (s *LogService) List(ctx context.Context, f models.LogFilter, page pagination.Page) ([]models.ActionLog, int, error) {
	logs, total, err := s.logs.List(ctx, f, page)
	if err != nil {
		return nil, 0, err
	}
	userIDs, outletIDs, deviceIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, l := range logs {
		if l.UserID != nil {
			userIDs[*l.UserID] = true
		}
		if l.OutletID != nil {
			outletIDs[*l.OutletID] = true
		}
		if l.DeviceID != nil {
			deviceIDs[*l.DeviceID] = true
		}
	}
	users, err := s.users.Labels(ctx, slices.Collect(maps.Keys(userIDs)))
	if err != nil {
		return nil, 0, err
	}
	outlets, err := s.outlets.Refs(ctx, slices.Collect(maps.Keys(outletIDs)))
	if err != nil {
		return nil, 0, err
	}
	devices, err := s.devices.Refs(ctx, slices.Collect(maps.Keys(deviceIDs)))
	if err != nil {
		return nil, 0, err
	}
	for i := range logs {
		l := &logs[i]
		if l.UserID != nil {
			if u, ok := users[*l.UserID]; ok {
				l.User = &u
			}
		}
		if l.OutletID != nil {
			if o, ok := outlets[*l.OutletID]; ok {
				l.Outlet = &o
			}
		}
		if l.DeviceID != nil {
			if d, ok := devices[*l.DeviceID]; ok {
				l.Device = &d
			}
		}
	}
	return logs, total, nil
}
