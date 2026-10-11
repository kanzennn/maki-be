package httpx

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// Page sizes for list endpoints.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Page is the page a list request asks for: ?page= counts from 1, and
// ?page_size= runs from 1 to MaxPageSize, DefaultPageSize when absent.
type Page struct {
	Number int
	Size   int
}

// Offset is how many items come before the page.
func (p Page) Offset() int { return (p.Number - 1) * p.Size }

// BindPage reads ?page= and ?page_size=. A value that isn't a whole number
// in range is a validation_failed error naming the parameter.
func BindPage(c *gin.Context) (Page, error) {
	p := Page{Number: 1, Size: DefaultPageSize}
	fields := map[string]string{}
	if v := c.Query("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fields["page"] = "min"
		}
		p.Number = n
	}
	if v := c.Query("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxPageSize {
			fields["page_size"] = "range"
		}
		p.Size = n
	}
	if len(fields) > 0 {
		return Page{}, Invalid(fields)
	}
	return p, nil
}

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
func NewList[T any](items []T, p Page, total int) List[T] {
	if items == nil {
		items = []T{}
	}
	return List[T]{Data: items, Meta: ListMeta{Page: p.Number, PageSize: p.Size, Total: total}}
}
