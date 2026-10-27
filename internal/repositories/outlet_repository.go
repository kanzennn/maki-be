package repositories

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	store "github.com/kervzent/maki-be/internal/repositories/store/outlet"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/pagination"
)

// OutletRepository owns outlets, the business's outlets. An outlet's
// opening hours are TIME columns, which it reads and writes as "15:04".
type OutletRepository struct {
	db *config.Database
}

// NewOutletRepository returns the outlets' repository.
func NewOutletRepository(db *config.Database) *OutletRepository {
	return &OutletRepository{db: db}
}

// Get returns the outlet: ErrOutletNotFound if there is no such outlet.
func (r *OutletRepository) Get(ctx context.Context, outletID string) (models.Outlet, error) {
	row, err := store.New(r.db.Conn(ctx)).GetOutlet(ctx, outletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Outlet{}, apperror.ErrOutletNotFound
	}
	if err != nil {
		return models.Outlet{}, fmt.Errorf("load outlet %s: %w", outletID, err)
	}
	return models.Outlet{
		OutletID: row.OutletID,
		Code:     row.Code,
		Name:     row.Name,
		Timezone: row.Timezone,
		Locale:   row.Locale,
		Active:   row.Status == "active",
	}, nil
}

// Cashier returns the outlet as its cashier needs it.
func (r *OutletRepository) Cashier(ctx context.Context, outletID string) (models.OutletCashier, error) {
	row, err := store.New(r.db.Conn(ctx)).GetOutletDetail(ctx, outletID)
	return cashierFrom(outletID, row, err)
}

// HoldForSale is Cashier that holds the outlet's row (FOR SHARE) until the
// transaction ctx carries ends.
func (r *OutletRepository) HoldForSale(ctx context.Context, outletID string) (models.OutletCashier, error) {
	row, err := store.New(r.db.Conn(ctx)).HoldOutletForSale(ctx, outletID)
	return cashierFrom(outletID, row, err)
}

// Refs returns the outlets' codes and names by ID, deleted ones too. An ID
// of no outlet is left out.
func (r *OutletRepository) Refs(ctx context.Context, outletIDs []string) (map[string]models.OutletRef, error) {
	out := make(map[string]models.OutletRef, len(outletIDs))
	if len(outletIDs) == 0 {
		return out, nil
	}
	rows, err := store.New(r.db.Conn(ctx)).ListOutletRefs(ctx, outletIDs)
	if err != nil {
		return nil, fmt.Errorf("name outlets: %w", err)
	}
	for _, row := range rows {
		out[row.OutletID] = models.OutletRef(row)
	}
	return out, nil
}

// Hours returns when the outlet opens and closes, in its time zone.
func (r *OutletRepository) Hours(ctx context.Context, outletID string) (models.OutletHours, error) {
	row, err := store.New(r.db.Conn(ctx)).GetOutletHours(ctx, outletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.OutletHours{}, apperror.ErrOutletNotFound
	}
	if err != nil {
		return models.OutletHours{}, fmt.Errorf("load the hours of outlet %s: %w", outletID, err)
	}
	return models.OutletHours{Timezone: row.Timezone, Open: hhmm(row.OpenTime), Close: hhmm(row.CloseTime)}, nil
}

// Exists reports whether the outlet exists and isn't deleted.
func (r *OutletRepository) Exists(ctx context.Context, outletID string) (bool, error) {
	return store.New(r.db.Conn(ctx)).OutletExists(ctx, outletID)
}

// List returns a page of outlets, by name, and how many match in all.
// Their Managers are left out.
func (r *OutletRepository) List(ctx context.Context, f models.OutletFilter, page pagination.Page) ([]models.OutletDetail, int, error) {
	status, search := optional(f.Status), optional(f.Search)
	if search != nil {
		*search = likeEscaper.Replace(*search)
	}
	q := store.New(r.db.Conn(ctx))
	total, err := q.CountOutlets(ctx, store.CountOutletsParams{Status: status, Search: search})
	if err != nil {
		return nil, 0, fmt.Errorf("count outlets: %w", err)
	}
	rows, err := q.ListOutlets(ctx, store.ListOutletsParams{
		Status:     status,
		Search:     search,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // BindPage caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list outlets: %w", err)
	}
	out := make([]models.OutletDetail, len(rows))
	for i, row := range rows {
		out[i] = detailFrom(row)
	}
	return out, int(total), nil
}

// Detail returns the outlet with everything the Owner sees, its Managers
// left out: ErrOutletNotFound if there is no such outlet.
func (r *OutletRepository) Detail(ctx context.Context, outletID string) (models.OutletDetail, error) {
	row, err := store.New(r.db.Conn(ctx)).GetOutletDetail(ctx, outletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.OutletDetail{}, apperror.ErrOutletNotFound
	}
	if err != nil {
		return models.OutletDetail{}, fmt.Errorf("load outlet %s: %w", outletID, err)
	}
	return detailFrom(row), nil
}

// Lock locks the outlet for a change and returns its fields:
// ErrOutletNotFound if there is no such outlet.
func (r *OutletRepository) Lock(ctx context.Context, outletID string) (models.OutletFields, error) {
	row, err := store.New(r.db.Conn(ctx)).GetOutletForUpdate(ctx, outletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.OutletFields{}, apperror.ErrOutletNotFound
	}
	if err != nil {
		return models.OutletFields{}, fmt.Errorf("lock outlet %s: %w", outletID, err)
	}
	return models.OutletFields{
		Code: row.Code, Name: row.Name, Address: row.Address, Phone: row.Phone,
		Timezone: row.Timezone, Locale: row.Locale, OpenTime: hhmm(row.OpenTime), CloseTime: hhmm(row.CloseTime),
		TaxBPS: int(row.TaxBps), ServiceBPS: int(row.ServiceBps), Status: row.Status,
	}, nil
}

// Insert stores a new, active outlet: ErrOutletCodeTaken if another outlet
// has, or had, the code.
func (r *OutletRepository) Insert(ctx context.Context, outletID string, f models.OutletFields) error {
	return codeTaken(store.New(r.db.Conn(ctx)).InsertOutlet(ctx, store.InsertOutletParams{
		OutletID:   outletID,
		Code:       f.Code,
		Name:       f.Name,
		Address:    f.Address,
		Phone:      f.Phone,
		Timezone:   f.Timezone,
		Locale:     f.Locale,
		OpenTime:   clock(f.OpenTime),
		CloseTime:  clock(f.CloseTime),
		TaxBps:     int32(f.TaxBPS),     //nolint:gosec // CheckOutlet keeps it within 0..10000
		ServiceBps: int32(f.ServiceBPS), //nolint:gosec // as above
	}))
}

// Update stores the outlet's fields but its status: ErrOutletCodeTaken if
// another outlet has, or had, the code.
func (r *OutletRepository) Update(ctx context.Context, outletID string, f models.OutletFields) error {
	return codeTaken(store.New(r.db.Conn(ctx)).UpdateOutlet(ctx, store.UpdateOutletParams{
		OutletID:   outletID,
		Code:       f.Code,
		Name:       f.Name,
		Address:    f.Address,
		Phone:      f.Phone,
		Timezone:   f.Timezone,
		Locale:     f.Locale,
		OpenTime:   clock(f.OpenTime),
		CloseTime:  clock(f.CloseTime),
		TaxBps:     int32(f.TaxBPS),     //nolint:gosec // CheckOutlet keeps it within 0..10000
		ServiceBps: int32(f.ServiceBPS), //nolint:gosec // as above
	}))
}

// SetStatus activates or deactivates the outlet: status is active or
// inactive.
func (r *OutletRepository) SetStatus(ctx context.Context, outletID, status string) error {
	return store.New(r.db.Conn(ctx)).SetOutletStatus(ctx, store.SetOutletStatusParams{OutletID: outletID, Status: status})
}

// Delete soft-deletes the outlet. Its code stays taken.
func (r *OutletRepository) Delete(ctx context.Context, outletID string) error {
	return store.New(r.db.Conn(ctx)).DeleteOutlet(ctx, outletID)
}

// codeTaken turns the code's unique violation into ErrOutletCodeTaken.
func codeTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "outlets_code_key" {
		return apperror.ErrOutletCodeTaken
	}
	return err
}

func cashierFrom(outletID string, row store.Outlet, err error) (models.OutletCashier, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return models.OutletCashier{}, apperror.ErrOutletNotFound
	}
	if err != nil {
		return models.OutletCashier{}, fmt.Errorf("load outlet %s: %w", outletID, err)
	}
	return models.OutletCashier{
		OutletID: row.OutletID, Code: row.Code, Name: row.Name, Address: row.Address, Phone: row.Phone,
		Timezone: row.Timezone, Locale: row.Locale, TaxBPS: int(row.TaxBps), ServiceBPS: int(row.ServiceBps),
		Active: row.Status == "active",
	}, nil
}

func detailFrom(row store.Outlet) models.OutletDetail {
	return models.OutletDetail{
		Outlet: models.Outlet{
			OutletID: row.OutletID,
			Code:     row.Code,
			Name:     row.Name,
			Timezone: row.Timezone,
			Locale:   row.Locale,
			Active:   row.Status == "active",
		},
		Address:    row.Address,
		Phone:      row.Phone,
		OpenTime:   hhmm(row.OpenTime),
		CloseTime:  hhmm(row.CloseTime),
		TaxBPS:     int(row.TaxBps),
		ServiceBPS: int(row.ServiceBps),
		CreatedAt:  row.CreatedAt,
		UpdatedAt:  row.UpdatedAt,
	}
}

// clock turns a checked "15:04" into a TIME; nil is NULL.
func clock(hhmm *string) pgtype.Time {
	if hhmm == nil {
		return pgtype.Time{}
	}
	t, _ := time.Parse("15:04", *hhmm)
	return pgtype.Time{Microseconds: int64(t.Hour()*3600+t.Minute()*60) * 1_000_000, Valid: true}
}

// hhmm formats a TIME as "15:04"; nil for NULL.
func hhmm(t pgtype.Time) *string {
	if !t.Valid {
		return nil
	}
	minutes := t.Microseconds / 60_000_000
	s := fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
	return &s
}
