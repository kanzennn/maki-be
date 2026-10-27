package services

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// VoucherOrders is the orders, as far as vouchers need them: the sales that
// used one, read in the transaction the context carries. OrderRepository is
// the real one.
type VoucherOrders interface {
	VoucherUses(ctx context.Context, voucherID string, page pagination.Page) ([]models.VoucherUse, int, error)
}

// VoucherService is the Owner's discount vouchers: a percentage or an
// amount off an order's subtotal, for every outlet or some, a number of
// uses across the outlets together, and a period. The Owner manages them;
// an outlet's Manager sees the ones its cashiers can take; the cashier
// checks a code before charging; and the checkout takes a use in the sale's
// own transaction, which a void gives back.
type VoucherService struct {
	db       *config.Database
	vouchers *repositories.VoucherRepository
	outlets  *OutletService
	devices  *DeviceService
	orders   VoucherOrders
	audit    *AuditService
	now      func() time.Time
}

// NewVoucherService returns the vouchers' service. outlets names the
// outlets a voucher is for; devices checks the cashier's device; now is the
// clock a voucher's status is read at.
func NewVoucherService(db *config.Database, vouchers *repositories.VoucherRepository, outlets *OutletService,
	devices *DeviceService, orders VoucherOrders, audit *AuditService, now func() time.Time,
) *VoucherService {
	return &VoucherService{db: db, vouchers: vouchers, outlets: outlets, devices: devices, orders: orders, audit: audit, now: now}
}

// List returns a page of the vouchers, the ones that run out last first,
// and how many match in all.
func (s *VoucherService) List(ctx context.Context, f models.VoucherFilter, page pagination.Page) ([]models.Voucher, int, error) {
	return s.list(ctx, nil, f, page)
}

// ListForOutlet is List for the outlet's Manager: only the vouchers its
// cashier can take, whatever their status, and of their outlets only this
// one.
func (s *VoucherService) ListForOutlet(ctx context.Context, outletID string, f models.VoucherFilter, page pagination.Page) ([]models.Voucher, int, error) {
	return s.list(ctx, &outletID, f, page)
}

func (s *VoucherService) list(ctx context.Context, outletID *string, f models.VoucherFilter, page pagination.Page) ([]models.Voucher, int, error) {
	out, total, err := s.vouchers.List(ctx, outletID, f, s.now(), page)
	if err != nil {
		return nil, 0, err
	}
	if err := s.nameOutlets(ctx, outletID, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// Get returns the voucher, with its outlets.
func (s *VoucherService) Get(ctx context.Context, voucherID string) (models.Voucher, error) {
	v, err := s.vouchers.Get(ctx, voucherID, s.now())
	if err != nil {
		return models.Voucher{}, err
	}
	out := []models.Voucher{v}
	if err := s.nameOutlets(ctx, nil, out); err != nil {
		return models.Voucher{}, err
	}
	return out[0], nil
}

// nameOutlets fills in the outlets of the vouchers that aren't for all of
// them, by name; deleted ones sell nothing, so they are left out. only, when
// set, is the one outlet to name, for its Manager, who sees no other: the
// vouchers are the ones for it.
func (s *VoucherService) nameOutlets(ctx context.Context, only *string, vouchers []models.Voucher) error {
	var voucherIDs []string
	for _, v := range vouchers {
		if !v.AllOutlets {
			voucherIDs = append(voucherIDs, v.VoucherID)
		}
	}
	if len(voucherIDs) == 0 {
		return nil
	}
	byVoucher := map[string][]string{}
	if only != nil {
		for _, voucherID := range voucherIDs {
			byVoucher[voucherID] = []string{*only}
		}
	} else {
		var err error
		if byVoucher, err = s.vouchers.OutletIDs(ctx, voucherIDs); err != nil {
			return err
		}
	}
	var outletIDs []string
	for _, ids := range byVoucher {
		outletIDs = append(outletIDs, ids...)
	}
	slices.Sort(outletIDs)
	refs, err := s.outlets.Refs(ctx, slices.Compact(outletIDs))
	if err != nil {
		return err
	}
	for i := range vouchers {
		v := &vouchers[i]
		var outlets []models.OutletRef
		for _, outletID := range byVoucher[v.VoucherID] {
			if r, ok := refs[outletID]; ok && !r.Deleted {
				outlets = append(outlets, r)
			}
		}
		slices.SortFunc(outlets, func(a, b models.OutletRef) int {
			return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Code, b.Code))
		})
		v.Outlets = outlets
	}
	return nil
}

// Create adds a voucher, active unless in.Active says otherwise.
func (s *VoucherService) Create(ctx context.Context, p models.Principal, ip string, in models.VoucherInput) (models.Voucher, error) {
	now := s.now()
	next, problems := validations.CheckVoucher(in)
	if problems["expired_at"] == "" && !next.ExpiredAt.After(now) {
		problems["expired_at"] = "future"
	}
	if err := s.checkOutlets(ctx, next, problems); err != nil {
		return models.Voucher{}, err
	}
	if len(problems) > 0 {
		return models.Voucher{}, apperror.Invalid(problems)
	}
	voucherID := id.New()
	active := in.Active == nil || *in.Active
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		if err := s.vouchers.Insert(ctx, voucherID, next, active); err != nil {
			return err
		}
		if err := s.vouchers.AddOutlets(ctx, voucherID, next.OutletIDs); err != nil {
			return err
		}
		after := next.Snapshot()
		after["is_active"] = active
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "voucher.created", UserID: p.UserID, TargetType: "voucher", TargetID: voucherID, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Voucher{}, err
	}
	return s.Get(ctx, voucherID)
}

// Update changes every field of the voucher but its status. Once a sale has
// used it, its code and type stay (locked), its quota doesn't go down
// (decrease) and its expiry doesn't move earlier (earlier); a new expiry is
// in the future either way. A change of nothing records nothing.
func (s *VoucherService) Update(ctx context.Context, p models.Principal, ip, voucherID string, in models.VoucherInput) (models.Voucher, error) {
	next, problems := validations.CheckVoucher(in)
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		now := s.now()
		// Locked first, so a sale under way takes its use before the rules
		// below are checked against it.
		prev, err := s.vouchers.Lock(ctx, voucherID, now)
		if err != nil {
			return err
		}
		if prev.Used {
			noteIf(problems, "code", "locked", next.Code != prev.Code)
			noteIf(problems, "type", "locked", next.Type != prev.Type)
			noteIf(problems, "quota", "decrease", next.Quota < prev.Quota)
			noteIf(problems, "expired_at", "earlier", next.ExpiredAt.Before(prev.ExpiredAt))
		}
		noteIf(problems, "expired_at", "future", !next.ExpiredAt.Equal(prev.ExpiredAt) && !next.ExpiredAt.After(now))
		if err := s.checkOutlets(ctx, next, problems); err != nil {
			return err
		}
		if len(problems) > 0 {
			return apperror.Invalid(problems)
		}
		before, after := changedAsPrinted(prev.Snapshot(), next.Snapshot())
		if len(after) == 0 {
			return nil
		}
		if err := s.vouchers.Update(ctx, voucherID, next); err != nil {
			return err
		}
		if !slices.Equal(prev.OutletIDs, next.OutletIDs) {
			if err := s.vouchers.ClearOutlets(ctx, voucherID); err != nil {
				return err
			}
			if err := s.vouchers.AddOutlets(ctx, voucherID, next.OutletIDs); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "voucher.updated", UserID: p.UserID, TargetType: "voucher", TargetID: voucherID,
			Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Voucher{}, err
	}
	return s.Get(ctx, voucherID)
}

// noteIf notes rule against field when it is broken, unless the field has a
// problem already.
func noteIf(problems map[string]string, field, rule string, broken bool) {
	if broken && problems[field] == "" {
		problems[field] = rule
	}
}

// SetActive turns the voucher on or off, at once: off, no sale can take it,
// and its history stays. Setting it as it is records nothing.
func (s *VoucherService) SetActive(ctx context.Context, p models.Principal, ip, voucherID string, active bool) (models.Voucher, error) {
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.vouchers.Lock(ctx, voucherID, s.now())
		if err != nil {
			return err
		}
		if prev.Active == active {
			return nil
		}
		if err := s.vouchers.SetActive(ctx, voucherID, active); err != nil {
			return err
		}
		action := "voucher.deactivated"
		if active {
			action = "voucher.activated"
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: action, UserID: p.UserID, TargetType: "voucher", TargetID: voucherID, IP: ip,
		})
	})
	if err != nil {
		return models.Voucher{}, err
	}
	return s.Get(ctx, voucherID)
}

// Delete soft-deletes a voucher no sale has used (ErrVoucherUsed: deactivate
// that one instead). Its code is free again.
func (s *VoucherService) Delete(ctx context.Context, p models.Principal, ip, voucherID string) error {
	return s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.vouchers.Lock(ctx, voucherID, s.now())
		if err != nil {
			return err
		}
		if prev.Used {
			return apperror.ErrVoucherUsed
		}
		if err := s.vouchers.Delete(ctx, voucherID); err != nil {
			return err
		}
		before := prev.Snapshot()
		before["is_active"] = prev.Active
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "voucher.deleted", UserID: p.UserID, TargetType: "voucher", TargetID: voucherID,
			Before: before, IP: ip,
		})
	})
}

// Uses returns a page of the sales that used the voucher, newest first,
// voided ones too, and how many there are.
func (s *VoucherService) Uses(ctx context.Context, voucherID string, page pagination.Page) ([]models.VoucherUse, int, error) {
	var uses []models.VoucherUse
	var total int
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := s.vouchers.Get(ctx, voucherID, s.now()); err != nil {
			return err
		}
		var err error
		uses, total, err = s.orders.VoucherUses(ctx, voucherID, page)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	outletIDs := make([]string, len(uses))
	for i, u := range uses {
		outletIDs[i] = u.Outlet.OutletID
	}
	slices.Sort(outletIDs)
	refs, err := s.outlets.Refs(ctx, slices.Compact(outletIDs))
	if err != nil {
		return nil, 0, err
	}
	for i := range uses {
		if r, ok := refs[uses[i].Outlet.OutletID]; ok {
			uses[i].Outlet = r
		}
	}
	return uses, total, nil
}

// checkOutlets notes in problems an outlet of the voucher that doesn't
// exist or was deleted.
func (s *VoucherService) checkOutlets(ctx context.Context, v models.VoucherFields, problems map[string]string) error {
	if len(v.OutletIDs) == 0 {
		return nil
	}
	refs, err := s.outlets.Refs(ctx, v.OutletIDs)
	if err != nil {
		return err
	}
	for _, outletID := range v.OutletIDs {
		if r, ok := refs[outletID]; !ok || r.Deleted {
			problems["outlet_ids"] = "exists"
		}
	}
	return nil
}

// Check is the cashier checking the code a customer gave, before charging,
// from one of the outlet's devices (token): the voucher's terms, for the
// device to work its discount out again as the cart changes, and its
// discount on the cart's subtotal now. A voucher the sale can't take is an
// error, as Redeem's would be; a subtotal short of its minimum spend isn't
// yet, as the cart may still grow (Applies is false).
func (s *VoucherService) Check(ctx context.Context, outletID, token, code string, subtotal *int64) (models.VoucherQuote, error) {
	if _, err := s.devices.Current(ctx, outletID, token); err != nil {
		return models.VoucherQuote{}, err
	}
	code, err := validations.CheckVoucherLookup(code, subtotal)
	if err != nil {
		return models.VoucherQuote{}, err
	}
	v, err := s.vouchers.FindByCode(ctx, code, s.now())
	if err != nil {
		return models.VoucherQuote{}, err
	}
	if err := s.usable(ctx, outletID, v); err != nil {
		return models.VoucherQuote{}, err
	}
	discount, applies := voucherDiscount(v.VoucherTerms, *subtotal)
	return models.VoucherQuote{Voucher: v, Discount: discount, Applies: applies}, nil
}

// Redeem takes one of the voucher's uses for a sale in the outlet, in the
// sale's transaction, the one ctx carries, as the last of its locks: it
// holds the voucher until the sale commits, so two sales after its last use
// go one after the other, and the second finds it used up. A voucher the
// sale can't take is an error, as at Check, and so is a subtotal short of
// its minimum spend (ErrVoucherMinSpend, with details.min_spend).
func (s *VoucherService) Redeem(ctx context.Context, outletID, code string, subtotal int64) (models.VoucherTaken, error) {
	var taken models.VoucherTaken
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		now := s.now()
		v, err := s.vouchers.LockByCode(ctx, validations.NormalizeVoucherCode(code), now)
		if err != nil {
			return err
		}
		if err := s.usable(ctx, outletID, v); err != nil {
			return err
		}
		discount, applies := voucherDiscount(v.VoucherTerms, subtotal)
		if !applies {
			return apperror.ErrVoucherMinSpend.WithDetails(map[string]any{"min_spend": *v.MinSpend})
		}
		if err := s.vouchers.TakeUse(ctx, v.VoucherID, now); err != nil {
			return err
		}
		taken = models.VoucherTaken{VoucherID: v.VoucherID, Code: v.Code, Discount: discount}
		return nil
	})
	return taken, err
}

// Release gives a voided sale's use of the voucher back, in the void's
// transaction, the one ctx carries. The voucher stays used.
func (s *VoucherService) Release(ctx context.Context, voucherID string) error {
	return s.vouchers.ReturnUse(ctx, voucherID)
}

// usable tells what keeps the voucher from a sale in the outlet now: first
// an outlet it isn't for, then its status.
func (s *VoucherService) usable(ctx context.Context, outletID string, v models.Voucher) error {
	if !v.AllOutlets {
		ok, err := s.vouchers.IsForOutlet(ctx, v.VoucherID, outletID)
		if err != nil {
			return err
		}
		if !ok {
			return apperror.ErrVoucherNotForOutlet
		}
	}
	switch v.Status {
	case models.VoucherInactive:
		return apperror.ErrVoucherInactive
	case models.VoucherExpired:
		return apperror.ErrVoucherExpired
	case models.VoucherUsedUp:
		return apperror.ErrVoucherUsedUp
	case models.VoucherScheduled:
		return apperror.ErrVoucherNotStarted
	}
	return nil
}

// voucherDiscount works out a voucher's discount on subtotal, the same way
// the web and Android cashiers do (api/test-vectors/voucher-discounts.json),
// before the service charge and the tax. applies is false for a subtotal
// below the minimum spend, which gets nothing.
//
// A percentage is rounded to the nearest rupiah, half up, as the order's
// service and tax are, then held to MaxDiscount; either kind is held to the
// subtotal, so a total never goes below zero. A subtotal is at most a
// billion and a percentage at most 100%, so nothing overflows.
func voucherDiscount(t models.VoucherTerms, subtotal int64) (discount int64, applies bool) {
	if t.MinSpend != nil && subtotal < *t.MinSpend {
		return 0, false
	}
	switch t.Type {
	case models.VoucherPercent:
		discount = (subtotal*t.Value + 5000) / 10000
		if t.MaxDiscount != nil {
			discount = min(discount, *t.MaxDiscount)
		}
	case models.VoucherAmount:
		discount = t.Value
	}
	return min(discount, subtotal), true
}
