package services

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/models"
	"github.com/kervzent/maki-be/internal/repositories"
	"github.com/kervzent/maki-be/internal/utils/apperror"
	"github.com/kervzent/maki-be/internal/utils/id"
	"github.com/kervzent/maki-be/internal/utils/natural"
	"github.com/kervzent/maki-be/internal/utils/pagination"
	"github.com/kervzent/maki-be/internal/validations"
)

// TableOrders is the orders, as far as tables need them: when each paid
// dine-in sale at the outlet's tables after since was written, by table ID,
// read in the transaction the context carries. OrderRepository is the real
// one.
type TableOrders interface {
	TableSales(ctx context.Context, outletID string, since time.Time) (map[string][]time.Time, error)
}

// TableService is each outlet's tables, in their areas. Whether customers
// are seated at a table comes from the outlet's sales, which the orders
// keep, and from the cashier releasing the table when they go; the
// cashier's floor shows that, and each sale at a table holds it.
type TableService struct {
	db      *config.Database
	tables  *repositories.TableRepository
	areas   *repositories.AreaRepository
	modes   *DineInModeService
	outlets *OutletService
	orders  TableOrders
	audit   *AuditService
	now     func() time.Time
}

// NewTableService returns the tables' service. outlets says when each outlet
// closes, which empties its tables; modes, whether it seats by table at all;
// now is the clock the statuses are read at.
func NewTableService(db *config.Database, tables *repositories.TableRepository, areas *repositories.AreaRepository,
	modes *DineInModeService, outlets *OutletService, orders TableOrders, audit *AuditService, now func() time.Time,
) *TableService {
	return &TableService{db: db, tables: tables, areas: areas, modes: modes, outlets: outlets, orders: orders, audit: audit, now: now}
}

// ListTables returns a page of the outlet's tables that match f, by area
// and then by name, with their statuses now, and how many match in all.
func (s *TableService) ListTables(ctx context.Context, outletID string, f models.TableFilter, page pagination.Page) ([]models.Table, int, error) {
	var areaID *string
	if f.AreaID != "" {
		areaID = &f.AreaID
	}
	var all []models.Table
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		var err error
		all, err = s.tablesIn(ctx, outletID, areaID)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	search := strings.ToLower(strings.TrimSpace(f.Search))
	var matched []models.Table
	for _, t := range all {
		if (f.Status == "" || t.Status == f.Status) && strings.Contains(strings.ToLower(t.Name), search) {
			matched = append(matched, t)
		}
	}
	from := min(page.Offset(), len(matched))
	to := min(from+page.Size, len(matched))
	return matched[from:to], len(matched), nil
}

// Table returns one of the outlet's tables, with its status now.
func (s *TableService) Table(ctx context.Context, outletID, tableID string) (models.Table, error) {
	var out models.Table
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		now := s.now()
		t, err := s.tables.Get(ctx, outletID, tableID, now)
		if err != nil {
			return err
		}
		since, sales, err := s.sales(ctx, outletID, now)
		if err != nil {
			return err
		}
		out = withStatus(t, since, sales[tableID])
		return nil
	})
	return out, err
}

// AreaTables returns the tables of one of the outlet's areas, placed or
// not, with their statuses now, by name, in the transaction ctx carries.
func (s *TableService) AreaTables(ctx context.Context, outletID, areaID string) ([]models.Table, error) {
	return s.tablesIn(ctx, outletID, &areaID)
}

// tablesIn reads the outlet's tables, or one area's, with their statuses
// now, by area and then by name.
func (s *TableService) tablesIn(ctx context.Context, outletID string, areaID *string) ([]models.Table, error) {
	now := s.now()
	tables, err := s.tables.List(ctx, outletID, areaID, now)
	if err != nil {
		return nil, err
	}
	since, sales, err := s.sales(ctx, outletID, now)
	if err != nil {
		return nil, err
	}
	out := make([]models.Table, len(tables))
	for i, t := range tables {
		out[i] = withStatus(t, since, sales[t.TableID])
	}
	slices.SortFunc(out, func(a, b models.Table) int {
		return cmp.Or(
			cmp.Compare(a.AreaOrder, b.AreaOrder), strings.Compare(strings.ToLower(a.AreaName), strings.ToLower(b.AreaName)),
			strings.Compare(a.AreaID, b.AreaID), natural.Compare(a.Name, b.Name), strings.Compare(a.TableID, b.TableID),
		)
	})
	return out, nil
}

// sales returns the moment from which a sale at the outlet's tables seats
// customers there, at now, and the sales since, by table.
func (s *TableService) sales(ctx context.Context, outletID string, now time.Time) (time.Time, map[string][]time.Time, error) {
	hours, err := s.outlets.Hours(ctx, outletID)
	if err != nil {
		return time.Time{}, nil, err
	}
	since, err := seatedSince(now, hours)
	if err != nil {
		return time.Time{}, nil, err
	}
	sales, err := s.orders.TableSales(ctx, outletID, since)
	if err != nil {
		return time.Time{}, nil, err
	}
	return since, sales, nil
}

// withStatus is t with its status from its sales since since.
func withStatus(t models.Table, since time.Time, sales []time.Time) models.Table {
	seated, occupied := seating(sales, since, t.ReleasedAt)
	if occupied {
		t.Seated = &seated
	}
	t.Status = tableStatus(t.Active, occupied, t.Scheduled)
	return t
}

// CreateTable adds a table to one of the outlet's areas, not placed on its
// layout yet.
func (s *TableService) CreateTable(ctx context.Context, p models.Principal, ip, outletID string, in models.TableInput) (models.Table, error) {
	next, err := validations.CheckTable(in)
	if err != nil {
		return models.Table{}, err
	}
	active := in.Active == nil || *in.Active
	tableID := id.New()
	err = s.db.InTx(ctx, func(ctx context.Context) error {
		if _, err := s.areas.Lock(ctx, outletID, next.AreaID); errors.Is(err, apperror.ErrAreaNotFound) {
			return apperror.Invalid(map[string]string{"area_id": "exists"})
		} else if err != nil {
			return err
		}
		if err := s.tables.Insert(ctx, outletID, tableID, next, active); err != nil {
			return err
		}
		if err := s.areas.BumpLayoutVersion(ctx, next.AreaID); err != nil {
			return err
		}
		after := next.Snapshot()
		after["is_active"] = active
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "table.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "table", TargetID: tableID, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Table{}, err
	}
	return s.Table(ctx, outletID, tableID)
}

// UpdateTable changes a table's name, area, seats and shape. A table that
// moves to another area leaves its place on the old one's layout. A change
// of nothing records nothing.
func (s *TableService) UpdateTable(ctx context.Context, p models.Principal, ip, outletID, tableID string, in models.TableInput) (models.Table, error) {
	next, err := validations.CheckTable(in)
	if err != nil {
		return models.Table{}, err
	}
	err = s.inTableTx(ctx, func(ctx context.Context) error {
		prev, err := s.lockInArea(ctx, outletID, tableID, next.AreaID)
		if err != nil {
			return err
		}
		before, after := changedAsPrinted(prev.Snapshot(), next.Snapshot())
		if len(after) == 0 {
			return nil
		}
		if err := s.tables.Update(ctx, tableID, next); err != nil {
			return err
		}
		for _, areaID := range slices.Compact([]string{prev.AreaID, next.AreaID}) {
			if err := s.areas.BumpLayoutVersion(ctx, areaID); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "table.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "table", TargetID: tableID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return models.Table{}, err
	}
	return s.Table(ctx, outletID, tableID)
}

// SetTableActive turns a table on or off at the cashier. It stays on its
// layout either way, and a table customers are seated at stays occupied
// until it is released.
func (s *TableService) SetTableActive(ctx context.Context, p models.Principal, ip, outletID, tableID string, active bool) (models.Table, error) {
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.tables.Lock(ctx, outletID, tableID)
		if err != nil {
			return err
		}
		if prev.Active == active {
			return nil
		}
		if err := s.tables.SetActive(ctx, tableID, active); err != nil {
			return err
		}
		action := "table.deactivated"
		if active {
			action = "table.activated"
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "table", TargetID: tableID, IP: ip,
		})
	})
	if err != nil {
		return models.Table{}, err
	}
	return s.Table(ctx, outletID, tableID)
}

// DeleteTable soft-deletes a table nobody is seated at. A sale at it under
// way finishes first, and is counted.
func (s *TableService) DeleteTable(ctx context.Context, p models.Principal, ip, outletID, tableID string) error {
	return s.inTableTx(ctx, func(ctx context.Context) error {
		prev, err := s.lockInArea(ctx, outletID, tableID, "")
		if err != nil {
			return err
		}
		since, sales, err := s.sales(ctx, outletID, s.now())
		if err != nil {
			return err
		}
		if _, seated := seating(sales[tableID], since, prev.ReleasedAt); seated {
			return apperror.ErrTableOccupied
		}
		if err := s.tables.Delete(ctx, tableID); err != nil {
			return err
		}
		if err := s.areas.BumpLayoutVersion(ctx, prev.AreaID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "table.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "table", TargetID: tableID, Before: prev.Snapshot(), IP: ip,
		})
	})
}

// HoldForSale holds one of the outlet's tables for a sale, in the sale's
// own transaction, the one ctx carries, until it commits: other sales at
// the table go on, while a change, a release or a delete of it waits for the
// sale. The table is the outlet's, not deleted, and selectable now;
// otherwise ErrTableUnavailable, with its status, or "missing".
func (s *TableService) HoldForSale(ctx context.Context, outletID, tableID string) (models.Seat, error) {
	now := s.now()
	var seat models.Seat
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		t, err := s.tables.HoldForSale(ctx, outletID, tableID, now)
		if errors.Is(err, apperror.ErrTableNotFound) {
			return apperror.ErrTableUnavailable.WithDetails(map[string]any{"status": "missing"})
		}
		if err != nil {
			return err
		}
		since, sales, err := s.sales(ctx, outletID, now)
		if err != nil {
			return err
		}
		_, occupied := seating(sales[tableID], since, t.ReleasedAt)
		if st := tableStatus(t.Active, occupied, t.Scheduled); !st.Selectable() {
			return apperror.ErrTableUnavailable.WithDetails(map[string]any{"status": string(st)})
		}
		seat = models.Seat{TableID: t.TableID, Name: t.Name}
		return nil
	})
	return seat, err
}

// Release empties a table its customers have left: the sales before it no
// longer seat anyone there. A sale at it under way finishes first, and is
// released too. A table nobody is seated at stays as it is, and records
// nothing.
func (s *TableService) Release(ctx context.Context, p models.Principal, ip, outletID, tableID string) (models.Table, error) {
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		prev, err := s.tables.Lock(ctx, outletID, tableID)
		if err != nil {
			return err
		}
		since, sales, err := s.sales(ctx, outletID, s.now())
		if err != nil {
			return err
		}
		seated, ok := seating(sales[tableID], since, prev.ReleasedAt)
		if !ok {
			return nil
		}
		if err := s.tables.Release(ctx, tableID); err != nil {
			return err
		}
		return s.audit.Record(ctx, models.AuditEntry{
			Action: "table.released", UserID: p.UserID, OutletID: outletID, TargetType: "table", TargetID: tableID,
			Before: map[string]any{"since": seated.Since.UTC(), "orders": seated.Orders}, IP: ip,
		})
	})
	if err != nil {
		return models.Table{}, err
	}
	return s.Table(ctx, outletID, tableID)
}

// Floor returns the outlet's areas that hold tables, in order, with their
// decorations, and their tables with their statuses now. An outlet that
// seats its customers by number stands has none to pick from.
func (s *TableService) Floor(ctx context.Context, outletID string) (models.Floor, error) {
	var out models.Floor
	err := s.db.InTx(ctx, func(ctx context.Context) error {
		mode, err := s.modes.DineInMode(ctx, outletID)
		if err != nil {
			return err
		}
		if mode == models.ModeNumber {
			return nil
		}
		areas, err := s.areas.Floor(ctx, outletID)
		if err != nil {
			return err
		}
		tables, err := s.tablesIn(ctx, outletID, nil)
		if err != nil {
			return err
		}
		byArea := map[string][]models.Table{}
		for _, t := range tables {
			byArea[t.AreaID] = append(byArea[t.AreaID], t)
		}
		for _, a := range areas {
			if len(byArea[a.AreaID]) == 0 {
				continue
			}
			a.Tables = byArea[a.AreaID]
			out.Areas = append(out.Areas, a)
		}
		return nil
	})
	return out, err
}

// errMoved is a table that moved to another area between reading its area
// and locking it: the transaction starts again.
var errMoved = errors.New("table: moved while being locked")

// inTableTx runs fn in a transaction, again when a table it locks moved to
// another area meanwhile: twice more at most.
func (s *TableService) inTableTx(ctx context.Context, fn func(ctx context.Context) error) error {
	for attempt := 1; ; attempt++ {
		err := s.db.InTx(ctx, fn)
		if !errors.Is(err, errMoved) || attempt == 3 {
			return err
		}
	}
}

// lockInArea locks the table's area, and to when the table moves there, in
// the order of their IDs, then the table: the order a layout save takes
// them in, so the two never wait for each other in a circle. An area to
// that isn't the outlet's is a field error; a table that moved meanwhile,
// errMoved.
func (s *TableService) lockInArea(ctx context.Context, outletID, tableID, to string) (models.TableLock, error) {
	from, err := s.tables.AreaOf(ctx, outletID, tableID)
	if err != nil {
		return models.TableLock{}, err
	}
	areas := []string{from}
	if to != "" && to != from {
		areas = append(areas, to)
		slices.Sort(areas)
	}
	for _, areaID := range areas {
		if _, err := s.areas.Lock(ctx, outletID, areaID); errors.Is(err, apperror.ErrAreaNotFound) && areaID == to {
			return models.TableLock{}, apperror.Invalid(map[string]string{"area_id": "exists"})
		} else if err != nil {
			return models.TableLock{}, err
		}
	}
	t, err := s.tables.Lock(ctx, outletID, tableID)
	if err != nil {
		return models.TableLock{}, err
	}
	if t.AreaID != from {
		return models.TableLock{}, errMoved
	}
	return t, nil
}

// seatedSince is the moment from which a sale at a table still seats
// customers there: the outlet's last opening or closing at or before now, in
// its time zone. So a table empties itself when the outlet closes, and a
// sale after closing keeps it only until the outlet opens again. An outlet
// without hours empties its tables at the start of its day.
func seatedSince(now time.Time, h models.OutletHours) (time.Time, error) {
	loc, err := time.LoadLocation(h.Timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("load time zone %q: %w", h.Timezone, err)
	}
	y, m, d := now.In(loc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if h.Open == nil || h.Close == nil {
		return today, nil
	}
	opens, err := timeOfDay(*h.Open)
	if err != nil {
		return time.Time{}, err
	}
	closes, err := timeOfDay(*h.Close)
	if err != nil {
		return time.Time{}, err
	}
	// The outlet opens and closes at those times every day, a closing past
	// midnight included. Yesterday's opening is always before now, so the
	// last of them is yesterday's or today's.
	var last time.Time
	for _, day := range []time.Time{today.AddDate(0, 0, -1), today} {
		for _, event := range []time.Time{onDay(day, opens), onDay(day, closes)} {
			if !event.After(now) && event.After(last) {
				last = event
			}
		}
	}
	return last, nil
}

// timeOfDay is a "15:04" time of day as the time since midnight.
func timeOfDay(s string) (time.Duration, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("read the time of day %q: %w", s, err)
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

// onDay is the time of day on day, in day's zone.
func onDay(day time.Time, t time.Duration) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d, int(t/time.Hour), int(t%time.Hour/time.Minute), 0, 0, day.Location())
}

// tableStatus is a table's status, in this order: occupied while customers
// are seated, an inactive or a scheduled table too until it is released;
// then inactive, scheduled, and available.
func tableStatus(active, occupied, scheduled bool) models.TableStatus {
	switch {
	case occupied:
		return models.TableOccupied
	case !active:
		return models.TableInactive
	case scheduled:
		return models.TableScheduled
	default:
		return models.TableAvailable
	}
}

// seating reads who is seated at a table from its sales since since (when
// each paid dine-in order there was written): those after the table was
// last released. ok is false when nobody is.
func seating(sales []time.Time, since time.Time, releasedAt *time.Time) (s models.Seating, ok bool) {
	from := since
	if releasedAt != nil && releasedAt.After(from) {
		from = *releasedAt
	}
	for _, sale := range sales {
		if !sale.After(from) {
			continue
		}
		if s.Orders == 0 || sale.Before(s.Since) {
			s.Since = sale
		}
		s.Orders++
	}
	return s, s.Orders > 0
}
