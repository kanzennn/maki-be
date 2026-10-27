package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// DeviceService is each outlet's cashier devices. One of the outlet's
// Managers activates a device on the device itself; from then on it proves
// itself with a token only it holds, apart from whoever signs in on it. A
// Manager or the Owner revokes it. The cashier's endpoints ask it which
// device a request comes from.
type DeviceService struct {
	db      *config.Database
	devices *repositories.DeviceRepository
	users   *UserService
	audit   *AuditService
}

// NewDeviceService returns the devices' service. users names who activated
// and revoked them.
func NewDeviceService(db *config.Database, devices *repositories.DeviceRepository, users *UserService, audit *AuditService) *DeviceService {
	return &DeviceService{db: db, devices: devices, users: users, audit: audit}
}

// Activate registers the device the Manager is signed in on, under its name,
// and returns it with its token: the one copy, which the device keeps.
// current is the device token the request carries: a device that is the
// outlet's already, and not revoked, isn't activated twice.
func (s *DeviceService) Activate(ctx context.Context, p models.Principal, ip, outletID, current, name, platform string) (models.Device, string, error) {
	name, err := validations.CheckDevice(name, platform)
	if err != nil {
		return models.Device{}, "", err
	}
	if current != "" {
		d, err := s.devices.ByToken(ctx, hashSecret(current))
		if err == nil && d.OutletID == outletID && d.RevokedAt == nil {
			return models.Device{}, "", apperror.ErrDeviceActive
		}
		if err != nil && !errors.Is(err, apperror.ErrDeviceNotRegistered) {
			return models.Device{}, "", err
		}
	}

	token, tokenHash := newSecret()
	deviceID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.devices.LockOutlet(ctx, outletID); err != nil {
			return err
		}
		n, err := s.devices.NextNumber(ctx, outletID)
		if err != nil {
			return err
		}
		if n > 9999 {
			return fmt.Errorf("outlet %s has used every device code", outletID)
		}
		code := fmt.Sprintf("K%02d", n)
		if err := s.devices.Insert(ctx, models.NewDevice{
			DeviceID: deviceID, OutletID: outletID, Code: code, Name: name, Platform: platform,
			TokenHash: tokenHash, ActivatedBy: p.UserID,
		}); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "device.activated", UserID: p.UserID, OutletID: outletID, TargetType: "device", TargetID: deviceID,
			After: map[string]any{"code": code, "name": name, "platform": platform}, IP: ip,
		})
	})
	if err != nil {
		return models.Device{}, "", err
	}
	d, err := s.Device(ctx, outletID, deviceID)
	return d, token, err
}

// List returns a page of the outlet's devices, those in use first, and how
// many there are in all.
func (s *DeviceService) List(ctx context.Context, outletID string, page pagination.Page) ([]models.Device, int, error) {
	out, total, err := s.devices.List(ctx, outletID, page)
	if err != nil {
		return nil, 0, err
	}
	if err := s.name(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// Device returns one of the outlet's devices.
func (s *DeviceService) Device(ctx context.Context, outletID, deviceID string) (models.Device, error) {
	d, err := s.devices.Get(ctx, outletID, deviceID)
	if err != nil {
		return models.Device{}, err
	}
	out := []models.Device{d}
	if err := s.name(ctx, out); err != nil {
		return models.Device{}, err
	}
	return out[0], nil
}

// Revoke stops the device selling, at once: a sale it has in progress
// finishes first. Revoking it again changes nothing.
func (s *DeviceService) Revoke(ctx context.Context, p models.Principal, ip, outletID, deviceID string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		d, err := s.devices.Lock(ctx, outletID, deviceID)
		if err != nil {
			return err
		}
		if d.RevokedAt != nil {
			return nil
		}
		if err := s.devices.Revoke(ctx, deviceID, p.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "device.revoked", UserID: p.UserID, OutletID: outletID, TargetType: "device", TargetID: deviceID,
			Before: map[string]any{"code": d.Code, "name": d.Name, "platform": d.Platform}, IP: ip,
		})
	})
}

// Current returns the outlet's device the token belongs to: for the
// cashier to know it can sell, and under which code. A token that is
// missing, unknown or another outlet's is ErrDeviceNotRegistered; a revoked
// device's is ErrDeviceRevoked.
func (s *DeviceService) Current(ctx context.Context, outletID, token string) (models.Device, error) {
	if token == "" {
		return models.Device{}, apperror.ErrDeviceNotRegistered
	}
	d, err := s.devices.ByToken(ctx, hashSecret(token))
	return usable(outletID, d, err)
}

// Open is Current for the cashier starting on the device, which it marks
// seen.
func (s *DeviceService) Open(ctx context.Context, outletID, token string) (models.Device, error) {
	d, err := s.Current(ctx, outletID, token)
	if err != nil {
		return models.Device{}, err
	}
	if err := s.devices.Touch(ctx, d.DeviceID); err != nil {
		return models.Device{}, err
	}
	return d, nil
}

// Hold is Current for a sale, in the transaction ctx carries: it holds the
// device until the sale commits, so a revocation waits for it and the
// device's sales go one at a time, and marks the device seen.
func (s *DeviceService) Hold(ctx context.Context, outletID, token string) (models.Device, error) {
	if token == "" {
		return models.Device{}, apperror.ErrDeviceNotRegistered
	}
	var d models.Device
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		held, err := s.devices.HoldByToken(ctx, hashSecret(token))
		if d, err = usable(outletID, held, err); err != nil {
			return err
		}
		return s.devices.Touch(ctx, d.DeviceID)
	})
	if err != nil {
		return models.Device{}, err
	}
	return d, nil
}

// usable is the device a token found, if it is the outlet's and not
// revoked.
func usable(outletID string, d models.Device, err error) (models.Device, error) {
	switch {
	case err != nil:
		return models.Device{}, err
	case d.OutletID != outletID:
		return models.Device{}, apperror.ErrDeviceNotRegistered
	case d.RevokedAt != nil:
		return models.Device{}, apperror.ErrDeviceRevoked
	}
	return d, nil
}

// name fills in the names of who activated and revoked the devices.
func (s *DeviceService) name(ctx context.Context, devices []models.Device) error {
	var ids []string
	for _, d := range devices {
		ids = append(ids, d.ActivatedBy.UserID)
		if d.RevokedBy != nil {
			ids = append(ids, d.RevokedBy.UserID)
		}
	}
	names, err := s.users.Names(ctx, ids)
	if err != nil {
		return err
	}
	for i := range devices {
		devices[i].ActivatedBy.Name = names[devices[i].ActivatedBy.UserID]
		if devices[i].RevokedBy != nil {
			devices[i].RevokedBy.Name = names[devices[i].RevokedBy.UserID]
		}
	}
	return nil
}
