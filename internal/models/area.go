package models

import "time"

// MaxAreaName is the most characters an area's name has.
const MaxAreaName = 50

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

// AreaInput is an area as a Manager fills it in.
type AreaInput struct {
	Name string
}

// AreaResponse is an area as the area endpoints send it.
type AreaResponse struct {
	AreaID    string `json:"area_id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	// TableCount is how many tables it holds, placed or not.
	TableCount int       `json:"table_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// NewAreaResponse returns the area's response, its times in UTC.
func NewAreaResponse(a Area) AreaResponse {
	return AreaResponse{
		AreaID: a.AreaID, Name: a.Name, SortOrder: a.SortOrder, TableCount: a.TableCount,
		CreatedAt: a.CreatedAt.UTC(), UpdatedAt: a.UpdatedAt.UTC(),
	}
}

// AreaRefResponse names the area a table, a layout or a schedule's table is
// in.
type AreaRefResponse struct {
	AreaID string `json:"area_id"`
	Name   string `json:"name"`
}
