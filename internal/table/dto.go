package table

import "time"

// The caps on request fields only bound what the server reads; the service
// applies the real rules.

type areaRequest struct {
	Name string `json:"name" binding:"required,max=200"`
}

// areaOrderRequest is the body of PUT /outlets/{oid}/areas/order: every
// area of the outlet, in its new order.
type areaOrderRequest struct {
	AreaIDs []string `json:"area_ids" binding:"required,max=1000,dive,max=64"`
}

type areaResponse struct {
	AreaID    string `json:"area_id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	// TableCount is how many tables it holds, placed or not.
	TableCount int       `json:"table_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toAreaResponse(a Area) areaResponse {
	return areaResponse{
		AreaID: a.AreaID, Name: a.Name, SortOrder: a.SortOrder, TableCount: a.TableCount,
		CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(),
	}
}

type tableRequest struct {
	Name     string `json:"name" binding:"required,max=200"`
	AreaID   string `json:"area_id" binding:"required,max=64"`
	Capacity *int   `json:"capacity"`
	Shape    string `json:"shape" binding:"max=16"`
	// IsActive is for a new table, active when left out; an existing one
	// changes it with PATCH …/status.
	IsActive *bool `json:"is_active"`
}

func (r tableRequest) input() TableInput {
	return TableInput{Name: r.Name, AreaID: r.AreaID, Capacity: r.Capacity, Shape: r.Shape, Active: r.IsActive}
}

type statusRequest struct {
	IsActive *bool `json:"is_active" binding:"required"`
}

// dineInModeRequest is the body of PUT /outlets/{oid}/dine-in-mode.
type dineInModeRequest struct {
	Mode string `json:"mode" binding:"required,max=16"`
}

type dineInModeResponse struct {
	Mode string `json:"mode"`
}

type areaRefResponse struct {
	AreaID string `json:"area_id"`
	Name   string `json:"name"`
}

type boxResponse struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type seatedResponse struct {
	// Since is when the sale that seated the customers was taken.
	Since time.Time `json:"since"`
	// Orders is how many sales there are at the table since.
	Orders int `json:"orders"`
}

type tableResponse struct {
	TableID  string          `json:"table_id"`
	Area     areaRefResponse `json:"area"`
	Name     string          `json:"name"`
	Capacity *int            `json:"capacity"`
	Shape    string          `json:"shape"`
	// Box is its place on its area's layout, in grid cells; null while it
	// isn't placed.
	Box *boxResponse `json:"box"`
	// Rotation turns its drawing inside its box: 0, 90, 180 or 270.
	Rotation int    `json:"rotation"`
	IsActive bool   `json:"is_active"`
	Status   string `json:"status"`
	// Seated is who is seated there, while the status is occupied.
	Seated    *seatedResponse `json:"seated"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

func toTableResponse(t Table) tableResponse {
	out := tableResponse{
		TableID: t.TableID, Area: areaRefResponse{AreaID: t.AreaID, Name: t.AreaName}, Name: t.Name,
		Capacity: t.Capacity, Shape: t.Shape, Rotation: t.Rotation, IsActive: t.Active, Status: string(t.Status),
		CreatedAt: t.CreatedAt.UTC(), UpdatedAt: t.UpdatedAt.UTC(),
	}
	if b := t.Box; b != nil {
		out.Box = &boxResponse{X: b.X, Y: b.Y, W: b.W, H: b.H}
	}
	if s := t.Seated; s != nil {
		out.Seated = &seatedResponse{Since: s.Since.UTC(), Orders: s.Orders}
	}
	return out
}

func toTableResponses(ts []Table) []tableResponse {
	out := make([]tableResponse, len(ts))
	for i, t := range ts {
		out[i] = toTableResponse(t)
	}
	return out
}

// layoutRequest is the body of PUT /outlets/{oid}/areas/{aid}/layout: the
// tables placed on it (the area's others come off it), and its
// decorations. Both lists are required, empty or not.
type layoutRequest struct {
	Version     *int                `json:"version" binding:"required"`
	Tables      []placementRequest  `json:"tables" binding:"required,max=1000"`
	Decorations []decorationRequest `json:"decorations" binding:"required,max=1000"`
}

type placementRequest struct {
	TableID  string `json:"table_id"`
	X        *int   `json:"x"`
	Y        *int   `json:"y"`
	W        *int   `json:"w"`
	H        *int   `json:"h"`
	Rotation *int   `json:"rotation"`
	Shape    string `json:"shape"`
}

type decorationRequest struct {
	Kind     string `json:"kind"`
	X        *int   `json:"x"`
	Y        *int   `json:"y"`
	W        *int   `json:"w"`
	H        *int   `json:"h"`
	Rotation *int   `json:"rotation"`
}

func (r layoutRequest) input() LayoutInput {
	in := LayoutInput{
		Version: r.Version, Tables: make([]Placement, len(r.Tables)), Decorations: make([]DecorationInput, len(r.Decorations)),
	}
	for i, t := range r.Tables {
		in.Tables[i] = Placement(t)
	}
	for i, d := range r.Decorations {
		in.Decorations[i] = DecorationInput(d)
	}
	return in
}

type decorationResponse struct {
	Kind     string `json:"kind"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	Rotation int    `json:"rotation"`
}

type layoutResponse struct {
	Area areaRefResponse `json:"area"`
	// Version goes up with each change to the layout or to a table in the
	// area; the editor saves on the one it opened.
	Version     int                  `json:"version"`
	Decorations []decorationResponse `json:"decorations"`
	// Tables are every table in the area, placed or not.
	Tables []tableResponse `json:"tables"`
}

func toLayoutResponse(l Layout) layoutResponse {
	return layoutResponse{
		Area: areaRefResponse{AreaID: l.AreaID, Name: l.AreaName}, Version: l.Version,
		Decorations: toDecorationResponses(l.Decorations), Tables: toTableResponses(l.Tables),
	}
}

func toDecorationResponses(ds []Decoration) []decorationResponse {
	out := make([]decorationResponse, len(ds))
	for i, d := range ds {
		out[i] = decorationResponse{Kind: d.Kind, X: d.Box.X, Y: d.Box.Y, W: d.Box.W, H: d.Box.H, Rotation: d.Rotation}
	}
	return out
}

// scheduleRequest is the body of POST and PUT /outlets/{oid}/table-schedules.
// ConfirmOverlap saves it though another schedule has some of its tables
// out at the same time.
type scheduleRequest struct {
	Title          string     `json:"title" binding:"required,max=400"`
	StartAt        *time.Time `json:"start_at" binding:"required"`
	EndAt          *time.Time `json:"end_at" binding:"required"`
	TableIDs       []string   `json:"table_ids" binding:"required,max=1000,dive,max=64"`
	ConfirmOverlap bool       `json:"confirm_overlap"`
}

func (r scheduleRequest) input() ScheduleInput {
	return ScheduleInput(r)
}

type userRefResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

type scheduleTableResponse struct {
	TableID string          `json:"table_id"`
	Name    string          `json:"name"`
	Area    areaRefResponse `json:"area"`
	// Deleted is whether the table was deleted since.
	Deleted bool `json:"deleted"`
}

type scheduleResponse struct {
	ScheduleID string    `json:"schedule_id"`
	Title      string    `json:"title"`
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`
	// Status is the schedule's now: upcoming, ongoing, finished or
	// cancelled.
	Status      string                  `json:"status"`
	Tables      []scheduleTableResponse `json:"tables"`
	CreatedBy   userRefResponse         `json:"created_by"`
	CancelledAt *time.Time              `json:"cancelled_at"`
	CancelledBy *userRefResponse        `json:"cancelled_by"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

func toScheduleResponse(s Schedule) scheduleResponse {
	out := scheduleResponse{
		ScheduleID: s.ScheduleID, Title: s.Title, StartAt: s.StartAt.UTC(), EndAt: s.EndAt.UTC(), Status: s.Status,
		Tables:    make([]scheduleTableResponse, len(s.Tables)),
		CreatedBy: userRefResponse(s.CreatedBy), CreatedAt: s.CreatedAt.UTC(), UpdatedAt: s.UpdatedAt.UTC(),
	}
	if s.CancelledAt != nil {
		at := s.CancelledAt.UTC()
		out.CancelledAt = &at
	}
	if s.CancelledBy != nil {
		by := userRefResponse(*s.CancelledBy)
		out.CancelledBy = &by
	}
	for i, t := range s.Tables {
		out.Tables[i] = scheduleTableResponse{
			TableID: t.TableID, Name: t.Name, Area: areaRefResponse{AreaID: t.AreaID, Name: t.AreaName}, Deleted: t.Deleted,
		}
	}
	return out
}

// floorResponse is GET /outlets/{oid}/pos/tables: the outlet's areas that
// hold tables, in order, as the cashier picks a table from them.
type floorResponse struct {
	Areas []floorAreaResponse `json:"areas"`
}

type floorAreaResponse struct {
	AreaID      string               `json:"area_id"`
	Name        string               `json:"name"`
	Decorations []decorationResponse `json:"decorations"`
	Tables      []floorTableResponse `json:"tables"`
}

// floorTableResponse is a table as the cashier sees it: its status says
// whether it can be picked.
type floorTableResponse struct {
	TableID  string `json:"table_id"`
	Name     string `json:"name"`
	Capacity *int   `json:"capacity"`
	Shape    string `json:"shape"`
	// Box is its place on its area's layout; null while it isn't placed.
	Box      *boxResponse `json:"box"`
	Rotation int          `json:"rotation"`
	Status   string       `json:"status"`
	// Seated is who is seated there, while the status is occupied.
	Seated *seatedResponse `json:"seated"`
}

func toFloorResponse(f Floor) floorResponse {
	out := floorResponse{Areas: make([]floorAreaResponse, len(f.Areas))}
	for i, a := range f.Areas {
		area := floorAreaResponse{
			AreaID: a.AreaID, Name: a.Name, Decorations: toDecorationResponses(a.Decorations),
			Tables: make([]floorTableResponse, len(a.Tables)),
		}
		for j, t := range a.Tables {
			full := toTableResponse(t)
			area.Tables[j] = floorTableResponse{
				TableID: full.TableID, Name: full.Name, Capacity: full.Capacity, Shape: full.Shape, Box: full.Box,
				Rotation: full.Rotation, Status: full.Status, Seated: full.Seated,
			}
		}
		out.Areas[i] = area
	}
	return out
}
