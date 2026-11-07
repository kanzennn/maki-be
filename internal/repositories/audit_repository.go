package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"time"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/audit"
	"github.com/kervzent/maki-be/internal/utils/pagination"
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

// List returns a page of the entries the filter leaves, newest first, and how
// many there are. Who, where and which device are named by ID only.
func (r *AuditRepository) List(ctx context.Context, f models.LogFilter, page pagination.Page) ([]models.ActionLog, int, error) {
	q := store.New(r.db.Conn(ctx))
	var fromAt, toAt *time.Time
	if !f.From.IsZero() {
		fromAt = &f.From
	}
	if !f.To.IsZero() {
		toAt = &f.To
	}
	userID := optional(f.UserID)
	total, err := q.CountActionLogs(ctx, store.CountActionLogsParams{
		FromAt: fromAt, ToAt: toAt, Actions: f.Actions, OutletIds: f.OutletIDs, UserID: userID,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count the Log Action: %w", err)
	}
	rows, err := q.ListActionLogs(ctx, store.ListActionLogsParams{
		FromAt: fromAt, ToAt: toAt, Actions: f.Actions, OutletIds: f.OutletIDs, UserID: userID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list the Log Action: %w", err)
	}
	out := make([]models.ActionLog, len(rows))
	for i, row := range rows {
		out[i] = models.ActionLog{
			ActionLogID: row.ActionLogID, OutletID: row.OutletID, UserID: row.UserID, DeviceID: row.DeviceID,
			Action: row.Action, TargetType: row.TargetType, TargetID: row.TargetID,
			Before: row.BeforeJSON, After: row.AfterJSON, OccurredAt: row.OccurredAt, CreatedAt: row.CreatedAt,
		}
		if row.IP != nil {
			ip := row.IP.String()
			out[i].IP = &ip
		}
	}
	return out, int(total), nil
}
