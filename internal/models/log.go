package models

import (
	"encoding/json"
	"time"
)

// LogFilter narrows the Log Action. Zero values don't narrow.
type LogFilter struct {
	From, To time.Time // from From, up to but not including To
	Actions  []string
	// OutletIDs is the outlets whose entries to show.
	OutletIDs []string
	UserID    string
}

// LogUser is who an entry says acted.
type LogUser struct {
	UserID    string
	Name      string
	IsOwner   bool
	IsManager bool
	RoleName  *string
}

// LogDevice is the device an entry says it came from.
type LogDevice struct {
	DeviceID string
	Code     string
	Name     string
}

// ActionLog is one Log Action entry as it reads back. Who, where and which
// device are nil when the entry names none.
type ActionLog struct {
	ActionLogID string
	OutletID    *string
	UserID      *string
	DeviceID    *string
	Action      string
	TargetType  *string
	TargetID    *string
	Before      json.RawMessage
	After       json.RawMessage
	IP          *string
	OccurredAt  time.Time
	CreatedAt   time.Time

	User   *LogUser
	Outlet *OutletRef
	Device *LogDevice
}

// LogUserResponse is who acted.
type LogUserResponse struct {
	UserID    string  `json:"user_id"`
	Name      string  `json:"name"`
	IsOwner   bool    `json:"is_owner"`
	IsManager bool    `json:"is_manager"`
	RoleName  *string `json:"role_name"`
}

// LogOutletResponse is the outlet an entry happened in.
type LogOutletResponse struct {
	OutletID string `json:"outlet_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
}

// LogDeviceResponse is the device an entry came from.
type LogDeviceResponse struct {
	DeviceID string `json:"device_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
}

// ActionLogResponse is an entry in the Log Action's lists.
type ActionLogResponse struct {
	ActionLogID string             `json:"action_log_id"`
	Action      string             `json:"action"`
	TargetType  *string            `json:"target_type"`
	TargetID    *string            `json:"target_id"`
	User        *LogUserResponse   `json:"user"`
	Outlet      *LogOutletResponse `json:"outlet"`
	Device      *LogDeviceResponse `json:"device"`
	Before      json.RawMessage    `json:"before"`
	After       json.RawMessage    `json:"after"`
	IP          *string            `json:"ip"`
	OccurredAt  time.Time          `json:"occurred_at"`
	CreatedAt   time.Time          `json:"created_at"`
}

// NewActionLogResponses returns the entries' responses, their times in UTC.
func NewActionLogResponses(logs []ActionLog) []ActionLogResponse {
	out := make([]ActionLogResponse, len(logs))
	for i, l := range logs {
		r := ActionLogResponse{
			ActionLogID: l.ActionLogID, Action: l.Action, TargetType: l.TargetType, TargetID: l.TargetID,
			Before: l.Before, After: l.After, IP: l.IP,
			OccurredAt: l.OccurredAt.UTC(), CreatedAt: l.CreatedAt.UTC(),
		}
		if l.User != nil {
			u := LogUserResponse(*l.User)
			r.User = &u
		}
		if l.Outlet != nil {
			r.Outlet = &LogOutletResponse{OutletID: l.Outlet.OutletID, Code: l.Outlet.Code, Name: l.Outlet.Name}
		}
		if l.Device != nil {
			d := LogDeviceResponse(*l.Device)
			r.Device = &d
		}
		out[i] = r
	}
	return out
}
