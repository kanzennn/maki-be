package voucher

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/voucher/store"
)

// Check is the cashier checking the code a customer gave, before charging,
// from one of the outlet's devices (token): the voucher's terms, for the
// device to work its discount out again as the cart changes, and its
// discount on the cart's subtotal now. A voucher the sale can't take is an
// error, as Redeem's would be; a subtotal short of its minimum spend isn't
// yet, as the cart may still grow (Quote.Applies is false).
func (s *Service) Check(ctx context.Context, outletID, token, code string, subtotal *int64) (Quote, error) {
	if _, err := s.devices.Current(ctx, outletID, token); err != nil {
		return Quote{}, err
	}
	code = NormalizeCode(code)
	problems := map[string]string{}
	if code == "" {
		problems["code"] = "required"
	}
	switch {
	case subtotal == nil:
		problems["subtotal"] = "required"
	case *subtotal < 0 || *subtotal > MaxAmount:
		problems["subtotal"] = "range"
	}
	if len(problems) > 0 {
		return Quote{}, httpx.Invalid(problems)
	}
	q := store.New(s.pool)
	r, err := q.FindVoucherByCode(ctx, store.FindVoucherByCodeParams{Code: code, At: s.now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return Quote{}, ErrVoucherNotFound
	}
	if err != nil {
		return Quote{}, fmt.Errorf("find voucher %s: %w", code, err)
	}
	v := voucher(store.GetVoucherRow(r))
	if err := usable(ctx, q, outletID, v); err != nil {
		return Quote{}, err
	}
	discount, applies := v.Discount(*subtotal)
	return Quote{Voucher: v, Discount: discount, Applies: applies}, nil
}

// Redeem takes one of the voucher's uses for a sale in the outlet, in the
// sale's transaction, as the last of its locks: it holds the voucher until
// the sale commits, so two sales after its last use go one after the other,
// and the second finds it used up. A voucher the sale can't take is an
// error, as at Check, and so is a subtotal short of its minimum spend
// (ErrVoucherMinSpend, with details.min_spend).
func (s *Service) Redeem(ctx context.Context, tx pgx.Tx, outletID, code string, subtotal int64) (Taken, error) {
	q := store.New(tx)
	now := s.now()
	r, err := q.LockVoucherByCode(ctx, store.LockVoucherByCodeParams{Code: NormalizeCode(code), At: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return Taken{}, ErrVoucherNotFound
	}
	if err != nil {
		return Taken{}, fmt.Errorf("lock voucher %s: %w", code, err)
	}
	v := voucher(store.GetVoucherRow(r))
	if err := usable(ctx, q, outletID, v); err != nil {
		return Taken{}, err
	}
	discount, applies := v.Discount(subtotal)
	if !applies {
		return Taken{}, ErrVoucherMinSpend.WithDetails(map[string]any{"min_spend": *v.MinSpend})
	}
	if err := q.TakeVoucherUse(ctx, store.TakeVoucherUseParams{VoucherID: v.VoucherID, At: &now}); err != nil {
		return Taken{}, fmt.Errorf("take a use of voucher %s: %w", v.VoucherID, err)
	}
	return Taken{VoucherID: v.VoucherID, Code: v.Code, Discount: discount}, nil
}

// Release gives a voided sale's use of the voucher back, in the void's
// transaction. The voucher stays used.
func (s *Service) Release(ctx context.Context, tx pgx.Tx, voucherID string) error {
	if err := store.New(tx).ReturnVoucherUse(ctx, voucherID); err != nil {
		return fmt.Errorf("return a use of voucher %s: %w", voucherID, err)
	}
	return nil
}

// usable tells what keeps the voucher from a sale in the outlet now: first
// an outlet it isn't for, then its status.
func usable(ctx context.Context, q *store.Queries, outletID string, v Voucher) error {
	if !v.AllOutlets {
		ok, err := q.VoucherIsForOutlet(ctx, store.VoucherIsForOutletParams{VoucherID: v.VoucherID, OutletID: outletID})
		if err != nil {
			return fmt.Errorf("find the outlets of voucher %s: %w", v.VoucherID, err)
		}
		if !ok {
			return ErrVoucherNotForOutlet
		}
	}
	switch v.Status {
	case StatusInactive:
		return ErrVoucherInactive
	case StatusExpired:
		return ErrVoucherExpired
	case StatusUsedUp:
		return ErrVoucherUsedUp
	case StatusScheduled:
		return ErrVoucherNotStarted
	}
	return nil
}
