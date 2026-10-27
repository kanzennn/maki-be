package models

import "time"

// AuditEntry is one Log Action entry to record.
type AuditEntry struct {
	// Action names what happened as <resource>.<action>, e.g. auth.login.
	Action string
	// UserID is who did it; empty when nobody is known, such as a failed
	// sign-in with an unknown username.
	UserID string
	// OutletID is the outlet it happened in; empty for the Owner's actions.
	OutletID string
	DeviceID string
	// TargetType and TargetID name what it happened to, e.g. "menu" and
	// its ID.
	TargetType string
	TargetID   string
	// Before and After are stored as JSON, as they are — never translated.
	// Nil stores nothing.
	Before, After any
	// IP is the client's address; empty when there is none, as for a
	// command run on the server.
	IP string
	// OccurredAt is when it happened: the device's clock for actions made
	// offline. Zero means now.
	OccurredAt time.Time
}
