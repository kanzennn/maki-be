package repositories

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/device"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// DeviceRepository owns devices, each outlet's cashier devices. A device's
// token is kept only as its hash.
type DeviceRepository struct {
	db *config.Database
}

// NewDeviceRepository returns the devices' repository.
func NewDeviceRepository(db *config.Database) *DeviceRepository {
	return &DeviceRepository{db: db}
}

// List returns a page of the outlet's devices, those in use first, and how
// many there are in all. Who activated and revoked them comes without a
// name.
func (r *DeviceRepository) List(ctx context.Context, outletID string, page pagination.Page) ([]models.Device, int, error) {
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountDevices(ctx, outletID)
	if err != nil {
		return nil, 0, fmt.Errorf("count devices: %w", err)
	}
	rows, err := q.ListDevices(ctx, store.ListDevicesParams{
		OutletID:   outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list devices: %w", err)
	}
	out := make([]models.Device, len(rows))
	for i, row := range rows {
		out[i] = deviceFrom(outletID, store.GetDeviceRow(row))
	}
	return out, int(total), nil
}

// Get returns one of the outlet's devices: ErrDeviceNotFound if it has no
// such device. Who activated and revoked it comes without a name.
func (r *DeviceRepository) Get(ctx context.Context, outletID, deviceID string) (models.Device, error) {
	row, err := store.New(r.db.Conn(ctx)).GetDevice(ctx, store.GetDeviceParams{DeviceID: deviceID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Device{}, apperror.ErrDeviceNotFound
	}
	if err != nil {
		return models.Device{}, fmt.Errorf("load device %s: %w", deviceID, err)
	}
	return deviceFrom(outletID, row), nil
}

// ByToken returns the device whose token hashes to tokenHash, of whichever
// outlet: ErrDeviceNotRegistered if there is none.
func (r *DeviceRepository) ByToken(ctx context.Context, tokenHash string) (models.Device, error) {
	row, err := store.New(r.db.Conn(ctx)).DeviceByToken(ctx, tokenHash)
	return tokenDevice(store.HoldDeviceByTokenRow(row), err)
}

// HoldByToken is ByToken that holds the device's row until the transaction
// ctx carries ends.
func (r *DeviceRepository) HoldByToken(ctx context.Context, tokenHash string) (models.Device, error) {
	return tokenDevice(store.New(r.db.Conn(ctx)).HoldDeviceByToken(ctx, tokenHash))
}

// Touch marks the device seen.
func (r *DeviceRepository) Touch(ctx context.Context, deviceID string) error {
	if err := store.New(r.db.Conn(ctx)).TouchDevice(ctx, deviceID); err != nil {
		return fmt.Errorf("touch device %s: %w", deviceID, err)
	}
	return nil
}

// LockOutlet locks the outlet's devices, so the devices activated at once
// get codes one after the other.
func (r *DeviceRepository) LockOutlet(ctx context.Context, outletID string) error {
	if err := store.New(r.db.Conn(ctx)).LockOutletDevices(ctx, outletID); err != nil {
		return fmt.Errorf("lock the devices of %s: %w", outletID, err)
	}
	return nil
}

// NextNumber returns the number of the outlet's next device: one more than
// it has ever activated.
func (r *DeviceRepository) NextNumber(ctx context.Context, outletID string) (int, error) {
	n, err := store.New(r.db.Conn(ctx)).NextDeviceNumber(ctx, outletID)
	if err != nil {
		return 0, fmt.Errorf("number the device: %w", err)
	}
	return int(n), nil
}

// Insert stores a newly activated device.
func (r *DeviceRepository) Insert(ctx context.Context, d models.NewDevice) error {
	if err := store.New(r.db.Conn(ctx)).InsertDevice(ctx, store.InsertDeviceParams(d)); err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	return nil
}

// Lock locks one of the outlet's devices for a change: ErrDeviceNotFound if
// it has no such device.
func (r *DeviceRepository) Lock(ctx context.Context, outletID, deviceID string) (models.Device, error) {
	row, err := store.New(r.db.Conn(ctx)).LockDevice(ctx, store.LockDeviceParams{DeviceID: deviceID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Device{}, apperror.ErrDeviceNotFound
	}
	if err != nil {
		return models.Device{}, fmt.Errorf("lock device %s: %w", deviceID, err)
	}
	return models.Device{
		DeviceID: row.DeviceID, OutletID: outletID, Code: row.Code, Name: row.Name, Platform: row.Platform,
		RevokedAt: row.RevokedAt,
	}, nil
}

// Revoke stops the device selling; revokedBy is who did it.
func (r *DeviceRepository) Revoke(ctx context.Context, deviceID, revokedBy string) error {
	if err := store.New(r.db.Conn(ctx)).RevokeDevice(ctx, store.RevokeDeviceParams{DeviceID: deviceID, RevokedBy: &revokedBy}); err != nil {
		return fmt.Errorf("revoke device %s: %w", deviceID, err)
	}
	return nil
}

// tokenDevice maps the device a token lookup found.
func tokenDevice(row store.HoldDeviceByTokenRow, err error) (models.Device, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Device{}, apperror.ErrDeviceNotRegistered
	}
	if err != nil {
		return models.Device{}, fmt.Errorf("find the device: %w", err)
	}
	return models.Device{
		DeviceID: row.DeviceID, OutletID: row.OutletID, Code: row.Code, Name: row.Name, Platform: row.Platform,
		RevokedAt: row.RevokedAt,
	}, nil
}

func deviceFrom(outletID string, row store.GetDeviceRow) models.Device {
	d := models.Device{
		DeviceID: row.DeviceID, OutletID: outletID, Code: row.Code, Name: row.Name, Platform: row.Platform,
		ActivatedBy: models.Person{UserID: row.ActivatedBy}, LastSeenAt: row.LastSeenAt, RevokedAt: row.RevokedAt,
		CreatedAt: row.CreatedAt,
	}
	if row.RevokedBy != nil {
		d.RevokedBy = &models.Person{UserID: *row.RevokedBy}
	}
	return d
}
