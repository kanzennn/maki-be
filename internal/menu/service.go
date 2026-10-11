// Package menu is an outlet's menu: its categories, its menus and their
// stock. It owns the menu_categories, menus and stock_movements tables.
package menu

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/audit"
)

// Users is the user module, as far as the menu needs it: the names of who
// changed the stock.
type Users interface {
	Names(ctx context.Context, userIDs []string) (map[string]string, error)
}

// Auditor records Log Action entries inside a transaction; audit.Recorder
// is the real one.
type Auditor interface {
	Record(ctx context.Context, tx pgx.Tx, e audit.Entry) error
}

// Service reads and changes an outlet's categories, menus and stock. Every
// method takes the outlet it acts in; the routes have checked the caller
// may act there.
type Service struct {
	pool  *pgxpool.Pool
	users Users
	audit Auditor
}

// NewService returns the menu module's service.
func NewService(pool *pgxpool.Pool, users Users, auditor Auditor) *Service {
	return &Service{pool: pool, users: users, audit: auditor}
}

// changes returns the fields that differ, before and after. A *string
// compares by its value.
func changes(prev, next map[string]any) (before, after map[string]any) {
	before, after = map[string]any{}, map[string]any{}
	for k, v := range next {
		if fmt.Sprint(deref(prev[k])) != fmt.Sprint(deref(v)) {
			before[k], after[k] = prev[k], v
		}
	}
	return before, after
}

func deref(v any) any {
	if s, ok := v.(*string); ok {
		if s == nil {
			return nil
		}
		return *s
	}
	return v
}

// trimmed is the text without surrounding spaces, or nil for none.
func trimmed(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}
