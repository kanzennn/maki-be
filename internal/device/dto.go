package device

import "time"

// activateRequest is the body of POST /outlets/{oid}/devices. The caps here
// only bound what the server reads; Activate applies the real rules.
type activateRequest struct {
	Name     string `json:"name" binding:"required,max=200"`
	Platform string `json:"platform" binding:"required,max=16"`
}

type personResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type deviceResponse struct {
	DeviceID    string          `json:"device_id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Platform    string          `json:"platform"`
	Status      string          `json:"status"`
	ActivatedBy personResponse  `json:"activated_by"`
	ActivatedAt time.Time       `json:"activated_at"`
	LastSeenAt  *time.Time      `json:"last_seen_at"`
	RevokedAt   *time.Time      `json:"revoked_at"`
	RevokedBy   *personResponse `json:"revoked_by"`
}

// activateResponse is the activated device and, for the Android app, its
// token. A browser gets its token as a cookie instead, which page scripts
// can't read.
type activateResponse struct {
	Device deviceResponse `json:"device"`
	Token  *string        `json:"token"`
}

func toDeviceResponse(d Device) deviceResponse {
	out := deviceResponse{
		DeviceID:    d.DeviceID,
		Code:        d.Code,
		Name:        d.Name,
		Platform:    d.Platform,
		Status:      "active",
		ActivatedBy: personResponse{UserID: d.ActivatedBy.UserID, Name: d.ActivatedBy.Name},
		ActivatedAt: d.CreatedAt.UTC(),
		LastSeenAt:  utc(d.LastSeenAt),
		RevokedAt:   utc(d.RevokedAt),
	}
	if d.RevokedAt != nil {
		out.Status = "revoked"
	}
	if d.RevokedBy != nil {
		out.RevokedBy = &personResponse{UserID: d.RevokedBy.UserID, Name: d.RevokedBy.Name}
	}
	return out
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
