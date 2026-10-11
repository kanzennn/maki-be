// Package dbtest gives integration tests a PostgreSQL schema of their own,
// with every migration applied, in the database named by TEST_DATABASE_URL.
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/id"
)

// New returns a pool whose connections use a new schema with every migration
// applied, and drops the schema when the test ends. Tests that each call New
// can't see each other's rows, so they may run in parallel; they can't see the
// rest of the database either, since the schema is the whole search_path.
//
// TEST_DATABASE_URL is read from the environment, or else from the .env file
// at the module root. Without it the test is skipped, except in CI, where it
// fails, so integration tests can't silently stop running there.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := Empty(t)
	if err := db.Migrate(t.Context(), pool, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return pool
}

// slots bounds how many of a test binary's tests hold a schema at once. Each
// holds a few connections while it runs, and every package's tests run at
// the same time, against one PostgreSQL and its max_connections (100 by
// default): unbounded, a full `go test ./...` runs out.
var slots = make(chan struct{}, 4)

// maxConns is each test's pool size: enough for the tests that send
// requests at once (the menu module's holds 8 waiting on one lock).
const maxConns = 10

// Empty is New without the migrations: a schema of the test's own with
// nothing in it, for tests of the migrations themselves, such as one that
// needs rows in place before a migration runs.
func Empty(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := databaseURL(t)
	slots <- struct{}{}
	// Registered first, so it runs last: after the pool closes and the schema
	// is dropped.
	t.Cleanup(func() { <-slots })
	ctx := t.Context()

	// Only lowercase letters, digits and underscores, so the name needs no
	// quoting in search_path. The timestamp lets sweep spot leftovers.
	schema := fmt.Sprintf("test_%d_%s", time.Now().Unix(), id.New())

	err := withConn(ctx, url, func(conn *pgx.Conn) error {
		sweepOnce.Do(func() { sweep(ctx, t, conn) })
		_, err := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
		return err
	})
	if err != nil {
		t.Fatalf("create test schema in TEST_DATABASE_URL: %v", err)
	}
	// Registered before the pool's cleanup, so it runs after the pool closes.
	t.Cleanup(func() { drop(t, url, schema) })

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func databaseURL(t testing.TB) string {
	t.Helper()
	// Set but empty in the environment skips the tests even when .env has a
	// value: `TEST_DATABASE_URL= go test ./...`.
	url, ok := os.LookupEnv("TEST_DATABASE_URL")
	if !ok {
		url = fromDotEnv()
	}
	if url != "" {
		return url
	}
	if os.Getenv("CI") != "" {
		t.Fatal("TEST_DATABASE_URL is not set: integration tests must run in CI")
	}
	t.Skip("TEST_DATABASE_URL is not set; skipping integration test")
	return ""
}

// fromDotEnv reads TEST_DATABASE_URL from the .env file next to go.mod, the
// file the binaries load in development. `go test` runs in each package's
// directory, so it walks up to find it. The environment is left alone.
func fromDotEnv() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			env, err := godotenv.Read(filepath.Join(dir, ".env"))
			if err != nil {
				return ""
			}
			return env["TEST_DATABASE_URL"]
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func drop(t testing.TB, url, schema string) {
	// The test's own context is already cancelled when cleanups run.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := withConn(ctx, url, func(conn *pgx.Conn) error { return dropSchema(ctx, conn, schema) })
	if err != nil {
		t.Errorf("drop test schema %s: %v", schema, err)
	}
}

// withConn runs fn on a connection of its own, outside any test schema.
func withConn(ctx context.Context, url string, fn func(*pgx.Conn) error) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	return errors.Join(fn(conn), conn.Close(ctx))
}

var sweepOnce sync.Once

// testSchema matches exactly the names New gives its schemas — never a
// schema someone created by hand — and captures the creation time.
var testSchema = regexp.MustCompile(`^test_(\d{10})_[a-z][0-9a-z]{23}$`)

// sweep drops test schemas left behind by runs that were killed before their
// cleanup ran. It only touches schemas more than an hour old, so a run that is
// still going keeps its own.
func sweep(ctx context.Context, t testing.TB, conn *pgx.Conn) {
	rows, err := conn.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 'test\_%'`)
	if err != nil {
		t.Logf("list leftover test schemas: %v", err)
		return
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Logf("list leftover test schemas: %v", err)
		return
	}
	for _, name := range names {
		m := testSchema.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		created, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || time.Since(time.Unix(created, 0)) < time.Hour {
			continue
		}
		if err := dropSchema(ctx, conn, name); err != nil {
			t.Logf("drop leftover test schema %s: %v", name, err)
		}
	}
}

// dropSchema drops the schema a table at a time, each in a transaction of
// its own, and then what is left. DROP SCHEMA … CASCADE alone holds a lock
// on every object in the schema until it commits, and a few dozen tests
// dropping theirs at once run PostgreSQL out of its lock table
// (max_locks_per_transaction × max_connections): "out of shared memory".
func dropSchema(ctx context.Context, conn *pgx.Conn, schema string) error {
	rows, err := conn.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = $1`, schema)
	if err != nil {
		return err
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, table := range tables {
		if _, err := conn.Exec(ctx, "DROP TABLE IF EXISTS "+pgx.Identifier{schema, table}.Sanitize()+" CASCADE"); err != nil {
			return err
		}
	}
	_, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	return err
}
