// Package table holds an outlet's dining areas, their tables and layouts,
// the schedules that take tables out of service, and how the outlet seats
// its dine-in customers: at those tables, or by number stands. It owns the
// table_areas, tables, table_schedules, table_schedule_items and
// dine_in_settings tables. Whether customers are seated at a table comes
// from the outlet's sales, which the order module keeps; the cashier
// releases a table when they go.
package table

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
	"github.com/kervzent/maki-be/internal/outlet"
)

// An area's canvas is a grid of GridWidth × GridHeight cells, 20 px each on
// a screen at 100%. A table's box is MinSide to MaxSide cells a side; a
// decoration's, one cell to the whole grid.
const (
	GridWidth  = 48
	GridHeight = 32
	MinSide    = 2
	MaxSide    = 16
	// MaxDecorations is how many decorations one area's canvas holds.
	MaxDecorations = 200
	MaxAreaName    = 50
	MaxTableName   = 30
	MaxCapacity    = 99
)

// A table's shapes.
const (
	ShapeSquare    = "square"
	ShapeRound     = "round"
	ShapeRectangle = "rectangle"
)

// What a decoration marks on the canvas.
const (
	KindWall    = "wall"
	KindCashier = "cashier"
	KindDoor    = "door"
)

// Status is a table's status at a moment.
type Status string

// A table's statuses. The cashier seats an order at an available or an
// occupied table (Selectable), never at an inactive or a scheduled one.
const (
	// Available is an active table nobody is seated at.
	Available Status = "available"
	// Occupied is a table with customers seated, from a dine-in sale there,
	// until the cashier releases it or the outlet closes.
	Occupied Status = "occupied"
	// Inactive is a table its Manager turned off.
	Inactive Status = "inactive"
	// Scheduled is a table a schedule has out of service now.
	Scheduled Status = "scheduled"
)

// Selectable reports whether the cashier can seat an order at a table in
// this status: another order at an occupied table is a new sale there.
func (s Status) Selectable() bool { return s == Available || s == Occupied }

// Box is a place on an area's grid: the top left cell, and the cells across
// and down.
type Box struct {
	X, Y, W, H int
}

// Area is one of an outlet's dining areas.
type Area struct {
	AreaID    string
	Name      string
	SortOrder int
	// TableCount is how many tables it holds, placed or not.
	TableCount int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Table is one of an outlet's tables, with its status at the moment it was
// read.
type Table struct {
	TableID  string
	AreaID   string
	AreaName string
	Name     string
	Capacity *int
	Shape    string
	// Box is its place on its area's layout; nil while it isn't placed.
	Box *Box
	// Rotation turns its drawing inside its box: 0, 90, 180 or 270.
	Rotation int
	Active   bool
	Status   Status
	// Seated is who is seated there, while Status is Occupied.
	Seated    *Seating
	CreatedAt time.Time
	UpdatedAt time.Time

	areaOrder int
}

// Decoration is a marker on an area's canvas that isn't a table: a wall,
// the cashier, a door.
type Decoration struct {
	Kind     string
	Box      Box
	Rotation int
}

// Layout is an area's canvas: its decorations and its tables, placed or
// not. Version goes up with each change to it; an editor saves on the
// version it opened.
type Layout struct {
	AreaID      string
	AreaName    string
	Version     int
	Decorations []Decoration
	Tables      []Table
}

// AreaInput is an area as a Manager fills it in.
type AreaInput struct {
	Name string
}

// TableInput is a table as a Manager fills it in. Active counts when it is
// made (nil: active); an existing table changes it with SetActive.
type TableInput struct {
	Name     string
	AreaID   string
	Capacity *int
	Shape    string // empty is square
	Active   *bool
}

// Placement is a table's place on a layout being saved. Nil is a field
// left out.
type Placement struct {
	TableID    string
	X, Y, W, H *int
	Rotation   *int
	Shape      string
}

// DecorationInput is a decoration on a layout being saved. Nil is a field
// left out.
type DecorationInput struct {
	Kind       string
	X, Y, W, H *int
	Rotation   *int
}

// LayoutInput is an area's layout as the editor saves it: the tables placed
// on it, the area's others coming off it, and its decorations. Version is
// the one the editor opened.
type LayoutInput struct {
	Version     *int
	Tables      []Placement
	Decorations []DecorationInput
}

// Filter narrows the table list; empty fields don't.
type Filter struct {
	AreaID string
	Status Status
	Search string // part of the name
}

// Outlets is the outlet module, as far as tables need it: when an outlet
// opens and closes, since a table empties itself at closing.
type Outlets interface {
	Hours(ctx context.Context, outletID string) (outlet.Hours, error)
}

// Orders is the order module, as far as tables need it: when each paid
// dine-in sale at the outlet's tables after since was written, by table ID.
// order.Records is the real one; it reads in the caller's transaction.
type Orders interface {
	TableSales(ctx context.Context, tx pgx.Tx, outletID string, since time.Time) (map[string][]time.Time, error)
}

// Users is the user module, as far as tables need it: the names of who
// made and who cancelled a schedule.
type Users interface {
	Names(ctx context.Context, userIDs []string) (map[string]string, error)
}

// Auditor records Log Action entries inside a transaction; audit.Recorder
// is the real one.
type Auditor interface {
	Record(ctx context.Context, tx pgx.Tx, e audit.Entry) error
}

// Service is the table module.
type Service struct {
	pool    *pgxpool.Pool
	outlets Outlets
	orders  Orders
	users   Users
	audit   Auditor
	now     func() time.Time
}

// NewService returns the table module.
func NewService(pool *pgxpool.Pool, outlets Outlets, orders Orders, users Users, auditor Auditor) *Service {
	return &Service{pool: pool, outlets: outlets, orders: orders, users: users, audit: auditor, now: time.Now}
}
