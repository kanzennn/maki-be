package table

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
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

// ListAreas returns a page of the outlet's areas, in their order, and how
// many there are in all.
func (s *Service) ListAreas(ctx context.Context, outletID string, page httpx.Page) ([]Area, int, error) {
	q := store.New(s.pool)
	total, err := q.CountAreas(ctx, outletID)
	if err != nil {
		return nil, 0, fmt.Errorf("count areas: %w", err)
	}
	rows, err := q.ListAreas(ctx, store.ListAreasParams{
		OutletID:   outletID,
		PageOffset: int32(min(page.Offset(), math.MaxInt32)), //nolint:gosec // capped above
		PageSize:   int32(page.Size),                         //nolint:gosec // httpx caps it at MaxPageSize
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list areas: %w", err)
	}
	out := make([]Area, len(rows))
	for i, r := range rows {
		out[i] = area(store.GetAreaRow(r))
	}
	return out, int(total), nil
}

// Area returns one of the outlet's areas.
func (s *Service) Area(ctx context.Context, outletID, areaID string) (Area, error) {
	r, err := store.New(s.pool).GetArea(ctx, store.GetAreaParams{AreaID: areaID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Area{}, ErrAreaNotFound
	}
	if err != nil {
		return Area{}, fmt.Errorf("load area %s: %w", areaID, err)
	}
	return area(r), nil
}

func area(r store.GetAreaRow) Area {
	return Area{
		AreaID: r.AreaID, Name: r.Name, SortOrder: int(r.SortOrder), TableCount: int(r.TableCount),
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// CreateArea adds an area to the outlet, last in the order, with an empty
// canvas.
func (s *Service) CreateArea(ctx context.Context, p authn.Principal, ip, outletID string, in AreaInput) (Area, error) {
	name, err := checkArea(in)
	if err != nil {
		return Area{}, err
	}
	areaID := id.New()
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		order, err := q.NextAreaOrder(ctx, outletID)
		if err != nil {
			return fmt.Errorf("order of a new area: %w", err)
		}
		if err := areaNameTaken(q.InsertArea(ctx, store.InsertAreaParams{
			AreaID: areaID, OutletID: outletID, Name: name, SortOrder: order,
		})); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "area.created", UserID: p.UserID, OutletID: outletID,
			TargetType: "area", TargetID: areaID, After: map[string]any{"name": name}, IP: ip,
		})
	})
	if err != nil {
		return Area{}, err
	}
	return s.Area(ctx, outletID, areaID)
}

// UpdateArea renames an area. A change of nothing records nothing.
func (s *Service) UpdateArea(ctx context.Context, p authn.Principal, ip, outletID, areaID string, in AreaInput) (Area, error) {
	name, err := checkArea(in)
	if err != nil {
		return Area{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockArea(ctx, q, outletID, areaID)
		if err != nil {
			return err
		}
		if prev.Name == name {
			return nil
		}
		if err := areaNameTaken(q.UpdateArea(ctx, store.UpdateAreaParams{AreaID: areaID, Name: name})); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "area.updated", UserID: p.UserID, OutletID: outletID, TargetType: "area", TargetID: areaID,
			Before: map[string]any{"name": prev.Name}, After: map[string]any{"name": name}, IP: ip,
		})
	})
	if err != nil {
		return Area{}, err
	}
	return s.Area(ctx, outletID, areaID)
}

// ReorderAreas puts the outlet's areas in the order of areaIDs, which must
// list each of them once: otherwise ErrAreasChanged, since the caller's list
// is out of date.
func (s *Service) ReorderAreas(ctx context.Context, p authn.Principal, ip, outletID string, areaIDs []string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		current, err := q.LockAreaIDs(ctx, outletID)
		if err != nil {
			return fmt.Errorf("lock areas: %w", err)
		}
		if !sameSet(current, areaIDs) {
			return ErrAreasChanged
		}
		if slices.Equal(current, areaIDs) {
			return nil
		}
		if err := q.SetAreaOrder(ctx, store.SetAreaOrderParams{OutletID: outletID, AreaIds: areaIDs}); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "area.reordered", UserID: p.UserID, OutletID: outletID, TargetType: "outlet", TargetID: outletID,
			Before: map[string]any{"area_ids": current}, After: map[string]any{"area_ids": areaIDs}, IP: ip,
		})
	})
}

// DeleteArea soft-deletes an area that holds no table, and its canvas with
// it.
func (s *Service) DeleteArea(ctx context.Context, p authn.Principal, ip, outletID, areaID string) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		prev, err := lockArea(ctx, q, outletID, areaID)
		if err != nil {
			return err
		}
		tables, err := q.CountAreaTables(ctx, areaID)
		if err != nil {
			return fmt.Errorf("count the tables of area %s: %w", areaID, err)
		}
		if tables > 0 {
			return ErrAreaNotEmpty.WithDetails(map[string]any{"tables": tables})
		}
		if err := q.DeleteArea(ctx, areaID); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, audit.Entry{
			Action: "area.deleted", UserID: p.UserID, OutletID: outletID,
			TargetType: "area", TargetID: areaID, Before: map[string]any{"name": prev.Name}, IP: ip,
		})
	})
}

// lockArea locks one of the outlet's areas for a change to it, to its
// layout or to one of its tables.
func lockArea(ctx context.Context, q *store.Queries, outletID, areaID string) (store.LockAreaRow, error) {
	r, err := q.LockArea(ctx, store.LockAreaParams{AreaID: areaID, OutletID: outletID})
	if errors.Is(err, pgx.ErrNoRows) {
		return store.LockAreaRow{}, ErrAreaNotFound
	}
	if err != nil {
		return store.LockAreaRow{}, fmt.Errorf("lock area %s: %w", areaID, err)
	}
	return r, nil
}

// checkArea validates an area as filled in, and returns its name trimmed.
func checkArea(in AreaInput) (string, error) {
	name := strings.TrimSpace(in.Name)
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		return "", httpx.Invalid(map[string]string{"name": "required"})
	case n > MaxAreaName:
		return "", httpx.Invalid(map[string]string{"name": "max"})
	}
	return name, nil
}

// areaNameTaken turns the name's unique violation into ErrAreaNameTaken.
func areaNameTaken(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.ConstraintName == "table_areas_outlet_name_key" {
		return ErrAreaNameTaken
	}
	return err
}

// sameSet reports whether want lists every ID in have exactly once. The IDs
// in have are distinct: they're primary keys.
func sameSet(have, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(have)), slices.Sorted(slices.Values(want)))
}
