package config

import (
	"context"
	"errors"
	"fmt"
	"hash/crc64"
	"log/slog"
	"path"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/kervzent/maki-be/migrations"
)

// Migrate applies every pending migration in migrations.FS to the pool's
// current schema, in order, and logs each one it applies. Each migration runs
// in its own transaction, so a failure leaves the schema at the last one that
// succeeded.
//
// It holds a PostgreSQL advisory lock while it runs, so a second run started
// at the same time — two deploys, say — waits for this one instead of racing
// it.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (err error) {
	// Migration state lives in the current schema (normally public), so the
	// lock is per schema too: tests that each migrate a schema of their own
	// never wait on each other.
	var schema string
	if err := pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		return fmt.Errorf("read current schema: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker(
		lock.WithLockID(lockID(schema)),
		lock.WithLockTimeout(1, 300), // retry every second, for up to 5 minutes
	)
	if err != nil {
		return fmt.Errorf("create migration lock: %w", err)
	}

	// goose needs database/sql. Closing this handle returns its connections
	// to the pool; the pool itself stays open.
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { err = errors.Join(err, sqlDB.Close()) }()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithSessionLocker(locker),
		// Only the SQL files in migrations.FS, never Go migrations registered
		// globally by some imported package.
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	results, err := provider.Up(ctx)
	// On a failure, the migrations that did apply are in the PartialError.
	if partial, ok := errors.AsType[*goose.PartialError](err); ok {
		results = partial.Applied
	}
	for _, r := range results {
		logger.Info("migration applied",
			slog.Int64("version", r.Source.Version),
			slog.String("file", path.Base(r.Source.Path)),
			slog.Duration("duration", r.Duration),
		)
	}
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	logger.Info("database schema is up to date",
		slog.String("schema", schema),
		slog.Int64("version", version),
		slog.Int("applied", len(results)),
	)
	return nil
}

// lockID derives the advisory lock key for a schema's migrations.
func lockID(schema string) int64 {
	sum := crc64.Checksum([]byte("maki-migrations:"+schema), crc64.MakeTable(crc64.ECMA))
	return int64(sum) //nolint:gosec // wrapping to a negative key is fine for a lock ID
}
