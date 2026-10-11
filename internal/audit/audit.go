// Package audit is the Log Action module. Other modules record what happened
// through Recorder, in the same transaction as the action itself, so a change
// and its log entry commit or roll back together. The rows are append-only:
// the database refuses to update or delete them.
//
// Reading the log (the Log Action screens for the Owner and each outlet)
// comes here too, with the log endpoints.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/audit/store"
	"github.com/kervzent/maki-be/internal/platform/id"
)

// Entry is one action to record.
type Entry struct {
	// Action names what happened as <module>.<action>, e.g. auth.login.
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

// Recorder writes Log Action entries. Modules take it as an interface of
// their own with this method, so their tests can use a fake.
type Recorder struct{}

// Record writes e as part of tx: the entry exists only if tx commits.
func (Recorder) Record(ctx context.Context, tx pgx.Tx, e Entry) error {
	if e.Action == "" {
		return errors.New("audit: an entry needs an action")
	}
	params := store.InsertActionLogParams{
		ActionLogID: id.New(),
		OutletID:    optional(e.OutletID),
		UserID:      optional(e.UserID),
		DeviceID:    optional(e.DeviceID),
		Action:      e.Action,
		TargetType:  optional(e.TargetType),
		TargetID:    optional(e.TargetID),
		OccurredAt:  e.OccurredAt,
	}
	if params.OccurredAt.IsZero() {
		params.OccurredAt = time.Now()
	}
	if e.IP != "" {
		ip, err := netip.ParseAddr(e.IP)
		if err != nil {
			return fmt.Errorf("audit: IP %q: %w", e.IP, err)
		}
		params.IP = &ip
	}
	var err error
	if params.BeforeJSON, err = marshal(e.Before); err != nil {
		return fmt.Errorf("audit: %s before: %w", e.Action, err)
	}
	if params.AfterJSON, err = marshal(e.After); err != nil {
		return fmt.Errorf("audit: %s after: %w", e.Action, err)
	}
	if err := store.New(tx).InsertActionLog(ctx, params); err != nil {
		return fmt.Errorf("audit: record %s: %w", e.Action, err)
	}
	return nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// marshal returns v as JSON, or nil — SQL NULL — for a nil v.
func marshal(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}
