// Package payment is Payment Control: which payment methods each outlet's
// cashier offers. The methods are fixed here; each outlet has a row per
// method saying whether it is on. It owns the outlet_payment_methods table.
package payment

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/payment/store"
)

// The payment methods there are. Cash is the one that works offline, so it
// stays on; QRIS and debit come with Next Dev, and stay off until then.
const (
	Cash  = "cash"
	QRIS  = "qris"
	Debit = "debit"
)

// Service reads and sets an outlet's payment methods.
type Service struct {
	pool *pgxpool.Pool
}

// NewService returns the payment module's service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// CreateDefaults gives a new outlet its payment methods, in the transaction
// that creates it: cash on, the others off.
func (s *Service) CreateDefaults(ctx context.Context, tx pgx.Tx, outletID string) error {
	if err := store.New(tx).InsertDefaultMethods(ctx, outletID); err != nil {
		return fmt.Errorf("create the payment methods of %s: %w", outletID, err)
	}
	return nil
}

// Enabled returns the methods the outlet's cashier offers, in order.
func (s *Service) Enabled(ctx context.Context, outletID string) ([]string, error) {
	methods, err := store.New(s.pool).ListEnabledMethods(ctx, outletID)
	if err != nil {
		return nil, fmt.Errorf("list the payment methods of %s: %w", outletID, err)
	}
	return methods, nil
}

// Accepts reports whether the outlet's cashier takes the method, read in
// the sale's transaction.
func (s *Service) Accepts(ctx context.Context, tx pgx.Tx, outletID, method string) (bool, error) {
	ok, err := store.New(tx).MethodEnabled(ctx, store.MethodEnabledParams{OutletID: outletID, Method: method})
	if err != nil {
		return false, fmt.Errorf("check payment method %s of %s: %w", method, outletID, err)
	}
	return ok, nil
}
