package order

import (
	"context"
	"fmt"
	"time"

	"github.com/kervzent/maki-be/internal/device"
	"github.com/kervzent/maki-be/internal/menu"
	"github.com/kervzent/maki-be/internal/order/store"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/table"
)

// Catalog is everything the cashier sells from, in one: the outlet for the
// receipt and the rates, the payment methods it takes, how it seats its
// dine-in customers, and its categories and menus in the cashier's order.
// One request, so a device can later keep it offline whole.
type Catalog struct {
	Outlet         outlet.Cashier
	PaymentMethods []string
	DineInMode     table.Mode
	Categories     []menu.PosCategory
	Menus          []menu.Menu
}

// Catalog returns the outlet's catalog.
func (s *Service) Catalog(ctx context.Context, outletID string) (Catalog, error) {
	o, err := s.outlets.Cashier(ctx, outletID)
	if err != nil {
		return Catalog{}, err
	}
	methods, err := s.payments.Enabled(ctx, outletID)
	if err != nil {
		return Catalog{}, err
	}
	mode, err := s.tables.DineInMode(ctx, outletID)
	if err != nil {
		return Catalog{}, err
	}
	categories, menus, err := s.menus.PosCatalog(ctx, outletID)
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{Outlet: o, PaymentMethods: methods, DineInMode: mode, Categories: categories, Menus: menus}, nil
}

// PosDevice is the device the cashier runs on, and its count of receipt
// numbers today.
type PosDevice struct {
	Device device.Device
	// Prefix starts each of its receipt numbers: the outlet's code and the
	// device's, as in BDG01-K01.
	Prefix string
	// Day is today in the outlet's time zone, from its midnight.
	Day time.Time
	// NextNumber is the count the device's next receipt number today
	// carries: one past the highest it has used.
	NextNumber int
}

// PosDevice returns the device the request comes from, for the cashier
// starting on it, and marks it seen. A device the outlet hasn't activated,
// or has revoked, can't sell.
func (s *Service) PosDevice(ctx context.Context, outletID, token string) (PosDevice, error) {
	d, err := s.devices.Open(ctx, outletID, token)
	if err != nil {
		return PosDevice{}, err
	}
	o, err := s.outlets.Cashier(ctx, outletID)
	if err != nil {
		return PosDevice{}, err
	}
	start, end, err := day(s.now(), o.Timezone)
	if err != nil {
		return PosDevice{}, err
	}
	last, err := store.New(s.pool).LastOrderNumber(ctx, store.LastOrderNumberParams{
		OutletID: outletID, DeviceID: d.DeviceID, DayStart: start, DayEnd: end,
	})
	if err != nil {
		return PosDevice{}, fmt.Errorf("find the last receipt number of device %s: %w", d.DeviceID, err)
	}
	return PosDevice{Device: d, Prefix: o.Code + "-" + d.Code, Day: start, NextNumber: int(last) + 1}, nil
}
