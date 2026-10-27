package models

import "time"

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

// NewDevice is a device to insert, under the hash of its token.
type NewDevice struct {
	DeviceID, OutletID, Code, Name, Platform string
	TokenHash                                string
	ActivatedBy                              string
}

// DeviceResponse is a device as the device endpoints send it.
type DeviceResponse struct {
	DeviceID    string          `json:"device_id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Platform    string          `json:"platform"`
	Status      string          `json:"status"`
	ActivatedBy PersonResponse  `json:"activated_by"`
	ActivatedAt time.Time       `json:"activated_at"`
	LastSeenAt  *time.Time      `json:"last_seen_at"`
	RevokedAt   *time.Time      `json:"revoked_at"`
	RevokedBy   *PersonResponse `json:"revoked_by"`
}

// ActivateDeviceResponse is the activated device and, for the Android app,
// its token. A browser gets its token as a cookie instead, which page
// scripts can't read.
type ActivateDeviceResponse struct {
	Device DeviceResponse `json:"device"`
	Token  *string        `json:"token"`
}

// NewDeviceResponse returns the device's response, its times in UTC.
func NewDeviceResponse(d Device) DeviceResponse {
	out := DeviceResponse{
		DeviceID:    d.DeviceID,
		Code:        d.Code,
		Name:        d.Name,
		Platform:    d.Platform,
		Status:      "active",
		ActivatedBy: NewPersonResponse(d.ActivatedBy),
		ActivatedAt: d.CreatedAt.UTC(),
		LastSeenAt:  utc(d.LastSeenAt),
		RevokedAt:   utc(d.RevokedAt),
	}
	if d.RevokedAt != nil {
		out.Status = "revoked"
	}
	if d.RevokedBy != nil {
		by := NewPersonResponse(*d.RevokedBy)
		out.RevokedBy = &by
	}
	return out
}

// utc is t in UTC, or nil.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
