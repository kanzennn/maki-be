package dbtest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kervzent/maki-be/internal/config"
)

// errHeldRollback ends a held transaction the test didn't commit.
var errHeldRollback = errors.New("dbtest: held transaction rolled back")

// Hold begins a transaction on db and returns the context that carries it,
// for a test that keeps a transaction open while other requests run against
// the locks it takes: what runs with ctx, a service's call or db.Conn(ctx),
// runs in it. commit ends it, committing; if the test doesn't, the
// transaction rolls back when the test ends.
func Hold(t testing.TB, db *config.Database) (ctx context.Context, commit func() error) {
	t.Helper()
	began := make(chan context.Context)
	end := make(chan bool)
	done := make(chan error, 1)
	go func() {
		// The transaction outlives t.Context(), which ends before the
		// cleanup that rolls it back.
		done <- db.InTx(context.WithoutCancel(t.Context()), func(ctx context.Context) error {
			began <- ctx
			if !<-end {
				return errHeldRollback
			}
			return nil
		})
	}()
	select {
	case ctx = <-began:
	case err := <-done:
		t.Fatalf("begin a held transaction: %v", err)
	}
	var once sync.Once
	var result error
	finish := func(keep bool) error {
		once.Do(func() {
			end <- keep
			result = <-done
		})
		return result
	}
	t.Cleanup(func() { _ = finish(false) })
	return ctx, func() error { return finish(true) }
}
