package response

import "github.com/kervzent/maki-be/internal/utils/pagination"

// ListMeta is the meta object of a list response.
type ListMeta struct {
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
	Total    int `json:"total"`
}

// List is a list response: one page of the items, and meta carrying the
// total across every page.
type List[T any] struct {
	Data []T      `json:"data"`
	Meta ListMeta `json:"meta"`
}

// NewList returns the list response for a page of items. No items is an
// empty array, never null.
func NewList[T any](items []T, p pagination.Page, total int) List[T] {
	if items == nil {
		items = []T{}
	}
	return List[T]{Data: items, Meta: ListMeta{Page: p.Number, PageSize: p.Size, Total: total}}
}
