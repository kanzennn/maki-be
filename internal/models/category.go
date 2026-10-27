package models

import "time"

// Category is one of an outlet's menu categories.
type Category struct {
	CategoryID string
	Name       string
	Icon       *string // an emoji
	SortOrder  int
	Active     bool
	MenuCount  int // menus in it that aren't deleted
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CategoryInput is a category as a Manager fills it in.
type CategoryInput struct {
	Name string
	Icon *string
}

// CategoryFields are a category's own fields, checked. Active is only read
// from a locked row.
type CategoryFields struct {
	Name   string
	Icon   *string
	Active bool
}

// Snapshot is the fields as the Log Action stores them.
func (c CategoryFields) Snapshot() map[string]any {
	return map[string]any{"name": c.Name, "icon": c.Icon}
}

// CategoryResponse is a category as the category endpoints send it.
type CategoryResponse struct {
	CategoryID string    `json:"category_id"`
	Name       string    `json:"name"`
	Icon       *string   `json:"icon"`
	SortOrder  int       `json:"sort_order"`
	IsActive   bool      `json:"is_active"`
	MenuCount  int       `json:"menu_count"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// NewCategoryResponse returns the category's response, its times in UTC.
func NewCategoryResponse(c Category) CategoryResponse {
	return CategoryResponse{
		CategoryID: c.CategoryID,
		Name:       c.Name,
		Icon:       c.Icon,
		SortOrder:  c.SortOrder,
		IsActive:   c.Active,
		MenuCount:  c.MenuCount,
		CreatedAt:  c.CreatedAt.UTC(),
		UpdatedAt:  c.UpdatedAt.UTC(),
	}
}
