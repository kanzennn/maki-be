package voucher

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/outlet"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/voucher/store"
)

// List returns a page of the vouchers, the ones that run out last first,
// and how many match in all.
func (s *Service) List(ctx context.Context, f Filter, page httpx.Page) ([]Voucher, int, error) {
	return s.list(ctx, nil, f, page)
}

// ListForOutlet is List for the outlet's Manager: only the vouchers its
// cashier can take, whatever their status, and of their outlets only this
// one.
func (s *Service) ListForOutlet(ctx context.Context, outletID string, f Filter, page httpx.Page) ([]Voucher, int, error) {
	return s.list(ctx, &outletID, f, page)
}

func (s *Service) list(ctx context.Context, outletID *string, f Filter, page httpx.Page) ([]Voucher, int, error) {
	var status, search *string
	if f.Status != "" {
		status = &f.Status
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		escaped := likeEscaper.Replace(q)
		search = &escaped
	}
	at := s.now()
	q := store.New(s.pool)
	total, err := q.CountVouchers(ctx, store.CountVouchersParams{At: at, OutletID: outletID, Status: status, Search: search})
	if err != nil {
		return nil, 0, fmt.Errorf("count vouchers: %w", err)
	}
	rows, err := q.ListVouchers(ctx, store.ListVouchersParams{
		At: at, OutletID: outletID, Status: status, Search: search,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list vouchers: %w", err)
	}
	out := make([]Voucher, len(rows))
	for i, r := range rows {
		out[i] = voucher(store.GetVoucherRow(r))
	}
	if err := s.nameOutlets(ctx, q, outletID, out); err != nil {
		return nil, 0, err
	}
	return out, int(total), nil
}

// likeEscaper keeps LIKE's wildcards in a search literal.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Get returns the voucher, with its outlets.
func (s *Service) Get(ctx context.Context, voucherID string) (Voucher, error) {
	q := store.New(s.pool)
	r, err := q.GetVoucher(ctx, store.GetVoucherParams{VoucherID: voucherID, At: s.now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return Voucher{}, ErrVoucherNotFound
	}
	if err != nil {
		return Voucher{}, fmt.Errorf("load voucher %s: %w", voucherID, err)
	}
	out := []Voucher{voucher(r)}
	if err := s.nameOutlets(ctx, q, nil, out); err != nil {
		return Voucher{}, err
	}
	return out[0], nil
}

func voucher(r store.GetVoucherRow) Voucher {
	return Voucher{
		VoucherID: r.VoucherID, Code: r.Code, Name: r.Name,
		Terms: Terms{Type: r.Type, Value: r.Value, MaxDiscount: r.MaxDiscount, MinSpend: r.MinSpend},
		Quota: int(r.Quota), UsedCount: int(r.UsedCount), StartAt: r.StartAt, ExpiredAt: r.ExpiredAt,
		AllOutlets: r.AllOutlets, Active: r.IsActive, Status: r.Status, FirstUsedAt: r.FirstUsedAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// nameOutlets fills in the outlets of the vouchers that aren't for all of
// them, by name; deleted ones sell nothing, so they are left out. only, when
// set, is the one outlet to name, for its Manager, who sees no other: the
// vouchers are the ones for it.
func (s *Service) nameOutlets(ctx context.Context, q *store.Queries, only *string, vouchers []Voucher) error {
	var voucherIDs []string
	for _, v := range vouchers {
		if !v.AllOutlets {
			voucherIDs = append(voucherIDs, v.VoucherID)
		}
	}
	if len(voucherIDs) == 0 {
		return nil
	}
	var pairs []store.VoucherOutlet
	if only != nil {
		for _, voucherID := range voucherIDs {
			pairs = append(pairs, store.VoucherOutlet{VoucherID: voucherID, OutletID: *only})
		}
	} else {
		var err error
		if pairs, err = q.ListVoucherOutlets(ctx, voucherIDs); err != nil {
			return fmt.Errorf("list the outlets of vouchers: %w", err)
		}
	}
	outletIDs := make([]string, len(pairs))
	for i, p := range pairs {
		outletIDs[i] = p.OutletID
	}
	slices.Sort(outletIDs)
	refs, err := s.outlets.Refs(ctx, slices.Compact(outletIDs))
	if err != nil {
		return err
	}
	byVoucher := map[string][]outlet.Ref{}
	for _, p := range pairs {
		if r, ok := refs[p.OutletID]; ok && !r.Deleted {
			byVoucher[p.VoucherID] = append(byVoucher[p.VoucherID], r)
		}
	}
	for i := range vouchers {
		outlets := byVoucher[vouchers[i].VoucherID]
		slices.SortFunc(outlets, func(a, b outlet.Ref) int {
			return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Code, b.Code))
		})
		vouchers[i].Outlets = outlets
	}
	return nil
}

// Create adds a voucher, active unless in.Active says otherwise.
func (s *Service) Create(ctx context.Context, p authn.Principal, ip string, in Input) (Voucher, error) {
	now := s.now()
	next, problems := checkVoucher(in)
	if problems["expired_at"] == "" && !next.ExpiredAt.After(now) {
		problems["expired_at"] = "future"
	}
	if err := s.checkOutlets(ctx, next, problems); err != nil {
		return Voucher{}, err
	}
	if len(problems) > 0 {
		return Voucher{}, httpx.Invalid(problems)
	}
	voucherID := id.New()
	active := in.Active == nil || *in.Active
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := codeTaken(q.InsertVoucher(ctx, store.InsertVoucherParams{
			VoucherID: voucherID, Code: next.Code, Name: next.Name, Type: next.Type, Value: next.Value,
			MaxDiscount: next.MaxDiscount, MinSpend: next.MinSpend, Quota: int32(next.Quota), //nolint:gosec // at most MaxQuota
			StartAt: next.StartAt, ExpiredAt: next.ExpiredAt, AllOutlets: next.AllOutlets, IsActive: active,
		})); err != nil {
			return err
		}
		if err := insertOutlets(ctx, q, voucherID, next.OutletIDs); err != nil {
			return err
		}
		after := next.snapshot()
		after["is_active"] = active
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "voucher.created", UserID: p.UserID, TargetType: "voucher", TargetID: voucherID, After: after, IP: ip,
		})
	})
	if err != nil {
		return Voucher{}, err
	}
	return s.Get(ctx, voucherID)
}

// Update changes every field of the voucher but its status. Once a sale has
// used it, its code and type stay (locked), its quota doesn't go down
// (decrease) and its expiry doesn't move earlier (earlier); a new expiry is
// in the future either way. A change of nothing records nothing.
func (s *Service) Update(ctx context.Context, p authn.Principal, ip, voucherID string, in Input) (Voucher, error) {
	next, problems := checkVoucher(in)
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		now := s.now()
		// Locked first, so a sale under way takes its use before the rules
		// below are checked against it.
		prev, err := lockVoucher(ctx, q, voucherID, now)
		if err != nil {
			return err
		}
		if prev.used {
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
			return httpx.Invalid(problems)
		}
		before, after := changes(prev.snapshot(), next.snapshot())
		if len(after) == 0 {
			return nil
		}
		if err := codeTaken(q.UpdateVoucher(ctx, store.UpdateVoucherParams{
			VoucherID: voucherID, Code: next.Code, Name: next.Name, Type: next.Type, Value: next.Value,
			MaxDiscount: next.MaxDiscount, MinSpend: next.MinSpend, Quota: int32(next.Quota), //nolint:gosec // at most MaxQuota
			StartAt: next.StartAt, ExpiredAt: next.ExpiredAt, AllOutlets: next.AllOutlets,
		})); err != nil {
			return err
		}
		if !slices.Equal(prev.OutletIDs, next.OutletIDs) {
			if err := q.DeleteVoucherOutlets(ctx, voucherID); err != nil {
				return fmt.Errorf("clear the outlets of voucher %s: %w", voucherID, err)
			}
			if err := insertOutlets(ctx, q, voucherID, next.OutletIDs); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "voucher.updated", UserID: p.UserID, TargetType: "voucher", TargetID: voucherID,
			Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return Voucher{}, err
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
func (s *Service) SetActive(ctx context.Context, p authn.Principal, ip, voucherID string, active bool) (Voucher, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockVoucher(ctx, q, voucherID, s.now())
		if err != nil {
			return err
		}
		if prev.active == active {
			return nil
		}
		if err := q.SetVoucherActive(ctx, store.SetVoucherActiveParams{VoucherID: voucherID, IsActive: active}); err != nil {
			return fmt.Errorf("set voucher %s active: %w", voucherID, err)
		}
		action := "voucher.deactivated"
		if active {
			action = "voucher.activated"
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: action, UserID: p.UserID, TargetType: "voucher", TargetID: voucherID, IP: ip,
		})
	})
	if err != nil {
		return Voucher{}, err
	}
	return s.Get(ctx, voucherID)
}

// Delete soft-deletes a voucher no sale has used (ErrVoucherUsed: deactivate
// that one instead). Its code is free again.
func (s *Service) Delete(ctx context.Context, p authn.Principal, ip, voucherID string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockVoucher(ctx, q, voucherID, s.now())
		if err != nil {
			return err
		}
		if prev.used {
			return ErrVoucherUsed
		}
		if err := q.DeleteVoucher(ctx, voucherID); err != nil {
			return fmt.Errorf("delete voucher %s: %w", voucherID, err)
		}
		before := prev.snapshot()
		before["is_active"] = prev.active
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "voucher.deleted", UserID: p.UserID, TargetType: "voucher", TargetID: voucherID,
			Before: before, IP: ip,
		})
	})
}

// Uses returns a page of the sales that used the voucher, newest first,
// voided ones too, and how many there are.
func (s *Service) Uses(ctx context.Context, voucherID string, page httpx.Page) ([]Use, int, error) {
	var uses []Use
	var total int
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := store.New(tx).GetVoucher(ctx, store.GetVoucherParams{VoucherID: voucherID, At: s.now()})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVoucherNotFound
		}
		if err != nil {
			return fmt.Errorf("load voucher %s: %w", voucherID, err)
		}
		uses, total, err = s.orders.VoucherUses(ctx, tx, voucherID, page)
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

// voucherFields is a voucher's own fields, checked, as the Log Action
// stores them.
type voucherFields struct {
	Code string
	Name *string
	Terms
	Quota      int
	StartAt    *time.Time
	ExpiredAt  time.Time
	AllOutlets bool
	// OutletIDs are sorted, each once; none for all outlets.
	OutletIDs []string
}

func (v voucherFields) snapshot() map[string]any {
	return map[string]any{
		"code": v.Code, "name": v.Name, "type": v.Type, "value": v.Value,
		"max_discount": int64Text(v.MaxDiscount), "min_spend": int64Text(v.MinSpend), "quota": v.Quota,
		"start_at": timeText(v.StartAt), "expired_at": timeText(&v.ExpiredAt),
		"all_outlets": v.AllOutlets, "outlet_ids": v.OutletIDs,
	}
}

// lockedVoucher is a voucher locked for a change: its fields, and what the
// change can't alter.
type lockedVoucher struct {
	voucherFields
	active bool
	used   bool
}

// lockVoucher locks the voucher for a change, with its outlets.
func lockVoucher(ctx context.Context, q *store.Queries, voucherID string, at time.Time) (lockedVoucher, error) {
	r, err := q.LockVoucher(ctx, store.LockVoucherParams{VoucherID: voucherID, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedVoucher{}, ErrVoucherNotFound
	}
	if err != nil {
		return lockedVoucher{}, fmt.Errorf("lock voucher %s: %w", voucherID, err)
	}
	v := lockedVoucher{
		voucherFields: voucherFields{
			Code: r.Code, Name: r.Name,
			Terms: Terms{Type: r.Type, Value: r.Value, MaxDiscount: r.MaxDiscount, MinSpend: r.MinSpend},
			Quota: int(r.Quota), StartAt: utc(r.StartAt), ExpiredAt: r.ExpiredAt.UTC(), AllOutlets: r.AllOutlets,
		},
		active: r.IsActive,
		used:   r.FirstUsedAt != nil,
	}
	if !v.AllOutlets {
		pairs, err := q.ListVoucherOutlets(ctx, []string{voucherID})
		if err != nil {
			return lockedVoucher{}, fmt.Errorf("list the outlets of voucher %s: %w", voucherID, err)
		}
		for _, p := range pairs {
			v.OutletIDs = append(v.OutletIDs, p.OutletID)
		}
		slices.Sort(v.OutletIDs)
	}
	return v, nil
}

func insertOutlets(ctx context.Context, q *store.Queries, voucherID string, outletIDs []string) error {
	if len(outletIDs) == 0 {
		return nil
	}
	if err := q.InsertVoucherOutlets(ctx, store.InsertVoucherOutletsParams{VoucherID: voucherID, OutletIds: outletIDs}); err != nil {
		return fmt.Errorf("insert the outlets of voucher %s: %w", voucherID, err)
	}
	return nil
}

// codePattern is the vouchers_code_check constraint's rule.
var codePattern = regexp.MustCompile(`^[A-Z0-9]{3,20}$`)

// NormalizeCode is a code as the vouchers keep it: capitals, without
// surrounding spaces. The Owner and the cashier may type it in any case.
func NormalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// checkVoucher validates a voucher as filled in, and returns its problems
// by field: every one at once, for validation_failed. Only a percentage has
// a maximum discount, and only a voucher for some outlets has outlets; the
// others' are dropped. Times are kept to the second.
func checkVoucher(in Input) (voucherFields, map[string]string) {
	problems := map[string]string{}
	v := voucherFields{
		Code: NormalizeCode(in.Code), Name: trimmed(in.Name), Terms: Terms{Type: in.Type}, AllOutlets: in.AllOutlets,
	}
	switch {
	case v.Code == "":
		problems["code"] = "required"
	case !codePattern.MatchString(v.Code):
		problems["code"] = "format"
	}
	if v.Name != nil && utf8.RuneCountInString(*v.Name) > MaxName {
		problems["name"] = "max"
	}
	maxValue := int64(MaxAmount)
	switch in.Type {
	case TypePercent:
		maxValue = MaxPercentBPS
	case TypeAmount:
	case "":
		problems["type"] = "required"
	default:
		problems["type"] = "oneof"
	}
	switch {
	case in.Value == nil:
		problems["value"] = "required"
	case *in.Value < 1 || *in.Value > maxValue:
		problems["value"] = "range"
	default:
		v.Value = *in.Value
	}
	if in.Type == TypePercent {
		v.MaxDiscount = amount(in.MaxDiscount, problems, "max_discount")
	}
	v.MinSpend = amount(in.MinSpend, problems, "min_spend")
	switch {
	case in.Quota == nil:
		problems["quota"] = "required"
	case *in.Quota < 1 || *in.Quota > MaxQuota:
		problems["quota"] = "range"
	default:
		v.Quota = *in.Quota
	}
	if in.StartAt != nil {
		v.StartAt = utc(in.StartAt)
	}
	if in.ExpiredAt == nil {
		problems["expired_at"] = "required"
	} else {
		v.ExpiredAt = *utc(in.ExpiredAt)
		if v.StartAt != nil && !v.ExpiredAt.After(*v.StartAt) {
			problems["expired_at"] = "after"
		}
	}
	if !v.AllOutlets {
		v.OutletIDs = slices.Compact(slices.Sorted(slices.Values(in.OutletIDs)))
		if len(v.OutletIDs) == 0 {
			problems["outlet_ids"] = "required"
		}
	}
	return v, problems
}

// amount checks an optional amount of rupiah, from 1 to MaxAmount.
func amount(v *int64, problems map[string]string, field string) *int64 {
	switch {
	case v == nil:
		return nil
	case *v < 1 || *v > MaxAmount:
		problems[field] = "range"
		return nil
	}
	return v
}

// checkOutlets notes in problems an outlet of the voucher that doesn't
// exist or was deleted.
func (s *Service) checkOutlets(ctx context.Context, v voucherFields, problems map[string]string) error {
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

// codeTaken turns the code's unique violation into ErrVoucherCodeTaken.
func codeTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "vouchers_code_key" {
		return ErrVoucherCodeTaken
	}
	if err != nil {
		return fmt.Errorf("save voucher: %w", err)
	}
	return nil
}

// changes returns the fields that differ, before and after. A *string
// compares by its value.
func changes(prev, next map[string]any) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	for k, v := range next {
		if fmt.Sprint(deref(prev[k])) != fmt.Sprint(deref(v)) {
			before[k], after[k] = prev[k], v
		}
	}
	return before, after
}

func deref(v any) any {
	if s, ok := v.(*string); ok {
		if s == nil {
			return nil
		}
		return *s
	}
	return v
}

// trimmed is the text without surrounding spaces, or nil for none.
func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// utc is the time in UTC, to the second: a voucher's times are kept no
// finer.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC().Truncate(time.Second)
	return &u
}

// int64Text shows an optional amount in the Log Action as the number or
// null.
func int64Text(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// timeText shows an optional time in the Log Action as RFC 3339 in UTC, or
// null.
func timeText(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}
