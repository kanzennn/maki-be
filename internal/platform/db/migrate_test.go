package db_test

import (
	"log/slog"
	"testing"

	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
)

func TestMigrateAppliesEveryMigrationOnce(t *testing.T) {
	t.Parallel()
	// dbtest.New has already applied every migration to a new schema.
	pool := dbtest.New(t)

	var table *string
	if err := pool.QueryRow(t.Context(), `SELECT to_regclass('outbox_jobs')::text`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table == nil {
		t.Fatal("outbox_jobs doesn't exist after migrating")
	}

	// A second run finds nothing to do.
	if err := db.Migrate(t.Context(), pool, slog.New(slog.NewTextHandler(t.Output(), nil))); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}
