package table

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/platform/authn"
	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/httpx"
	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/table/store"
)

// ListTables returns a page of the outlet's tables that match f, by area
// and then by name, with their statuses now, and how many match in all.
func (s *Service) ListTables(ctx context.Context, outletID string, f Filter, page httpx.Page) ([]Table, int, error) {
	var areaID *string
	if f.AreaID != "" {
		areaID = &f.AreaID
	}
	var all []Table
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		all, err = s.tablesIn(ctx, tx, outletID, areaID)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	search := strings.ToLower(strings.TrimSpace(f.Search))
	var matched []Table
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
func (s *Service) Table(ctx context.Context, outletID, tableID string) (Table, error) {
	var out Table
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		now := s.now()
		r, err := store.New(tx).GetTable(ctx, store.GetTableParams{TableID: tableID, OutletID: outletID, At: now})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTableNotFound
		}
		if err != nil {
			return fmt.Errorf("load table %s: %w", tableID, err)
		}
		since, sales, err := s.sales(ctx, tx, outletID, now)
		if err != nil {
			return err
		}
		out = newTable(r, since, sales[tableID])
		return nil
	})
	return out, err
}

// tablesIn reads the outlet's tables, or one area's, in tx, with their
// statuses now, by area and then by name.
func (s *Service) tablesIn(ctx context.Context, tx pgx.Tx, outletID string, areaID *string) ([]Table, error) {
	now := s.now()
	rows, err := store.New(tx).ListTables(ctx, store.ListTablesParams{OutletID: outletID, AreaID: areaID, At: now})
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	since, sales, err := s.sales(ctx, tx, outletID, now)
	if err != nil {
		return nil, err
	}
	out := make([]Table, len(rows))
	for i, r := range rows {
		out[i] = newTable(store.GetTableRow(r), since, sales[r.TableID])
	}
	slices.SortFunc(out, func(a, b Table) int {
		return cmp.Or(
			cmp.Compare(a.areaOrder, b.areaOrder), strings.Compare(strings.ToLower(a.AreaName), strings.ToLower(b.AreaName)),
			strings.Compare(a.AreaID, b.AreaID), natural(a.Name, b.Name), strings.Compare(a.TableID, b.TableID),
		)
	})
	return out, nil
}

// sales returns the moment from which a sale at the outlet's tables seats
// customers there, at now, and the sales since, by table.
func (s *Service) sales(ctx context.Context, tx pgx.Tx, outletID string, now time.Time) (time.Time, map[string][]time.Time, error) {
	hours, err := s.outlets.Hours(ctx, outletID)
	if err != nil {
		return time.Time{}, nil, err
	}
	since, err := seatedSince(now, hours)
	if err != nil {
		return time.Time{}, nil, err
	}
	sales, err := s.orders.TableSales(ctx, tx, outletID, since)
	if err != nil {
		return time.Time{}, nil, err
	}
	return since, sales, nil
}

// newTable is a table read from the store, with its status from its sales
// since since.
func newTable(r store.GetTableRow, since time.Time, sales []time.Time) Table {
	t := Table{
		TableID: r.TableID, AreaID: r.AreaID, AreaName: r.AreaName, Name: r.Name, Capacity: intPtr(r.Capacity),
		Shape: r.Shape, Rotation: int(r.Rotation), Active: r.IsActive, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		areaOrder: int(r.AreaSortOrder),
	}
	// The schema keeps the box whole or leaves it out.
	if r.X != nil && r.Y != nil && r.W != nil && r.H != nil {
		t.Box = &Box{X: int(*r.X), Y: int(*r.Y), W: int(*r.W), H: int(*r.H)}
	}
	seated, occupied := seating(sales, since, r.ReleasedAt)
	if occupied {
		t.Seated = &seated
	}
	t.Status = status(r.IsActive, occupied, r.Scheduled)
	return t
}

// CreateTable adds a table to one of the outlet's areas, not placed on its
// layout yet.
func (s *Service) CreateTable(ctx context.Context, p authn.Principal, ip, outletID string, in TableInput) (Table, error) {
	next, err := checkTable(in)
	if err != nil {
		return Table{}, err
	}
	active := in.Active == nil || *in.Active
	tableID := id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := lockArea(ctx, q, outletID, next.areaID); errors.Is(err, ErrAreaNotFound) {
			return httpx.Invalid(map[string]string{"area_id": "exists"})
		} else if err != nil {
			return err
		}
		if err := tableNameTaken(q.InsertTable(ctx, store.InsertTableParams{
			TableID: tableID, OutletID: outletID, AreaID: next.areaID, Name: next.name,
			Capacity: int32Ptr(next.capacity), Shape: next.shape, IsActive: active,
		})); err != nil {
			return err
		}
		if err := q.BumpLayoutVersion(ctx, next.areaID); err != nil {
			return fmt.Errorf("move the layout of area %s on: %w", next.areaID, err)
		}
		after := next.snapshot()
		after["is_active"] = active
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "table.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "table", TargetID: tableID, After: after, IP: ip,
		})
	})
	if err != nil {
		return Table{}, err
	}
	return s.Table(ctx, outletID, tableID)
}

// UpdateTable changes a table's name, area, seats and shape. A table that
// moves to another area leaves its place on the old one's layout. A change
// of nothing records nothing.
func (s *Service) UpdateTable(ctx context.Context, p authn.Principal, ip, outletID, tableID string, in TableInput) (Table, error) {
	next, err := checkTable(in)
	if err != nil {
		return Table{}, err
	}
	err = s.inTableTx(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockTableInArea(ctx, q, outletID, tableID, next.areaID)
		if err != nil {
			return err
		}
		before, after := changes(fieldsOf(prev).snapshot(), next.snapshot())
		if len(after) == 0 {
			return nil
		}
		if err := tableNameTaken(q.UpdateTable(ctx, store.UpdateTableParams{
			TableID: tableID, AreaID: next.areaID, Name: next.name, Capacity: int32Ptr(next.capacity), Shape: next.shape,
		})); err != nil {
			return err
		}
		for _, areaID := range slices.Compact([]string{prev.AreaID, next.areaID}) {
			if err := q.BumpLayoutVersion(ctx, areaID); err != nil {
				return fmt.Errorf("move the layout of area %s on: %w", areaID, err)
			}
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "table.updated", UserID: p.UserID, OutletID: outletID,
			TargetType: "table", TargetID: tableID, Before: before, After: after, IP: ip,
		})
	})
	if err != nil {
		return Table{}, err
	}
	return s.Table(ctx, outletID, tableID)
}

// SetTableActive turns a table on or off at the cashier. It stays on its
// layout either way, and a table customers are seated at stays occupied
// until it is released.
func (s *Service) SetTableActive(ctx context.Context, p authn.Principal, ip, outletID, tableID string, active bool) (Table, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockTable(ctx, q, outletID, tableID)
		if err != nil {
			return err
		}
		if prev.IsActive == active {
			return nil
		}
		if err := q.SetTableActive(ctx, store.SetTableActiveParams{TableID: tableID, IsActive: active}); err != nil {
			return err
		}
		action := "table.deactivated"
		if active {
			action = "table.activated"
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: action, UserID: p.UserID, OutletID: outletID, TargetType: "table", TargetID: tableID, IP: ip,
		})
	})
	if err != nil {
		return Table{}, err
	}
	return s.Table(ctx, outletID, tableID)
}

// DeleteTable soft-deletes a table nobody is seated at. A sale at it under
// way finishes first, and is counted.
func (s *Service) DeleteTable(ctx context.Context, p authn.Principal, ip, outletID, tableID string) error {
	return s.inTableTx(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockTableInArea(ctx, q, outletID, tableID, "")
		if err != nil {
			return err
		}
		since, sales, err := s.sales(ctx, tx, outletID, s.now())
		if err != nil {
			return err
		}
		if _, seated := seating(sales[tableID], since, prev.ReleasedAt); seated {
			return ErrTableOccupied
		}
		if err := q.DeleteTable(ctx, tableID); err != nil {
			return err
		}
		if err := q.BumpLayoutVersion(ctx, prev.AreaID); err != nil {
			return fmt.Errorf("move the layout of area %s on: %w", prev.AreaID, err)
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "table.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "table", TargetID: tableID, Before: fieldsOf(prev).snapshot(), IP: ip,
		})
	})
}

// errMoved is a table that moved to another area between reading its area
// and locking it: the transaction starts again.
var errMoved = errors.New("table: moved while being locked")

// inTableTx runs fn in a transaction, again when a table it locks moved to
// another area meanwhile: twice more at most.
func (s *Service) inTableTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	for attempt := 1; ; attempt++ {
		err := db.WithTx(ctx, s.pool, fn)
		if !errors.Is(err, errMoved) || attempt == 3 {
			return err
		}
	}
}

// lockTableInArea locks the table's area, and to when the table moves there,
// in the order of their IDs, then the table: the order a layout save takes
// them in, so the two never wait for each other in a circle. An area to that
// isn't the outlet's is a field error; a table that moved meanwhile,
// errMoved.
func lockTableInArea(ctx context.Context, q *store.Queries, outletID, tableID, to string) (store.LockTableRow, error) {
	from, err := q.GetTableArea(ctx, store.GetTableAreaParams{TableID: tableID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.LockTableRow{}, ErrTableNotFound
	}
	if err != nil {
		return store.LockTableRow{}, fmt.Errorf("find the area of table %s: %w", tableID, err)
	}
	areas := []string{from}
	if to != "" && to != from {
		areas = append(areas, to)
		slices.Sort(areas)
	}
	for _, areaID := range areas {
		if _, err := lockArea(ctx, q, outletID, areaID); errors.Is(err, ErrAreaNotFound) && areaID == to {
			return store.LockTableRow{}, httpx.Invalid(map[string]string{"area_id": "exists"})
		} else if err != nil {
			return store.LockTableRow{}, err
		}
	}
	t, err := lockTable(ctx, q, outletID, tableID)
	if err != nil {
		return store.LockTableRow{}, err
	}
	if t.AreaID != from {
		return store.LockTableRow{}, errMoved
	}
	return t, nil
}

// lockTable locks one of the outlet's tables.
func lockTable(ctx context.Context, q *store.Queries, outletID, tableID string) (store.LockTableRow, error) {
	r, err := q.LockTable(ctx, store.LockTableParams{TableID: tableID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.LockTableRow{}, ErrTableNotFound
	}
	if err != nil {
		return store.LockTableRow{}, fmt.Errorf("lock table %s: %w", tableID, err)
	}
	return r, nil
}

// tableFields is a table's own fields, checked, as the Log Action stores
// them.
type tableFields struct {
	name     string
	areaID   string
	capacity *int
	shape    string
}

func (t tableFields) snapshot() map[string]any {
	var capacity any
	if t.capacity != nil {
		capacity = *t.capacity
	}
	return map[string]any{"name": t.name, "area_id": t.areaID, "capacity": capacity, "shape": t.shape}
}

func fieldsOf(r store.LockTableRow) tableFields {
	return tableFields{name: r.Name, areaID: r.AreaID, capacity: intPtr(r.Capacity), shape: r.Shape}
}

// checkTable validates a table as filled in: every problem at once as
// validation_failed.
func checkTable(in TableInput) (tableFields, error) {
	fields := map[string]string{}
	t := tableFields{name: strings.TrimSpace(in.Name), areaID: in.AreaID, capacity: in.Capacity, shape: in.Shape}
	switch n := utf8.RuneCountInString(t.name); {
	case n == 0:
		fields["name"] = "required"
	case n > MaxTableName:
		fields["name"] = "max"
	}
	if t.areaID == "" {
		fields["area_id"] = "required"
	}
	if c := t.capacity; c != nil && (*c < 1 || *c > MaxCapacity) {
		fields["capacity"] = "range"
	}
	switch t.shape {
	case "":
		t.shape = ShapeSquare
	case ShapeSquare, ShapeRound, ShapeRectangle:
	default:
		fields["shape"] = "oneof"
	}
	if len(fields) > 0 {
		return tableFields{}, httpx.Invalid(fields)
	}
	return t, nil
}

// changes returns the fields of next that differ from prev, as they were
// and as they are.
func changes(prev, next map[string]any) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	for k, v := range next {
		if fmt.Sprint(prev[k]) != fmt.Sprint(v) {
			before[k], after[k] = prev[k], v
		}
	}
	return before, after
}

// tableNameTaken turns the name's unique violation into ErrTableNameTaken.
func tableNameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "tables_outlet_name_key" {
		return ErrTableNameTaken
	}
	return err
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // checked to MaxCapacity
	return &n
}
