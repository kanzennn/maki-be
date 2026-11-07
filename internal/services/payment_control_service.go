package services

import (
	"context"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
)

// PaymentControlService is the Manager's side of Payment Control: seeing the
// outlet's methods and turning them on and off.
type PaymentControlService struct {
	db       *config.Database
	payments *repositories.PaymentRepository
	audit    *AuditService
}

// NewPaymentControlService returns Payment Control's service.
func NewPaymentControlService(db *config.Database, payments *repositories.PaymentRepository, audit *AuditService) *PaymentControlService {
	return &PaymentControlService{db: db, payments: payments, audit: audit}
}

// Methods returns the outlet's methods, in the cashier's order.
func (s *PaymentControlService) Methods(ctx context.Context, outletID string) ([]models.PaymentMethod, error) {
	return s.payments.List(ctx, outletID)
}

// SetMethods applies the changes and returns the methods as they are. Cash
// stays on (ErrPaymentMethodRequired), which also keeps one method on, and a
// method not released yet can't be turned on (ErrPaymentMethodComingSoon).
// Only what changes is written and recorded, as payment.updated.
func (s *PaymentControlService) SetMethods(ctx context.Context, p models.Principal, ip, outletID string, changes []models.PaymentMethod) ([]models.PaymentMethod, error) {
	for _, c := range changes {
		if c.Method == models.PaymentCash && !c.Enabled {
			return nil, apperror.ErrPaymentMethodRequired
		}
		if c.Enabled && !models.PaymentMethodReleased(c.Method) {
			return nil, apperror.ErrPaymentMethodComingSoon
		}
	}
	var out []models.PaymentMethod
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		current, err := s.payments.Lock(ctx, outletID)
		if err != nil {
			return err
		}
		before, after := map[string]any{}, map[string]any{}
		for _, c := range changes {
			for i := range current {
				if current[i].Method != c.Method || current[i].Enabled == c.Enabled {
					continue
				}
				before[c.Method], after[c.Method] = current[i].Enabled, c.Enabled
				current[i].Enabled = c.Enabled
				if err := s.payments.Set(ctx, outletID, c); err != nil {
					return err
				}
			}
		}
		out = current
		if len(after) == 0 {
			return nil
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "payment.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "outlet", TargetID: outletID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
