package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/platform/db/dbtest"
)

const testKind = "test.job"

// newTestWorker returns a worker over a fresh schema that handles testKind
// with h.
func newTestWorker(t *testing.T, opts Options, h Handler) (*Worker, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.New(t)
	w := NewWorker(pool, slog.New(slog.NewTextHandler(t.Output(), nil)), opts)
	w.Handle(testKind, h)
	return w, pool
}

// runNext claims the next due job and runs it, as one turn of Run would.
func runNext(t *testing.T, w *Worker) {
	t.Helper()
	c, ok, err := w.claim(t.Context(), w.kinds())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !ok {
		t.Fatal("no job was due")
	}
	w.run(t.Context(), c)
}

func assertNothingDue(t *testing.T, w *Worker) {
	t.Helper()
	c, ok, err := w.claim(t.Context(), w.kinds())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if ok {
		t.Fatalf("claimed job %s, want none due", c.ID)
	}
}

func TestWorkerCompletesJob(t *testing.T) {
	t.Parallel()
	var got Job
	w, pool := newTestWorker(t, Options{}, func(_ context.Context, job Job) error {
		got = job
		return nil
	})
	jobID := enqueue(t, pool, testKind, map[string]int{"n": 7})

	runNext(t, w)

	var payload map[string]int
	if err := json.Unmarshal(got.Payload, &payload); err != nil || payload["n"] != 7 {
		t.Errorf("handler payload = %s, want {\"n\": 7}", got.Payload)
	}
	if got.ID != jobID || got.Kind != testKind || got.Attempt != 1 {
		t.Errorf("handler job = %s/%s attempt %d, want %s/%s attempt 1", got.ID, got.Kind, got.Attempt, jobID, testKind)
	}
	job := getJob(t, pool, jobID)
	if job.Status != "done" || job.CompletedAt == nil || job.LockedUntil != nil {
		t.Errorf("job = %s, completed_at %v, locked_until %v; want done, set, nil", job.Status, job.CompletedAt, job.LockedUntil)
	}
	assertNothingDue(t, w)
}

func TestWorkerRetriesFailedJobWithBackoff(t *testing.T) {
	t.Parallel()
	w, pool := newTestWorker(t, Options{}, func(context.Context, Job) error {
		return errors.New("smtp: connection refused")
	})
	jobID := enqueue(t, pool, testKind, nil)

	runNext(t, w)

	job := getJob(t, pool, jobID)
	if job.Status != "pending" || job.Attempts != 1 || job.LockedUntil != nil {
		t.Errorf("job = %s after %d attempts, locked_until %v; want pending after 1, nil", job.Status, job.Attempts, job.LockedUntil)
	}
	if job.LastError == nil || *job.LastError != "smtp: connection refused" {
		t.Errorf("last_error = %v, want the handler's error", job.LastError)
	}
	// run_at and updated_at come from the same now().
	if wait := job.RunAt.Sub(job.UpdatedAt); wait != 30*time.Second {
		t.Errorf("retry scheduled %v after the failure, want 30s", wait)
	}
	assertNothingDue(t, w)
}

func TestWorkerParksJobAfterLastAttempt(t *testing.T) {
	t.Parallel()
	var runs atomic.Int32
	w, pool := newTestWorker(t, Options{MaxAttempts: 2}, func(context.Context, Job) error {
		runs.Add(1)
		return errors.New("still broken")
	})
	jobID := enqueue(t, pool, testKind, nil)

	runNext(t, w)
	exec(t, pool, `UPDATE outbox_jobs SET run_at = now() WHERE outbox_job_id = $1`, jobID)
	runNext(t, w)

	job := getJob(t, pool, jobID)
	if job.Status != "failed" || job.Attempts != 2 || runs.Load() != 2 {
		t.Errorf("job = %s after %d attempts and %d runs, want failed after 2 and 2", job.Status, job.Attempts, runs.Load())
	}
	assertNothingDue(t, w)
}

func TestWorkerTurnsPanicIntoFailure(t *testing.T) {
	t.Parallel()
	w, pool := newTestWorker(t, Options{}, func(context.Context, Job) error {
		panic("boom")
	})
	jobID := enqueue(t, pool, testKind, nil)

	runNext(t, w)

	job := getJob(t, pool, jobID)
	if job.Status != "pending" || job.LastError == nil || *job.LastError != "panic: boom" {
		t.Errorf("job = %s with last_error %v, want pending with \"panic: boom\"", job.Status, job.LastError)
	}
}

func TestWorkerCancelsJobAtTimeout(t *testing.T) {
	t.Parallel()
	w, pool := newTestWorker(t, Options{JobTimeout: 50 * time.Millisecond}, func(ctx context.Context, _ Job) error {
		<-ctx.Done()
		return ctx.Err()
	})
	jobID := enqueue(t, pool, testKind, nil)

	runNext(t, w)

	job := getJob(t, pool, jobID)
	if job.Status != "pending" || job.LastError == nil || *job.LastError != context.DeadlineExceeded.Error() {
		t.Errorf("job = %s with last_error %v, want pending with the deadline error", job.Status, job.LastError)
	}
}

func TestWorkerReclaimsJobWhoseClaimExpired(t *testing.T) {
	t.Parallel()
	var attempt int
	w, pool := newTestWorker(t, Options{}, func(_ context.Context, job Job) error {
		attempt = job.Attempt
		return nil
	})
	jobID := enqueue(t, pool, testKind, nil)

	// A worker claims the job and then goes quiet, as if it had crashed.
	stale, ok, err := w.claim(t.Context(), w.kinds())
	if err != nil || !ok {
		t.Fatalf("claim: %v, %v", ok, err)
	}
	assertNothingDue(t, w)
	exec(t, pool, `UPDATE outbox_jobs SET locked_until = now() - interval '1 second' WHERE outbox_job_id = $1`, jobID)

	runNext(t, w)
	if attempt != 2 {
		t.Errorf("reclaimed run was attempt %d, want 2", attempt)
	}

	// The stale worker wakes up and reports a failure; the newer run's
	// outcome stands.
	w.record(t.Context(), w.logger, stale, errors.New("late failure"))
	if job := getJob(t, pool, jobID); job.Status != "done" || job.LastError != nil {
		t.Errorf("job = %s with last_error %v after a stale report, want done with none", job.Status, job.LastError)
	}
}

func TestWorkerParksJobWhoseLastClaimExpired(t *testing.T) {
	t.Parallel()
	var runs atomic.Int32
	w, pool := newTestWorker(t, Options{MaxAttempts: 1}, func(context.Context, Job) error {
		runs.Add(1)
		return nil
	})
	jobID := enqueue(t, pool, testKind, nil)
	if _, ok, err := w.claim(t.Context(), w.kinds()); err != nil || !ok {
		t.Fatalf("claim: %v, %v", ok, err)
	}
	exec(t, pool, `UPDATE outbox_jobs SET locked_until = now() - interval '1 second' WHERE outbox_job_id = $1`, jobID)

	runNext(t, w)

	job := getJob(t, pool, jobID)
	if runs.Load() != 0 {
		t.Errorf("handler ran %d times, want 0: the job had no attempts left", runs.Load())
	}
	if job.Status != "failed" || job.LastError == nil || *job.LastError != errClaimExpired.Error() {
		t.Errorf("job = %s with last_error %v, want failed with the expired-claim error", job.Status, job.LastError)
	}
}

func TestWorkerLeavesOtherJobsAlone(t *testing.T) {
	t.Parallel()
	w, pool := newTestWorker(t, Options{}, func(context.Context, Job) error { return nil })
	unknown := enqueue(t, pool, "test.other_kind", nil)
	scheduled := enqueue(t, pool, testKind, nil)
	exec(t, pool, `UPDATE outbox_jobs SET run_at = now() + interval '1 hour' WHERE outbox_job_id = $1`, scheduled)

	assertNothingDue(t, w)
	for _, jobID := range []string{unknown, scheduled} {
		if job := getJob(t, pool, jobID); job.Status != "pending" || job.Attempts != 0 {
			t.Errorf("job %s = %s after %d attempts, want untouched", job.Kind, job.Status, job.Attempts)
		}
	}
}

func TestWorkerPurgesOldDoneJobs(t *testing.T) {
	t.Parallel()
	w, pool := newTestWorker(t, Options{}, func(context.Context, Job) error { return nil })
	old := enqueue(t, pool, testKind, nil)
	recent := enqueue(t, pool, testKind, nil)
	runNext(t, w)
	runNext(t, w)
	exec(t, pool, `UPDATE outbox_jobs SET completed_at = now() - interval '8 days' WHERE outbox_job_id = $1`, old)

	w.purge(t.Context())

	if n := countJobs(t, pool); n != 1 {
		t.Fatalf("%d jobs left, want 1", n)
	}
	getJob(t, pool, recent)
}

func TestRunFinishesRunningJobsOnShutdown(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	w, pool := newTestWorker(t, Options{PollInterval: 10 * time.Millisecond}, func(context.Context, Job) error {
		close(started)
		<-release
		return nil
	})
	jobID := enqueue(t, pool, testKind, nil)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(stopped)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the job never started")
	}
	cancel()
	select {
	case <-stopped:
		t.Fatal("Run returned while a job was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return after the job finished")
	}

	if job := getJob(t, pool, jobID); job.Status != "done" {
		t.Errorf("job = %s, want done: a shutdown must not cut a job off", job.Status)
	}
}

func TestBackoff(t *testing.T) {
	want := []time.Duration{
		30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour,
	}
	for i, w := range want {
		if got := backoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
	if got := backoff(1000); got != time.Hour {
		t.Errorf("backoff(1000) = %v, want the one-hour cap", got)
	}
}

func TestErrorText(t *testing.T) {
	if got := errorText(errors.New("bad \x00byte \xff")); got != "bad byte �" {
		t.Errorf("errorText = %q, want NUL dropped and invalid UTF-8 replaced", got)
	}
	long := errorText(errors.New(strings.Repeat("é", maxErrorLength+10)))
	if n := len([]rune(long)); n != maxErrorLength+1 || !strings.HasSuffix(long, "…") {
		t.Errorf("errorText of a long error has %d characters, want %d ending in …", n, maxErrorLength+1)
	}
}

func TestHandleRejectsSecondHandlerForKind(t *testing.T) {
	w := NewWorker(nil, slog.New(slog.DiscardHandler), Options{})
	w.Handle(testKind, func(context.Context, Job) error { return nil })
	defer func() {
		if recover() == nil {
			t.Error("second Handle for the same kind didn't panic")
		}
	}()
	w.Handle(testKind, func(context.Context, Job) error { return nil })
}

func countJobs(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
