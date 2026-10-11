package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/platform/db"
	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
	"github.com/kervzent/maki-be/internal/platform/outbox/store"
)

func TestEnqueueCommitsWithTheTransaction(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()

	var kept string
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		kept, err = Enqueue(ctx, tx, "test.kept", map[string]string{"menu_id": "m1"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var dropped string
	errRollback := errors.New("roll back")
	err = db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		if dropped, err = Enqueue(ctx, tx, "test.dropped", nil); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("WithTx error = %v, want the rollback error", err)
	}

	job := getJob(t, pool, kept)
	if job.Kind != "test.kept" || job.Status != "pending" || job.Attempts != 0 {
		t.Errorf("kept job = %s/%s/%d attempts, want test.kept/pending/0", job.Kind, job.Status, job.Attempts)
	}
	var payload map[string]string
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload["menu_id"] != "m1" {
		t.Errorf("payload = %s, want {\"menu_id\":\"m1\"}", job.Payload)
	}

	if _, err := store.New(pool).GetJob(ctx, dropped); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("job enqueued in a rolled-back transaction: GetJob error = %v, want no rows", err)
	}
}

func TestEnqueueAtStoresRunAt(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()

	// PostgreSQL keeps microseconds.
	runAt := time.Now().Add(26 * time.Hour).Truncate(time.Microsecond)
	var jobID string
	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		jobID, err = EnqueueAt(ctx, tx, "test.scheduled", nil, runAt)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := getJob(t, pool, jobID).RunAt; !got.Equal(runAt) {
		t.Errorf("run_at = %v, want %v", got, runAt)
	}
}

func TestEnqueueRejectsUnencodablePayload(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	ctx := t.Context()

	err := db.WithTx(ctx, pool, func(tx pgx.Tx) error {
		_, err := Enqueue(ctx, tx, "test.bad", map[string]any{"f": func() {}})
		return err
	})
	if _, ok := errors.AsType[*json.UnsupportedTypeError](err); !ok {
		t.Errorf("error = %v, want a JSON encoding error", err)
	}
}

// enqueue adds a job in a transaction of its own.
func enqueue(t *testing.T, pool *pgxpool.Pool, kind string, payload any) string {
	t.Helper()
	var jobID string
	err := db.WithTx(t.Context(), pool, func(tx pgx.Tx) error {
		var err error
		jobID, err = Enqueue(t.Context(), tx, kind, payload)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return jobID
}

func getJob(t *testing.T, pool *pgxpool.Pool, jobID string) store.OutboxJob {
	t.Helper()
	job, err := store.New(pool).GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatalf("get job %s: %v", jobID, err)
	}
	return job
}

// exec runs SQL that puts a job into a state the worker's own queries can't,
// such as an expired claim.
func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.WithoutCancel(t.Context()), sql, args...); err != nil {
		t.Fatal(err)
	}
}
