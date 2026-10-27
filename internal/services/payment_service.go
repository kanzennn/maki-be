// Package services holds the business rules, and the transactions that
// group a resource's writes: each service validates what it is given, then
// works through its repository and the services of the resources it needs.
// It knows nothing of HTTP or SQL; its errors are those of utils/apperror.
package services

import (
	"context"

	"github.com/kervzent/maki-be/internal/repositories"
)

// PaymentService is Payment Control: which payment methods each outlet's
// cashier offers. The methods are fixed (models.PaymentCash, …); each outlet
// has a row per method saying whether it is on.
type PaymentService struct {
	payments *repositories.PaymentRepository
}

// NewPaymentService returns the payment methods' service.
func NewPaymentService(payments *repositories.PaymentRepository) *PaymentService {
	return &PaymentService{payments: payments}
}

// CreateDefaults gives a new outlet its payment methods, in the transaction
// that creates it: cash on, the others off.
func (s *PaymentService) CreateDefaults(ctx context.Context, outletID string) error {
	return s.payments.InsertDefaults(ctx, outletID)
}

// Enabled returns the methods the outlet's cashier offers, in order.
func (s *PaymentService) Enabled(ctx context.Context, outletID string) ([]string, error) {
	return s.payments.ListEnabled(ctx, outletID)
}

// Accepts reports whether the outlet's cashier takes the method, read in
// the sale's transaction.
func (s *PaymentService) Accepts(ctx context.Context, outletID, method string) (bool, error) {
	return s.payments.IsEnabled(ctx, outletID, method)
}
