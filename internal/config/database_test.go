package config_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kervzent/maki-be/internal/config"
	"github.com/kervzent/maki-be/internal/tests/dbtest"
)

// count is how many rows the notes table holds, as a query on db.Conn(ctx)
// sees them.
func count(t *testing.T, ctx context.Context, db *config.Database) int {
	t.Helper()
	var n int
	if err := db.Conn(ctx).QueryRow(ctx, `SELECT count(*) FROM notes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func note(ctx context.Context, db *config.Database, text string) error {
	_, err := db.Conn(ctx).Exec(ctx, `INSERT INTO notes (text) VALUES ($1)`, text)
	return err
}

func newDatabase(t *testing.T) *config.Database {
	t.Helper()
	pool := dbtest.Empty(t)
	if _, err := pool.Exec(t.Context(), `CREATE TABLE notes (text text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return config.NewDatabase(pool)
}

func TestInTxCommitsWhatItsContextWrote(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	ctx := t.Context()
	err := db.InTx(ctx, func(ctx context.Context) error {
		if err := note(ctx, db, "kopi"); err != nil {
			return err
		}
		// Inside, the transaction's own write is there already.
		if n := count(t, ctx, db); n != 1 {
			t.Errorf("inside the transaction: %d notes, want 1", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, ctx, db); n != 1 {
		t.Errorf("after the commit: %d notes, want 1", n)
	}
}

func TestInTxRollsBackOnAnError(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	ctx := t.Context()
	errStop := errors.New("stop")
	err := db.InTx(ctx, func(ctx context.Context) error {
		if err := note(ctx, db, "kopi"); err != nil {
			return err
		}
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("InTx = %v, want the error fn returned", err)
	}
	if n := count(t, ctx, db); n != 0 {
		t.Errorf("after the rollback: %d notes, want 0", n)
	}
}

// A service that runs in a transaction, called by another inside its own,
// writes in the caller's: both go, or neither.
func TestInTxJoinsTheTransactionItIsCalledIn(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	ctx := t.Context()
	errLater := errors.New("the caller fails after the call")
	err := db.InTx(ctx, func(ctx context.Context) error {
		if err := db.InTx(ctx, func(ctx context.Context) error { return note(ctx, db, "inner") }); err != nil {
			return err
		}
		return errLater
	})
	if !errors.Is(err, errLater) {
		t.Fatalf("InTx = %v, want the caller's error", err)
	}
	if n := count(t, ctx, db); n != 0 {
		t.Errorf("%d notes, want the inner write rolled back with the caller's transaction", n)
	}
}

func TestInTxRollsBackOnAPanic(t *testing.T) {
	t.Parallel()
	db := newDatabase(t)
	ctx := t.Context()
	func() {
		defer func() { _ = recover() }()
		_ = db.InTx(ctx, func(ctx context.Context) error {
			if err := note(ctx, db, "kopi"); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if n := count(t, ctx, db); n != 0 {
		t.Errorf("after the panic: %d notes, want 0", n)
	}
}
