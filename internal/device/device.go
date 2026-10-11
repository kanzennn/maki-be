// Package device is an outlet's cashier devices. One of the outlet's
// Managers activates a device on the device itself; from then on it proves
// itself with a token only it holds, apart from whoever signs in on it. A
// Manager or the Owner revokes it. It owns the devices table.
package device

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/device/store"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
)

// The platforms a device runs on.
const (
	PlatformWeb     = "web"     // the cashier in a browser, on the till's PC
	PlatformAndroid = "android" // the Android app
)

// Device is one of an outlet's cashier devices.
type Device struct {
	DeviceID string
	OutletID string
	// Code is in every receipt number the device makes: K01, K02, … in the
	// order the outlet's devices were activated.
	Code        string
	Name        string
	Platform    string
	ActivatedBy Person
	// LastSeenAt is when the device last sold, or opened the cashier.
	LastSeenAt *time.Time
	RevokedAt  *time.Time
	RevokedBy  *Person
	CreatedAt  time.Time
}

// Person is who activated or revoked a device.
type Person struct {
	UserID string
	Name   string
}

// Users is the user module, as far as devices need it: the names of who
// activated and revoked them.
type Users interface {
	Names(ctx context.Context, userIDs []string) (map[string]string, error)
}

// Auditor records Log Action entries inside a transaction; audit.Recorder
// is the real one.
type Auditor interface {
	Record(ctx context.Context, tx pgx.Tx, e audit.Entry) error
}

// Service activates, lists and revokes an outlet's devices, and tells the
// cashier's endpoints which device a request comes from.
type Service struct {
	pool  *pgxpool.Pool
	users Users
	audit Auditor
}

// NewService returns the device module's service.
func NewService(pool *pgxpool.Pool, users Users, auditor Auditor) *Service {
	return &Service{pool: pool, users: users, audit: auditor}
}

// Activate registers the device the Manager is signed in on, under its name,
// and returns it with its token: the one copy, which the device keeps.
// current is the device token the request carries: a device that is the
// outlet's already, and not revoked, isn't activated twice.
func (s *Service) Activate(ctx context.Context, p authn.Principal, ip, outletID, current, name, platform string) (Device, string, error) {
	name = strings.TrimSpace(name)
	fields := map[string]string{}
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		fields["name"] = "required"
	case n > 50:
		fields["name"] = "max"
	}
	if platform != PlatformWeb && platform != PlatformAndroid {
		fields["platform"] = "oneof"
	}
	if len(fields) > 0 {
		return Device{}, "", httpx.Invalid(fields)
	}
	if current != "" {
		d, err := store.New(s.pool).DeviceByToken(ctx, hashToken(current))
		if err == nil && d.OutletID == outletID && d.RevokedAt == nil {
			return Device{}, "", ErrDeviceActive
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Device{}, "", fmt.Errorf("find the current device: %w", err)
		}
	}

	token, err := newToken()
	if err != nil {
		return Device{}, "", err
	}
	deviceID := id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.LockOutletDevices(ctx, outletID); err != nil {
			return fmt.Errorf("lock the devices of %s: %w", outletID, err)
		}
		n, err := q.NextDeviceNumber(ctx, outletID)
		if err != nil {
			return fmt.Errorf("number the device: %w", err)
		}
		if n > 9999 {
			return fmt.Errorf("outlet %s has used every device code", outletID)
		}
		code := fmt.Sprintf("K%02d", n)
		if err := q.InsertDevice(ctx, store.InsertDeviceParams{
			DeviceID: deviceID, OutletID: outletID, Code: code, Name: name, Platform: platform,
			TokenHash: hashToken(token), ActivatedBy: p.UserID,
		}); err != nil {
			return fmt.Errorf("insert device: %w", err)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "device.activated", UserID: p.UserID, OutletID: outletID, TargetType: "device", TargetID: deviceID,
			After: map[string]any{"code": code, "name": name, "platform": platform}, IP: ip,
		})
	})
	if err != nil {
		return Device{}, "", err
	}
	d, err := s.Device(ctx, outletID, deviceID)
	return d, token, err
}

// List returns a page of the outlet's devices, those in use first, and how
// many there are in all.
func (s *Service) List(ctx context.Context, outletID string, page httpx.Page) ([]Device, int, error) {
	q := store.New(s.pool)
	total, err := q.CountDevices(ctx, outletID)
	if err != nil {
		return nil, 0, fmt.Errorf("count devices: %w", err)
	}
	rows, err := q.ListDevices(ctx, store.ListDevicesParams{
		OutletID:   outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list devices: %w", err)
	}
	out := make([]Device, len(rows))
	for i, r := range rows {
		out[i] = device(outletID, store.GetDeviceRow(r))
	}
	if err := s.name(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, int(total), nil
}

// Device returns one of the outlet's devices.
func (s *Service) Device(ctx context.Context, outletID, deviceID string) (Device, error) {
	r, err := store.New(s.pool).GetDevice(ctx, store.GetDeviceParams{DeviceID: deviceID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrDeviceNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("load device %s: %w", deviceID, err)
	}
	out := []Device{device(outletID, r)}
	if err := s.name(ctx, out); err != nil {
		return Device{}, err
	}
	return out[0], nil
}

// Revoke stops the device selling, at once: a sale it has in progress
// finishes first. Revoking it again changes nothing.
func (s *Service) Revoke(ctx context.Context, p authn.Principal, ip, outletID, deviceID string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		d, err := q.LockDevice(ctx, store.LockDeviceParams{DeviceID: deviceID, OutletID: outletID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceNotFound
		}
		if err != nil {
			return fmt.Errorf("lock device %s: %w", deviceID, err)
		}
		if d.RevokedAt != nil {
			return nil
		}
		if err := q.RevokeDevice(ctx, store.RevokeDeviceParams{DeviceID: deviceID, RevokedBy: &p.UserID}); err != nil {
			return fmt.Errorf("revoke device %s: %w", deviceID, err)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "device.revoked", UserID: p.UserID, OutletID: outletID, TargetType: "device", TargetID: deviceID,
			Before: map[string]any{"code": d.Code, "name": d.Name, "platform": d.Platform}, IP: ip,
		})
	})
}

// Current returns the outlet's device the token belongs to: for the
// cashier to know it can sell, and under which code. A token that is
// missing, unknown or another outlet's is ErrDeviceNotRegistered; a revoked
// device's is ErrDeviceRevoked.
func (s *Service) Current(ctx context.Context, outletID, token string) (Device, error) {
	if token == "" {
		return Device{}, ErrDeviceNotRegistered
	}
	r, err := store.New(s.pool).DeviceByToken(ctx, hashToken(token))
	return current(outletID, store.HoldDeviceByTokenRow(r), err)
}

// Open is Current for the cashier starting on the device, which it marks
// seen.
func (s *Service) Open(ctx context.Context, outletID, token string) (Device, error) {
	d, err := s.Current(ctx, outletID, token)
	if err != nil {
		return Device{}, err
	}
	if err := store.New(s.pool).TouchDevice(ctx, d.DeviceID); err != nil {
		return Device{}, fmt.Errorf("touch device %s: %w", d.DeviceID, err)
	}
	return d, nil
}

// Hold is Current for a sale, in its transaction: it holds the device until
// the sale commits, so a revocation waits for it and the device's sales go
// one at a time, and marks the device seen.
func (s *Service) Hold(ctx context.Context, tx pgx.Tx, outletID, token string) (Device, error) {
	if token == "" {
		return Device{}, ErrDeviceNotRegistered
	}
	q := store.New(tx)
	r, err := q.HoldDeviceByToken(ctx, hashToken(token))
	d, err := current(outletID, r, err)
	if err != nil {
		return Device{}, err
	}
	if err := q.TouchDevice(ctx, d.DeviceID); err != nil {
		return Device{}, fmt.Errorf("touch device %s: %w", d.DeviceID, err)
	}
	return d, nil
}

func current(outletID string, r store.HoldDeviceByTokenRow, err error) (Device, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrDeviceNotRegistered
	}
	if err != nil {
		return Device{}, fmt.Errorf("find the device: %w", err)
	}
	if r.OutletID != outletID {
		return Device{}, ErrDeviceNotRegistered
	}
	if r.RevokedAt != nil {
		return Device{}, ErrDeviceRevoked
	}
	return Device{DeviceID: r.DeviceID, OutletID: r.OutletID, Code: r.Code, Name: r.Name, Platform: r.Platform}, nil
}

func device(outletID string, r store.GetDeviceRow) Device {
	d := Device{
		DeviceID: r.DeviceID, OutletID: outletID, Code: r.Code, Name: r.Name, Platform: r.Platform,
		ActivatedBy: Person{UserID: r.ActivatedBy}, LastSeenAt: r.LastSeenAt, RevokedAt: r.RevokedAt,
		CreatedAt: r.CreatedAt,
	}
	if r.RevokedBy != nil {
		d.RevokedBy = &Person{UserID: *r.RevokedBy}
	}
	return d
}

// name fills in the names of who activated and revoked the devices.
func (s *Service) name(ctx context.Context, devices []Device) error {
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

// newToken returns a new device token: 32 random bytes, URL-safe.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make a device token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is what the database keeps of a token, as it does of sessions'.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
