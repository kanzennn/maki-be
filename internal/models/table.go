package models

import "time"

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
	MaxTableName   = 30
	MaxCapacity    = 99
)

// A table's shapes.
const (
	ShapeSquare    = "square"
	ShapeRound     = "round"
	ShapeRectangle = "rectangle"
)

// TableStatus is a table's status at a moment.
type TableStatus string

// A table's statuses. The cashier seats an order at an available or an
// occupied table (Selectable), never at an inactive or a scheduled one.
const (
	// TableAvailable is an active table nobody is seated at.
	TableAvailable TableStatus = "available"
	// TableOccupied is a table with customers seated, from a dine-in sale
	// there, until the cashier releases it or the outlet closes.
	TableOccupied TableStatus = "occupied"
	// TableInactive is a table its Manager turned off.
	TableInactive TableStatus = "inactive"
	// TableScheduled is a table a schedule has out of service now.
	TableScheduled TableStatus = "scheduled"
)

// Selectable reports whether the cashier can seat an order at a table in
// this status: another order at an occupied table is a new sale there.
func (s TableStatus) Selectable() bool { return s == TableAvailable || s == TableOccupied }

// Box is a place on an area's grid: the top left cell, and the cells across
// and down.
type Box struct {
	X, Y, W, H int
}

// Table is one of an outlet's tables, with its status at the moment it was
// read.
type Table struct {
	TableID  string
	AreaID   string
	AreaName string
	// AreaOrder is its area's place in the outlet's order, for sorting.
	AreaOrder int
	Name      string
	Capacity  *int
	Shape     string
	// Box is its place on its area's layout; nil while it isn't placed.
	Box *Box
	// Rotation turns its drawing inside its box: 0, 90, 180 or 270.
	Rotation int
	Active   bool
	// ReleasedAt is when the cashier last released it; Scheduled, whether
	// a schedule has it out of service at the moment it was read. Its
	// status comes from them, and from its sales.
	ReleasedAt *time.Time
	Scheduled  bool
	Status     TableStatus
	// Seated is who is seated there, while Status is TableOccupied.
	Seated    *Seating
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Seating is the customers seated at a table: since the first sale that
// seated them, and how many sales there are at the table since.
type Seating struct {
	Since  time.Time
	Orders int
}

// TableInput is a table as a Manager fills it in. Active counts when it is
// made (nil: active); an existing table changes it with SetTableActive.
type TableInput struct {
	Name     string
	AreaID   string
	Capacity *int
	Shape    string // empty is square
	Active   *bool
}

// TableFilter narrows the table list; empty fields don't.
type TableFilter struct {
	AreaID string
	Status TableStatus
	Search string // part of the name
}

// TableFields are a table's own fields, checked.
type TableFields struct {
	Name     string
	AreaID   string
	Capacity *int
	Shape    string
}

// Snapshot is the fields as the Log Action stores them.
func (t TableFields) Snapshot() map[string]any {
	return map[string]any{"name": t.Name, "area_id": t.AreaID, "capacity": NullableInt(t.Capacity), "shape": t.Shape}
}

// TableLock is a table as it is locked for a change.
type TableLock struct {
	TableFields
	TableID    string
	Active     bool
	ReleasedAt *time.Time // when the cashier last released it
}

// Seat is the table a sale seats its customers at, as the order keeps it.
type Seat struct {
	TableID string
	Name    string
}

// BoxResponse is a box on an area's grid, in cells.
type BoxResponse struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// SeatedResponse is who is seated at a table.
type SeatedResponse struct {
	// Since is when the sale that seated the customers was taken.
	Since time.Time `json:"since"`
	// Orders is how many sales there are at the table since.
	Orders int `json:"orders"`
}

// TableResponse is a table as the table endpoints send it.
type TableResponse struct {
	TableID  string          `json:"table_id"`
	Area     AreaRefResponse `json:"area"`
	Name     string          `json:"name"`
	Capacity *int            `json:"capacity"`
	Shape    string          `json:"shape"`
	// Box is its place on its area's layout, in grid cells; null while it
	// isn't placed.
	Box *BoxResponse `json:"box"`
	// Rotation turns its drawing inside its box: 0, 90, 180 or 270.
	Rotation int    `json:"rotation"`
	IsActive bool   `json:"is_active"`
	Status   string `json:"status"`
	// Seated is who is seated there, while the status is occupied.
	Seated    *SeatedResponse `json:"seated"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// NewTableResponse returns the table's response, its times in UTC.
func NewTableResponse(t Table) TableResponse {
	out := TableResponse{
		TableID: t.TableID, Area: AreaRefResponse{AreaID: t.AreaID, Name: t.AreaName}, Name: t.Name,
		Capacity: t.Capacity, Shape: t.Shape, Rotation: t.Rotation, IsActive: t.Active, Status: string(t.Status),
		CreatedAt: t.CreatedAt.UTC(), UpdatedAt: t.UpdatedAt.UTC(),
	}
	if b := t.Box; b != nil {
		out.Box = &BoxResponse{X: b.X, Y: b.Y, W: b.W, H: b.H}
	}
	if s := t.Seated; s != nil {
		out.Seated = &SeatedResponse{Since: s.Since.UTC(), Orders: s.Orders}
	}
	return out
}

// NewTableResponses returns the tables' responses.
func NewTableResponses(ts []Table) []TableResponse {
	out := make([]TableResponse, len(ts))
	for i, t := range ts {
		out[i] = NewTableResponse(t)
	}
	return out
}
