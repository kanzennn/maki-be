package services

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/id"
)

// AuditService is the Log Action. The other services record what happened
// through it, in the same transaction as the action itself — the one their
// context carries — so a change and its log entry commit or roll back
// together.
//
// Reading the log (the Log Action screens for the Owner and each outlet)
// comes here too, with the log endpoints.
type AuditService struct {
	logs *repositories.AuditRepository
	now  func() time.Time
}

// NewAuditService returns the Log Action's service.
func NewAuditService(logs *repositories.AuditRepository) *AuditService {
	return &AuditService{logs: logs, now: time.Now}
}

// Record writes e in the transaction ctx carries: the entry exists only if
// that commits. An entry needs an action; it happened now unless it says
// when.
func (s *AuditService) Record(ctx context.Context, e models.AuditEntry) error {
	if e.Action == "" {
		return errors.New("audit: an entry needs an action")
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = s.now()
	}
	return s.logs.Insert(ctx, id.New(), e)
}

// changedFields returns the fields that differ, before and after: what the
// Log Action entry of a change records.
func changedFields(prev, next map[string]any) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	for k, v := range next {
		if !reflect.DeepEqual(derefAny(prev[k]), derefAny(v)) {
			before[k], after[k] = prev[k], v
		}
	}
	return before, after
}

// changedAsPrinted is changedFields comparing the values as fmt prints
// them. Outlets, menus and vouchers record their changes this way.
func changedAsPrinted(prev, next map[string]any) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	for k, v := range next {
		if fmt.Sprint(derefAny(prev[k])) != fmt.Sprint(derefAny(v)) {
			before[k], after[k] = prev[k], v
		}
	}
	return before, after
}

// derefAny shows a *string as its value, so two pointers to the same text
// compare equal.
func derefAny(v any) any {
	if s, ok := v.(*string); ok {
		if s == nil {
			return nil
		}
		return *s
	}
	return v
}
