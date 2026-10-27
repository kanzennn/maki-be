// Package repositories holds the only code that touches the database: one
// repository per resource, each wrapping that resource's queries, which sqlc
// generates into store/<resource>. A repository runs its queries in the
// transaction its context carries, if any, returns the models, and turns the
// database's answers — no row, a taken name — into the resource's errors.
package repositories

import (
	"context"
	"fmt"

	"github.com/kervzent/maki-be/internal/config"
	store "github.com/kervzent/maki-be/internal/repositories/store/payment"
)

// PaymentRepository owns outlet_payment_methods: a row per outlet and
// method, saying whether the method is on.
type PaymentRepository struct {
	db *config.Database
}

// NewPaymentRepository returns the payment methods' repository.
func NewPaymentRepository(db *config.Database) *PaymentRepository {
	return &PaymentRepository{db: db}
}

// InsertDefaults gives an outlet its methods: cash on, the others off.
func (r *PaymentRepository) InsertDefaults(ctx context.Context, outletID string) error {
	if err := store.New(r.db.Conn(ctx)).InsertDefaultMethods(ctx, outletID); err != nil {
		return fmt.Errorf("create the payment methods of %s: %w", outletID, err)
	}
	return nil
}

// ListEnabled returns the methods the outlet's cashier offers, in order.
func (r *PaymentRepository) ListEnabled(ctx context.Context, outletID string) ([]string, error) {
	methods, err := store.New(r.db.Conn(ctx)).ListEnabledMethods(ctx, outletID)
	if err != nil {
		return nil, fmt.Errorf("list the payment methods of %s: %w", outletID, err)
	}
	return methods, nil
}

// IsEnabled reports whether the outlet's cashier takes the method.
func (r *PaymentRepository) IsEnabled(ctx context.Context, outletID, method string) (bool, error) {
	ok, err := store.New(r.db.Conn(ctx)).MethodEnabled(ctx, store.MethodEnabledParams{OutletID: outletID, Method: method})
	if err != nil {
		return false, fmt.Errorf("check payment method %s of %s: %w", method, outletID, err)
	}
	return ok, nil
}
