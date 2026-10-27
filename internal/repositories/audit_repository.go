package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/audit"
)

// AuditRepository owns action_logs, the Log Action entries. The rows are
// append-only: the database refuses to update or delete them.
type AuditRepository struct {
	db *config.Database
}

// NewAuditRepository returns the Log Action's repository.
func NewAuditRepository(db *config.Database) *AuditRepository {
	return &AuditRepository{db: db}
}

// Insert writes the entry under actionLogID, in the transaction ctx carries:
// it exists only if that commits. Its IP must be an address, and its before
// and after values JSON.
func (r *AuditRepository) Insert(ctx context.Context, actionLogID string, e models.AuditEntry) error {
	params := store.InsertActionLogParams{
		ActionLogID: actionLogID,
		OutletID:    optional(e.OutletID),
		UserID:      optional(e.UserID),
		DeviceID:    optional(e.DeviceID),
		Action:      e.Action,
		TargetType:  optional(e.TargetType),
		TargetID:    optional(e.TargetID),
		OccurredAt:  e.OccurredAt,
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
	if err := store.New(r.db.Conn(ctx)).InsertActionLog(ctx, params); err != nil {
		return fmt.Errorf("audit: record %s: %w", e.Action, err)
	}
	return nil
}

// optional is s for a nullable column: nil, SQL NULL, when it is empty.
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
