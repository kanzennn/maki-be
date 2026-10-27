package config

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect creates the connection pool. Connections open lazily, so a database
// that is down at startup doesn't stop the process; /readyz reports it.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	return pool, nil
}

// DBTX is what a query runs on: the pool, or the transaction a context
// carries. It is the DBTX every sqlc package declares, so either one opens
// that package's queries.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Database is the connection pool, and the transactions the services group
// their writes in. A transaction travels on the context: the repositories
// called with that context run their queries in it, so a service calling
// another service inside its transaction never hands it a pgx.Tx.
type Database struct {
	pool *pgxpool.Pool
}

// NewDatabase returns the database over pool.
func NewDatabase(pool *pgxpool.Pool) *Database {
	return &Database{pool: pool}
}

// Pool is the connection pool itself, for what works with it whole: the
// readiness probe, the migrations, the outbox worker.
func (d *Database) Pool() *pgxpool.Pool { return d.pool }

type txKey struct{}

// InTx runs fn in a transaction, which the context fn gets carries. Called
// inside a transaction already, fn joins that one, so a service's writes
// and those of the services it calls commit or roll back together. It
// commits when fn returns nil and rolls back when fn returns an error or
// panics.
func (d *Database) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	return withTx(ctx, d.pool, func(tx pgx.Tx) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

// Conn is what a query made with ctx runs on: the transaction ctx carries,
// or else the pool.
func (d *Database) Conn(ctx context.Context) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return d.pool
}

// withTx runs fn inside a transaction of its own. It commits when fn returns
// nil and rolls back when fn returns an error or panics.
func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// The rollback error is secondary to the error that caused it.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
