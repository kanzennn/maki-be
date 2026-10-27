package services

import (
	"context"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
)

// PosService is what the cashier starts from: the catalog it sells from,
// and the device it runs on. Every method takes the outlet it acts in; the
// routes have checked the caller may act there.
type PosService struct {
	orders   *repositories.OrderRepository
	menus    *MenuService
	devices  *DeviceService
	payments *PaymentService
	outlets  *OutletService
	modes    *DineInModeService
	now      func() time.Time
}

// NewPosService returns the cashier's service. orders counts each device's
// receipt numbers; now is the clock that says which day is today.
func NewPosService(orders *repositories.OrderRepository, menus *MenuService, devices *DeviceService, payments *PaymentService,
	outlets *OutletService, modes *DineInModeService, now func() time.Time,
) *PosService {
	return &PosService{
		orders: orders, menus: menus, devices: devices, payments: payments, outlets: outlets, modes: modes, now: now,
	}
}

// Catalog returns the outlet's catalog.
func (s *PosService) Catalog(ctx context.Context, outletID string) (models.PosCatalog, error) {
	o, err := s.outlets.Cashier(ctx, outletID)
	if err != nil {
		return models.PosCatalog{}, err
	}
	methods, err := s.payments.Enabled(ctx, outletID)
	if err != nil {
		return models.PosCatalog{}, err
	}
	mode, err := s.modes.DineInMode(ctx, outletID)
	if err != nil {
		return models.PosCatalog{}, err
	}
	categories, menus, err := s.menus.PosCatalog(ctx, outletID)
	if err != nil {
		return models.PosCatalog{}, err
	}
	return models.PosCatalog{
		Outlet: o, PaymentMethods: methods, DineInMode: mode, Categories: categories, Menus: menus,
	}, nil
}

// Device returns the device the request comes from, for the cashier
// starting on it, and marks it seen. A device the outlet hasn't activated,
// or has revoked, can't sell.
func (s *PosService) Device(ctx context.Context, outletID, token string) (models.PosDevice, error) {
	d, err := s.devices.Open(ctx, outletID, token)
	if err != nil {
		return models.PosDevice{}, err
	}
	o, err := s.outlets.Cashier(ctx, outletID)
	if err != nil {
		return models.PosDevice{}, err
	}
	start, end, err := outletDay(s.now(), o.Timezone)
	if err != nil {
		return models.PosDevice{}, err
	}
	last, err := s.orders.LastNumber(ctx, outletID, d.DeviceID, start, end)
	if err != nil {
		return models.PosDevice{}, err
	}
	return models.PosDevice{Device: d, Prefix: o.Code + "-" + d.Code, Day: start, NextNumber: last + 1}, nil
}
