// Package pagination is the page a list request asks for, which the services
// and repositories page their lists by.
package pagination

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
