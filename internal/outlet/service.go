package outlet

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/outlet/store"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/i18n"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/user"
)

// DefaultTimezone is an outlet's time zone when the Owner picks none.
const DefaultTimezone = "Asia/Jakarta"

// Users is the user module, as far as outlets need it: a new outlet's
// cashier role, and who manages each outlet.
type Users interface {
	CreateTemplateRole(ctx context.Context, tx pgx.Tx, outletID, locale string) error
	ManagersOf(ctx context.Context, outletIDs []string) (map[string][]user.Manager, error)
}

// Payments is the payment module, as far as outlets need it: a new outlet's
// payment methods.
type Payments interface {
	CreateDefaults(ctx context.Context, tx pgx.Tx, outletID string) error
}

// Orders is the order module, as far as outlets need it: whether an outlet
// has taken an order, after which its code can't change. It reads in the
// caller's transaction; order.Records is the real one.
type Orders interface {
	HasOrders(ctx context.Context, tx pgx.Tx, outletID string) (bool, error)
}

// Auditor records Log Action entries inside a transaction; audit.Recorder
// is the real one.
type Auditor interface {
	Record(ctx context.Context, tx pgx.Tx, e audit.Entry) error
}

// Service reads and changes outlets.
type Service struct {
	pool     *pgxpool.Pool
	users    Users
	payments Payments
	orders   Orders
	audit    Auditor
}

// NewService returns the outlet service.
func NewService(pool *pgxpool.Pool, users Users, payments Payments, orders Orders, auditor Auditor) *Service {
	return &Service{pool: pool, users: users, payments: payments, orders: orders, audit: auditor}
}

// Get returns the outlet.
func (s *Service) Get(ctx context.Context, outletID string) (Outlet, error) {
	r, err := store.New(s.pool).GetOutlet(ctx, outletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Outlet{}, ErrNotFound
	}
	if err != nil {
		return Outlet{}, fmt.Errorf("load outlet %s: %w", outletID, err)
	}
	return Outlet{
		OutletID: r.OutletID,
		Code:     r.Code,
		Name:     r.Name,
		Timezone: r.Timezone,
		Locale:   r.Locale,
		Active:   r.Status == "active",
	}, nil
}

// Cashier returns the outlet as its cashier needs it.
func (s *Service) Cashier(ctx context.Context, outletID string) (Cashier, error) {
	r, err := store.New(s.pool).GetOutletDetail(ctx, outletID)
	return cashier(outletID, r, err)
}

// HoldForSale is Cashier in a sale's transaction. It holds the outlet (FOR
// SHARE) until the sale commits: the code, time zone and rates the sale is
// checked against stay as they are, and a change to them waits for it.
func (s *Service) HoldForSale(ctx context.Context, tx pgx.Tx, outletID string) (Cashier, error) {
	r, err := store.New(tx).HoldOutletForSale(ctx, outletID)
	return cashier(outletID, r, err)
}

func cashier(outletID string, r store.Outlet, err error) (Cashier, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Cashier{}, ErrNotFound
	}
	if err != nil {
		return Cashier{}, fmt.Errorf("load outlet %s: %w", outletID, err)
	}
	return Cashier{
		OutletID: r.OutletID, Code: r.Code, Name: r.Name, Address: r.Address, Phone: r.Phone,
		Timezone: r.Timezone, Locale: r.Locale, TaxBPS: int(r.TaxBps), ServiceBPS: int(r.ServiceBps),
		Active: r.Status == "active",
	}, nil
}

// Refs returns the outlets' codes and names by ID, deleted ones too. An ID
// of no outlet is left out.
func (s *Service) Refs(ctx context.Context, outletIDs []string) (map[string]Ref, error) {
	out := make(map[string]Ref, len(outletIDs))
	if len(outletIDs) == 0 {
		return out, nil
	}
	rows, err := store.New(s.pool).ListOutletRefs(ctx, outletIDs)
	if err != nil {
		return nil, fmt.Errorf("name outlets: %w", err)
	}
	for _, r := range rows {
		out[r.OutletID] = Ref(r)
	}
	return out, nil
}

// Hours returns when the outlet opens and closes, in its time zone: a table
// empties itself when the outlet closes.
func (s *Service) Hours(ctx context.Context, outletID string) (Hours, error) {
	r, err := store.New(s.pool).GetOutletHours(ctx, outletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Hours{}, ErrNotFound
	}
	if err != nil {
		return Hours{}, fmt.Errorf("load the hours of outlet %s: %w", outletID, err)
	}
	return Hours{Timezone: r.Timezone, Open: hhmm(r.OpenTime), Close: hhmm(r.CloseTime)}, nil
}

// OutletExists reports whether the outlet exists and isn't deleted, for the
// OutletScope middleware.
func (s *Service) OutletExists(ctx context.Context, outletID string) (bool, error) {
	return store.New(s.pool).OutletExists(ctx, outletID)
}

// List returns a page of outlets, by name, and how many match in all.
func (s *Service) List(ctx context.Context, f Filter, page httpx.Page) ([]Detail, int, error) {
	var status, search *string
	if f.Status != "" {
		status = &f.Status
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		escaped := likeEscaper.Replace(q)
		search = &escaped
	}
	q := store.New(s.pool)
	total, err := q.CountOutlets(ctx, store.CountOutletsParams{Status: status, Search: search})
	if err != nil {
		return nil, 0, fmt.Errorf("count outlets: %w", err)
	}
	rows, err := q.ListOutlets(ctx, store.ListOutletsParams{
		Status:     status,
		Search:     search,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list outlets: %w", err)
	}
	out := make([]Detail, len(rows))
	ids := make([]string, len(rows))
	for i, r := range rows {
		out[i], ids[i] = detail(r), r.OutletID
	}
	managers, err := s.users.ManagersOf(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range out {
		out[i].Managers = managers[out[i].OutletID]
	}
	return out, int(total), nil
}

// likeEscaper keeps LIKE's wildcards in a search literal.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Detail returns the outlet with its Managers.
func (s *Service) Detail(ctx context.Context, outletID string) (Detail, error) {
	r, err := store.New(s.pool).GetOutletDetail(ctx, outletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("load outlet %s: %w", outletID, err)
	}
	d := detail(r)
	managers, err := s.users.ManagersOf(ctx, []string{outletID})
	if err != nil {
		return Detail{}, err
	}
	d.Managers = managers[outletID]
	return d, nil
}

// Create adds an outlet, with a cashier role for its Manager to start from
// and its payment methods, in one transaction.
func (s *Service) Create(ctx context.Context, p authn.Principal, ip string, in Input) (Detail, error) {
	row, err := check(in)
	if err != nil {
		return Detail{}, err
	}
	row.OutletID = id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := codeTaken(store.New(tx).InsertOutlet(ctx, store.InsertOutletParams{
			OutletID:   row.OutletID,
			Code:       row.Code,
			Name:       row.Name,
			Address:    row.Address,
			Phone:      row.Phone,
			Timezone:   row.Timezone,
			Locale:     row.Locale,
			OpenTime:   row.OpenTime,
			CloseTime:  row.CloseTime,
			TaxBps:     row.TaxBps,
			ServiceBps: row.ServiceBps,
		})); err != nil {
			return err
		}
		if err := s.users.CreateTemplateRole(ctx, tx, row.OutletID, row.Locale); err != nil {
			return err
		}
		if err := s.payments.CreateDefaults(ctx, tx, row.OutletID); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "outlet.created", UserID: p.UserID, OutletID: row.OutletID,
			TargetType: "outlet", TargetID: row.OutletID, After: snapshot(row), IP: ip,
		})
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, row.OutletID)
}

// Update changes every field of the outlet but its status. The code is
// printed in every receipt number, so it can't change once the outlet has
// taken an order (ErrCodeLocked).
func (s *Service) Update(ctx context.Context, p authn.Principal, ip, outletID string, in Input) (Detail, error) {
	next, err := check(in)
	if err != nil {
		return Detail{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lock(ctx, q, outletID)
		if err != nil {
			return err
		}
		next.Status = prev.Status
		before, after := changes(snapshot(prev), snapshot(next))
		if len(after) == 0 {
			return nil
		}
		// The lock above waited for any sale under way, which holds the
		// outlet until it commits, so its order is counted here.
		if next.Code != prev.Code {
			used, err := s.orders.HasOrders(ctx, tx, outletID)
			if err != nil {
				return err
			}
			if used {
				return ErrCodeLocked
			}
		}
		if err := codeTaken(q.UpdateOutlet(ctx, store.UpdateOutletParams{
			OutletID:   outletID,
			Code:       next.Code,
			Name:       next.Name,
			Address:    next.Address,
			Phone:      next.Phone,
			Timezone:   next.Timezone,
			Locale:     next.Locale,
			OpenTime:   next.OpenTime,
			CloseTime:  next.CloseTime,
			TaxBps:     next.TaxBps,
			ServiceBps: next.ServiceBps,
		})); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "outlet.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "outlet", TargetID: outletID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, outletID)
}

// SetStatus activates or deactivates the outlet. Deactivating it ends its
// users' access at their next request; the Owner can still see it and
// activate it again.
func (s *Service) SetStatus(ctx context.Context, p authn.Principal, ip, outletID string, active bool) (Detail, error) {
	status, action := "inactive", "outlet.deactivated"
	if active {
		status, action = "active", "outlet.activated"
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lock(ctx, q, outletID)
		if err != nil || prev.Status == status {
			return err
		}
		if err := q.SetOutletStatus(ctx, store.SetOutletStatusParams{OutletID: outletID, Status: status}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "outlet", TargetID: outletID,
			Before: map[string]any{"status": prev.Status}, After: map[string]any{"status": status}, IP: ip,
		})
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Detail(ctx, outletID)
}

// Delete soft-deletes a deactivated outlet, once confirmName repeats its
// name. Its data and its code stay; it is gone from every list.
func (s *Service) Delete(ctx context.Context, p authn.Principal, ip, outletID, confirmName string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lock(ctx, q, outletID)
		if err != nil {
			return err
		}
		if prev.Status == "active" {
			return ErrStillActive
		}
		if strings.TrimSpace(confirmName) != prev.Name {
			return ErrNameMismatch
		}
		if err := q.DeleteOutlet(ctx, outletID); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "outlet.deleted", UserID: p.UserID, OutletID: outletID, TargetType: "outlet", TargetID: outletID,
			Before: snapshot(prev), IP: ip,
		})
	})
}

func lock(ctx context.Context, q *store.Queries, outletID string) (store.Outlet, error) {
	r, err := q.GetOutletForUpdate(ctx, outletID)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Outlet{}, ErrNotFound
	}
	if err != nil {
		return store.Outlet{}, fmt.Errorf("lock outlet %s: %w", outletID, err)
	}
	return r, nil
}

// codeTaken turns the code's unique violation into ErrCodeTaken.
func codeTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "outlets_code_key" {
		return ErrCodeTaken
	}
	return err
}

var (
	codePattern = regexp.MustCompile(`^[A-Z0-9]{2,10}$`)
	hourPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
)

// check validates what the Owner filled in and returns it as a row, every
// problem at once as validation_failed.
func check(in Input) (store.Outlet, error) {
	fields := map[string]string{}
	r := store.Outlet{
		Code:     strings.ToUpper(strings.TrimSpace(in.Code)),
		Name:     strings.TrimSpace(in.Name),
		Address:  trimmed(in.Address),
		Phone:    trimmed(in.Phone),
		Timezone: strings.TrimSpace(in.Timezone),
		Locale:   strings.TrimSpace(in.Locale),
		Status:   "active",
	}
	if !codePattern.MatchString(r.Code) {
		fields["code"] = "format"
	}
	switch n := utf8.RuneCountInString(r.Name); {
	case n == 0:
		fields["name"] = "required"
	case n > 100:
		fields["name"] = "max"
	}
	if r.Address != nil && utf8.RuneCountInString(*r.Address) > 500 {
		fields["address"] = "max"
	}
	if r.Phone != nil && utf8.RuneCountInString(*r.Phone) > 32 {
		fields["phone"] = "max"
	}
	if r.Timezone == "" {
		r.Timezone = DefaultTimezone
	}
	if !validTimezone(r.Timezone) {
		fields["timezone"] = "timezone"
	}
	if r.Locale == "" {
		r.Locale = i18n.Default
	}
	if !i18n.Supported(r.Locale) {
		fields["locale"] = "oneof"
	}
	checkHours(in.OpenTime, in.CloseTime, &r, fields)
	r.TaxBps = basisPoints(in.TaxPct, fields, "tax_pct")
	r.ServiceBps = basisPoints(in.ServicePct, fields, "service_pct")
	if len(fields) > 0 {
		return store.Outlet{}, httpx.Invalid(fields)
	}
	return r, nil
}

// validTimezone accepts an IANA zone such as Asia/Jakarta. "Local" would be
// the server's zone, not the outlet's.
func validTimezone(tz string) bool {
	if tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// checkHours sets the opening hours on r: both or neither, as "15:04", and
// never the same time twice.
func checkHours(open, closing *string, r *store.Outlet, fields map[string]string) {
	o, c := trimmed(open), trimmed(closing)
	switch {
	case o == nil && c == nil:
		return
	case o == nil:
		fields["open_time"] = "required"
		return
	case c == nil:
		fields["close_time"] = "required"
		return
	}
	if !hourPattern.MatchString(*o) {
		fields["open_time"] = "format"
	}
	if !hourPattern.MatchString(*c) {
		fields["close_time"] = "format"
	}
	if fields["open_time"] != "" || fields["close_time"] != "" {
		return
	}
	if *o == *c {
		fields["close_time"] = "different"
		return
	}
	r.OpenTime, r.CloseTime = clock(*o), clock(*c)
}

// clock turns a checked "15:04" into a TIME.
func clock(hhmm string) pgtype.Time {
	t, _ := time.Parse("15:04", hhmm)
	return pgtype.Time{Microseconds: int64(t.Hour()*3600+t.Minute()*60) * 1_000_000, Valid: true}
}

// hhmm formats a TIME as "15:04"; nil for none.
func hhmm(t pgtype.Time) *string {
	if !t.Valid {
		return nil
	}
	minutes := t.Microseconds / 60_000_000
	s := fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
	return &s
}

// basisPoints turns a percentage with at most two decimals, 0 to 100, into
// basis points. Anything else is noted in fields.
func basisPoints(pct float64, fields map[string]string, field string) int32 {
	if math.IsNaN(pct) || pct < 0 || pct > 100 {
		fields[field] = "range"
		return 0
	}
	bps := math.Round(pct * 100)
	if math.Abs(pct*100-bps) > 1e-6 {
		fields[field] = "decimals"
		return 0
	}
	return int32(bps)
}

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

func detail(r store.Outlet) Detail {
	return Detail{
		Outlet: Outlet{
			OutletID: r.OutletID,
			Code:     r.Code,
			Name:     r.Name,
			Timezone: r.Timezone,
			Locale:   r.Locale,
			Active:   r.Status == "active",
		},
		Address:    r.Address,
		Phone:      r.Phone,
		OpenTime:   hhmm(r.OpenTime),
		CloseTime:  hhmm(r.CloseTime),
		TaxBPS:     int(r.TaxBps),
		ServiceBPS: int(r.ServiceBps),
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

// snapshot is an outlet's fields as the Log Action stores them.
func snapshot(r store.Outlet) map[string]any {
	return map[string]any{
		"code":        r.Code,
		"name":        r.Name,
		"address":     r.Address,
		"phone":       r.Phone,
		"timezone":    r.Timezone,
		"locale":      r.Locale,
		"open_time":   hhmm(r.OpenTime),
		"close_time":  hhmm(r.CloseTime),
		"tax_bps":     r.TaxBps,
		"service_bps": r.ServiceBps,
		"status":      r.Status,
	}
}

// changes returns the fields that differ, before and after.
func changes(prev, next map[string]any) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	for k, v := range next {
		if fmt.Sprint(deref(prev[k])) != fmt.Sprint(deref(v)) {
			before[k], after[k] = prev[k], v
		}
	}
	return before, after
}

// deref shows a *string as its value, so two pointers to the same text
// compare equal.
func deref(v any) any {
	if s, ok := v.(*string); ok {
		if s == nil {
			return nil
		}
		return *s
	}
	return v
}
