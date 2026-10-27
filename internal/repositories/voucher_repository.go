package repositories

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/voucher"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// VoucherRepository owns vouchers and voucher_outlets: the Owner's discount
// vouchers, and the outlets each is for when it isn't for all of them. A
// voucher's status is the one it has at a moment.
type VoucherRepository struct {
	db *config.Database
}

// NewVoucherRepository returns the vouchers' repository.
func NewVoucherRepository(db *config.Database) *VoucherRepository {
	return &VoucherRepository{db: db}
}

// List returns a page of the vouchers, or of those an outlet's cashier can
// take when outletID is set, the ones that run out last first, with their
// statuses at the moment at, and how many match in all. Their outlets are
// left out.
func (r *VoucherRepository) List(ctx context.Context, outletID *string, f models.VoucherFilter, at time.Time, page pagination.Page) ([]models.Voucher, int, error) {
	status, search := optional(f.Status), optional(f.Search)
	if search != nil {
		*search = likeEscaper.Replace(*search)
	}
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountVouchers(ctx, store.CountVouchersParams{At: at, OutletID: outletID, Status: status, Search: search})
	if err != nil {
		return nil, 0, fmt.Errorf("count vouchers: %w", err)
	}
	rows, err := q.ListVouchers(ctx, store.ListVouchersParams{
		At: at, OutletID: outletID, Status: status, Search: search,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list vouchers: %w", err)
	}
	out := make([]models.Voucher, len(rows))
	for i, row := range rows {
		out[i] = voucherFrom(store.GetVoucherRow(row))
	}
	return out, int(total), nil
}

// Get returns the voucher with its status at the moment at:
// ErrVoucherNotFound if there is no such voucher. Its outlets are left out.
func (r *VoucherRepository) Get(ctx context.Context, voucherID string, at time.Time) (models.Voucher, error) {
	row, err := store.New(r.db.Conn(ctx)).GetVoucher(ctx, store.GetVoucherParams{VoucherID: voucherID, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Voucher{}, apperror.ErrVoucherNotFound
	}
	if err != nil {
		return models.Voucher{}, fmt.Errorf("load voucher %s: %w", voucherID, err)
	}
	return voucherFrom(row), nil
}

// FindByCode returns the voucher with the code, with its status at the
// moment at: ErrVoucherNotFound if there is none.
func (r *VoucherRepository) FindByCode(ctx context.Context, code string, at time.Time) (models.Voucher, error) {
	row, err := store.New(r.db.Conn(ctx)).FindVoucherByCode(ctx, store.FindVoucherByCodeParams{Code: code, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Voucher{}, apperror.ErrVoucherNotFound
	}
	if err != nil {
		return models.Voucher{}, fmt.Errorf("find voucher %s: %w", code, err)
	}
	return voucherFrom(store.GetVoucherRow(row)), nil
}

// LockByCode is FindByCode that locks the voucher until the transaction ctx
// carries ends.
func (r *VoucherRepository) LockByCode(ctx context.Context, code string, at time.Time) (models.Voucher, error) {
	row, err := store.New(r.db.Conn(ctx)).LockVoucherByCode(ctx, store.LockVoucherByCodeParams{Code: code, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Voucher{}, apperror.ErrVoucherNotFound
	}
	if err != nil {
		return models.Voucher{}, fmt.Errorf("lock voucher %s: %w", code, err)
	}
	return voucherFrom(store.GetVoucherRow(row)), nil
}

// IsForOutlet reports whether a voucher that isn't for all outlets is for
// this one.
func (r *VoucherRepository) IsForOutlet(ctx context.Context, voucherID, outletID string) (bool, error) {
	ok, err := store.New(r.db.Conn(ctx)).VoucherIsForOutlet(ctx, store.VoucherIsForOutletParams{VoucherID: voucherID, OutletID: outletID})
	if err != nil {
		return false, fmt.Errorf("find the outlets of voucher %s: %w", voucherID, err)
	}
	return ok, nil
}

// TakeUse takes one of the voucher's uses for a sale at the moment at.
func (r *VoucherRepository) TakeUse(ctx context.Context, voucherID string, at time.Time) error {
	if err := store.New(r.db.Conn(ctx)).TakeVoucherUse(ctx, store.TakeVoucherUseParams{VoucherID: voucherID, At: &at}); err != nil {
		return fmt.Errorf("take a use of voucher %s: %w", voucherID, err)
	}
	return nil
}

// ReturnUse gives one of the voucher's uses back. It stays used.
func (r *VoucherRepository) ReturnUse(ctx context.Context, voucherID string) error {
	if err := store.New(r.db.Conn(ctx)).ReturnVoucherUse(ctx, voucherID); err != nil {
		return fmt.Errorf("return a use of voucher %s: %w", voucherID, err)
	}
	return nil
}

// OutletIDs returns the outlets of each of the vouchers, by voucher ID, in
// the order they were stored.
func (r *VoucherRepository) OutletIDs(ctx context.Context, voucherIDs []string) (map[string][]string, error) {
	pairs, err := store.New(r.db.Conn(ctx)).ListVoucherOutlets(ctx, voucherIDs)
	if err != nil {
		return nil, fmt.Errorf("list the outlets of vouchers: %w", err)
	}
	out := map[string][]string{}
	for _, p := range pairs {
		out[p.VoucherID] = append(out[p.VoucherID], p.OutletID)
	}
	return out, nil
}

// Insert stores a new voucher, without its outlets: ErrVoucherCodeTaken if
// another voucher has the code.
func (r *VoucherRepository) Insert(ctx context.Context, voucherID string, f models.VoucherFields, active bool) error {
	return voucherCodeTaken(store.New(r.db.Conn(ctx)).InsertVoucher(ctx, store.InsertVoucherParams{
		VoucherID: voucherID, Code: f.Code, Name: f.Name, Type: f.Type, Value: f.Value,
		MaxDiscount: f.MaxDiscount, MinSpend: f.MinSpend, Quota: int32(f.Quota), //nolint:gosec // at most MaxQuota
		StartAt: f.StartAt, ExpiredAt: f.ExpiredAt, AllOutlets: f.AllOutlets, IsActive: active,
	}))
}

// AddOutlets stores the outlets a voucher is for; none stores nothing.
func (r *VoucherRepository) AddOutlets(ctx context.Context, voucherID string, outletIDs []string) error {
	if len(outletIDs) == 0 {
		return nil
	}
	if err := store.New(r.db.Conn(ctx)).InsertVoucherOutlets(ctx, store.InsertVoucherOutletsParams{VoucherID: voucherID, OutletIds: outletIDs}); err != nil {
		return fmt.Errorf("insert the outlets of voucher %s: %w", voucherID, err)
	}
	return nil
}

// ClearOutlets takes every outlet off the voucher.
func (r *VoucherRepository) ClearOutlets(ctx context.Context, voucherID string) error {
	if err := store.New(r.db.Conn(ctx)).DeleteVoucherOutlets(ctx, voucherID); err != nil {
		return fmt.Errorf("clear the outlets of voucher %s: %w", voucherID, err)
	}
	return nil
}

// Update stores every field of the voucher but its status and outlets:
// ErrVoucherCodeTaken if another voucher has the code.
func (r *VoucherRepository) Update(ctx context.Context, voucherID string, f models.VoucherFields) error {
	return voucherCodeTaken(store.New(r.db.Conn(ctx)).UpdateVoucher(ctx, store.UpdateVoucherParams{
		VoucherID: voucherID, Code: f.Code, Name: f.Name, Type: f.Type, Value: f.Value,
		MaxDiscount: f.MaxDiscount, MinSpend: f.MinSpend, Quota: int32(f.Quota), //nolint:gosec // at most MaxQuota
		StartAt: f.StartAt, ExpiredAt: f.ExpiredAt, AllOutlets: f.AllOutlets,
	}))
}

// SetActive turns the voucher on or off.
func (r *VoucherRepository) SetActive(ctx context.Context, voucherID string, active bool) error {
	if err := store.New(r.db.Conn(ctx)).SetVoucherActive(ctx, store.SetVoucherActiveParams{VoucherID: voucherID, IsActive: active}); err != nil {
		return fmt.Errorf("set voucher %s active: %w", voucherID, err)
	}
	return nil
}

// Delete soft-deletes the voucher. Its code is free again.
func (r *VoucherRepository) Delete(ctx context.Context, voucherID string) error {
	if err := store.New(r.db.Conn(ctx)).DeleteVoucher(ctx, voucherID); err != nil {
		return fmt.Errorf("delete voucher %s: %w", voucherID, err)
	}
	return nil
}

// Lock locks the voucher for a change, and returns its fields with its
// outlets: ErrVoucherNotFound if there is no such voucher.
func (r *VoucherRepository) Lock(ctx context.Context, voucherID string, at time.Time) (models.VoucherLock, error) {
	q := store.New(r.db.Conn(ctx))
	row, err := q.LockVoucher(ctx, store.LockVoucherParams{VoucherID: voucherID, At: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.VoucherLock{}, apperror.ErrVoucherNotFound
	}
	if err != nil {
		return models.VoucherLock{}, fmt.Errorf("lock voucher %s: %w", voucherID, err)
	}
	v := models.VoucherLock{
		VoucherFields: models.VoucherFields{
			Code: row.Code, Name: row.Name,
			VoucherTerms: models.VoucherTerms{Type: row.Type, Value: row.Value, MaxDiscount: row.MaxDiscount, MinSpend: row.MinSpend},
			Quota:        int(row.Quota), StartAt: toSecond(row.StartAt), ExpiredAt: row.ExpiredAt.UTC(), AllOutlets: row.AllOutlets,
		},
		Active: row.IsActive,
		Used:   row.FirstUsedAt != nil,
	}
	if !v.AllOutlets {
		pairs, err := q.ListVoucherOutlets(ctx, []string{voucherID})
		if err != nil {
			return models.VoucherLock{}, fmt.Errorf("list the outlets of voucher %s: %w", voucherID, err)
		}
		for _, p := range pairs {
			v.OutletIDs = append(v.OutletIDs, p.OutletID)
		}
		slices.Sort(v.OutletIDs)
	}
	return v, nil
}

// voucherCodeTaken turns the code's unique violation into
// ErrVoucherCodeTaken.
func voucherCodeTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "vouchers_code_key" {
		return apperror.ErrVoucherCodeTaken
	}
	if err != nil {
		return fmt.Errorf("save voucher: %w", err)
	}
	return nil
}

func voucherFrom(row store.GetVoucherRow) models.Voucher {
	return models.Voucher{
		VoucherID: row.VoucherID, Code: row.Code, Name: row.Name,
		VoucherTerms: models.VoucherTerms{Type: row.Type, Value: row.Value, MaxDiscount: row.MaxDiscount, MinSpend: row.MinSpend},
		Quota:        int(row.Quota), UsedCount: int(row.UsedCount), StartAt: row.StartAt, ExpiredAt: row.ExpiredAt,
		AllOutlets: row.AllOutlets, Active: row.IsActive, Status: row.Status, FirstUsedAt: row.FirstUsedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// toSecond is the time in UTC, to the second, as a voucher's times are
// kept; nil stays nil.
func toSecond(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC().Truncate(time.Second)
	return &u
}
