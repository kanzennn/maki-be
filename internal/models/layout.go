package models

// What a decoration marks on the canvas.
const (
	KindWall    = "wall"
	KindCashier = "cashier"
	KindDoor    = "door"
)

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

// AreaCanvas is an area's canvas as it is stored: its decorations, and the
// version an editor saves on.
type AreaCanvas struct {
	AreaID      string
	Name        string
	Version     int
	Decorations []Decoration
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

// PlacedTable is a Placement, checked: a table's box, turn and shape on the
// layout.
type PlacedTable struct {
	TableID  string
	Box      Box
	Rotation int
	Shape    string
}

// Floor is the outlet's tables as the cashier sees them: each area that
// holds a table, in order, with its decorations and its tables.
type Floor struct {
	Areas []FloorArea
}

// FloorArea is one area of the cashier's floor.
type FloorArea struct {
	AreaID      string
	Name        string
	Decorations []Decoration
	Tables      []Table
}

// DecorationResponse is a decoration on an area's canvas.
type DecorationResponse struct {
	Kind     string `json:"kind"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	Rotation int    `json:"rotation"`
}

// NewDecorationResponses returns the decorations' responses.
func NewDecorationResponses(ds []Decoration) []DecorationResponse {
	out := make([]DecorationResponse, len(ds))
	for i, d := range ds {
		out[i] = DecorationResponse{Kind: d.Kind, X: d.Box.X, Y: d.Box.Y, W: d.Box.W, H: d.Box.H, Rotation: d.Rotation}
	}
	return out
}

// LayoutResponse is an area's layout as GET and PUT …/layout send it.
type LayoutResponse struct {
	Area AreaRefResponse `json:"area"`
	// Version goes up with each change to the layout or to a table in the
	// area; the editor saves on the one it opened.
	Version     int                  `json:"version"`
	Decorations []DecorationResponse `json:"decorations"`
	// Tables are every table in the area, placed or not.
	Tables []TableResponse `json:"tables"`
}

// NewLayoutResponse returns the layout's response.
func NewLayoutResponse(l Layout) LayoutResponse {
	return LayoutResponse{
		Area: AreaRefResponse{AreaID: l.AreaID, Name: l.AreaName}, Version: l.Version,
		Decorations: NewDecorationResponses(l.Decorations), Tables: NewTableResponses(l.Tables),
	}
}

// FloorResponse is GET /outlets/{oid}/pos/tables: the outlet's areas that
// hold tables, in order, as the cashier picks a table from them.
type FloorResponse struct {
	Areas []FloorAreaResponse `json:"areas"`
}

// FloorAreaResponse is one area of the cashier's floor.
type FloorAreaResponse struct {
	AreaID      string               `json:"area_id"`
	Name        string               `json:"name"`
	Decorations []DecorationResponse `json:"decorations"`
	Tables      []FloorTableResponse `json:"tables"`
}

// FloorTableResponse is a table as the cashier sees it: its status says
// whether it can be picked.
type FloorTableResponse struct {
	TableID  string `json:"table_id"`
	Name     string `json:"name"`
	Capacity *int   `json:"capacity"`
	Shape    string `json:"shape"`
	// Box is its place on its area's layout; null while it isn't placed.
	Box      *BoxResponse `json:"box"`
	Rotation int          `json:"rotation"`
	Status   string       `json:"status"`
	// Seated is who is seated there, while the status is occupied.
	Seated *SeatedResponse `json:"seated"`
}

// NewFloorResponse returns the floor's response.
func NewFloorResponse(f Floor) FloorResponse {
	out := FloorResponse{Areas: make([]FloorAreaResponse, len(f.Areas))}
	for i, a := range f.Areas {
		area := FloorAreaResponse{
			AreaID: a.AreaID, Name: a.Name, Decorations: NewDecorationResponses(a.Decorations),
			Tables: make([]FloorTableResponse, len(a.Tables)),
		}
		for j, t := range a.Tables {
			full := NewTableResponse(t)
			area.Tables[j] = FloorTableResponse{
				TableID: full.TableID, Name: full.Name, Capacity: full.Capacity, Shape: full.Shape, Box: full.Box,
				Rotation: full.Rotation, Status: full.Status, Seated: full.Seated,
			}
		}
		out.Areas[i] = area
	}
	return out
}
