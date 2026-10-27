package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kervzent/maki-be/internal/utils/outbox/store"
)

// Options tunes a Worker. Fields left at zero take the default shown.
type Options struct {
	// Concurrency is how many jobs run at once. Default 4.
	Concurrency int
	// PollInterval is how long the worker waits before looking again when
	// no job is due. Default 1 second.
	PollInterval time.Duration
	// JobTimeout is how long one run may take before its context is
	// cancelled. Default 2 minutes.
	JobTimeout time.Duration
	// MaxAttempts is how many runs a job gets before it is parked as
	// failed. Default 10, which with the backoff below spans about three
	// hours: 30s after the first failure, doubling up to an hour.
	MaxAttempts int
	// Retention is how long done jobs are kept before they are deleted.
	// Default 7 days.
	Retention time.Duration
}

const (
	// A claim lasts this much longer than JobTimeout: time for the worker to
	// record the outcome before another worker may take the job over.
	leaseMargin = time.Minute

	retryBase = 30 * time.Second
	retryMax  = time.Hour

	// How long to wait after a claim failed, e.g. while the database is down.
	claimErrorDelay = 5 * time.Second
	// How often done jobs past Retention are deleted.
	purgeInterval = time.Hour
	// Bounds recording a job's outcome, which still happens during shutdown.
	writeTimeout = 10 * time.Second
	// Longest error text stored with a job, in characters.
	maxErrorLength = 2000
)

// Worker claims due jobs and runs their handlers. Register every handler with
// Handle before calling Run.
type Worker struct {
	queries  *store.Queries
	logger   *slog.Logger
	opts     Options
	handlers map[string]Handler
}

// NewWorker returns a Worker with no handlers.
func NewWorker(pool *pgxpool.Pool, logger *slog.Logger, opts Options) *Worker {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	if opts.JobTimeout <= 0 {
		opts.JobTimeout = 2 * time.Minute
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 10
	}
	if opts.Retention <= 0 {
		opts.Retention = 7 * 24 * time.Hour
	}
	return &Worker{
		queries:  store.New(pool),
		logger:   logger,
		opts:     opts,
		handlers: make(map[string]Handler),
	}
}

// Handle registers the handler for jobs of kind. A second handler for the same
// kind is a wiring mistake, so it panics.
func (w *Worker) Handle(kind string, h Handler) {
	if _, dup := w.handlers[kind]; dup {
		panic("outbox: a handler for " + kind + " is already registered")
	}
	w.handlers[kind] = h
}

// Run claims and runs jobs until ctx is cancelled, then waits for the jobs
// still running. It only claims kinds it has a handler for, so during a
// deploy an older worker leaves a new kind of job to the newer one.
//
// Cancelling ctx doesn't cut a running job off: each run has its own
// JobTimeout. Give the process that long to stop, or the job runs again once
// its claim expires.
func (w *Worker) Run(ctx context.Context) {
	kinds := w.kinds()
	if len(kinds) == 0 {
		w.logger.Warn("outbox worker has no job handlers registered")
	}
	w.logger.Info("outbox worker running",
		slog.Any("kinds", kinds),
		slog.Int("concurrency", w.opts.Concurrency),
	)

	var running sync.WaitGroup
	defer running.Wait()
	running.Go(func() { w.purgeLoop(ctx) })

	// One slot per job that may run at once. A job is only claimed once a
	// slot is free, so its claim never runs down while it waits for a turn.
	slots := make(chan struct{}, w.opts.Concurrency)
	for {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}

		c, ok, err := w.claim(ctx, kinds)
		if err != nil || !ok {
			<-slots
			delay := w.opts.PollInterval
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				w.logger.Error("claim outbox job", slog.String("error", err.Error()))
				delay = claimErrorDelay
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
			continue
		}

		running.Go(func() {
			defer func() { <-slots }()
			w.run(ctx, c)
		})
	}
}

func (w *Worker) kinds() []string {
	return slices.Sorted(maps.Keys(w.handlers))
}

// claimed is a job this worker holds. attempts is the attempt number the
// claim set, which its outcome must match to be recorded.
type claimed struct {
	Job
	attempts int32
}

// claim takes the next due job, if there is one.
func (w *Worker) claim(ctx context.Context, kinds []string) (claimed, bool, error) {
	row, err := w.queries.ClaimJob(ctx, store.ClaimJobParams{
		LeaseSeconds: (w.opts.JobTimeout + leaseMargin).Seconds(),
		Kinds:        kinds,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return claimed{}, false, nil
	}
	if err != nil {
		return claimed{}, false, err
	}
	return claimed{
		Job: Job{
			ID:      row.OutboxJobID,
			Kind:    row.Kind,
			Payload: row.Payload,
			Attempt: int(row.Attempts),
		},
		attempts: row.Attempts,
	}, true, nil
}

// errClaimExpired ends a job whose final attempt's claim expired before the
// attempt was recorded: its worker stopped, or the run outlived its claim.
var errClaimExpired = errors.New("the last attempt did not finish before its claim expired")

// run runs one claimed job and records how it ended.
func (w *Worker) run(ctx context.Context, c claimed) {
	logger := w.logger.With(
		slog.String("job_id", c.ID),
		slog.String("kind", c.Kind),
		slog.Int("attempt", c.Attempt),
	)

	var err error
	if c.Attempt > w.opts.MaxAttempts {
		err = errClaimExpired
	} else {
		// The run has its own deadline and ignores ctx's cancellation, so a
		// shutdown waits for it instead of cutting it off halfway.
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.opts.JobTimeout)
		start := time.Now()
		err = w.call(runCtx, logger, c.Job)
		cancel()
		logger = logger.With(slog.Duration("duration", time.Since(start)))
	}
	w.record(ctx, logger, c, err)
}

// call runs the job's handler, turning a panic into an error so one bad job
// can't take the worker down.
func (w *Worker) call(ctx context.Context, logger *slog.Logger, job Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			logger.Error("job panicked", slog.Any("panic", p), slog.String("stack", string(debug.Stack())))
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return w.handlers[job.Kind](ctx, job)
}

// record stores how a run ended: done, back to pending after a backoff, or
// parked as failed once the job is out of attempts.
func (w *Worker) record(ctx context.Context, logger *slog.Logger, c claimed, jobErr error) {
	// Recorded even during shutdown, so a finished run isn't repeated.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()

	lastAttempt := c.Attempt >= w.opts.MaxAttempts
	delay := backoff(c.Attempt)

	var n int64
	var err error
	switch {
	case jobErr == nil:
		n, err = w.queries.CompleteJob(ctx, store.CompleteJobParams{
			OutboxJobID: c.ID,
			Attempts:    c.attempts,
		})
	case lastAttempt:
		n, err = w.queries.FailJob(ctx, store.FailJobParams{
			OutboxJobID: c.ID,
			Attempts:    c.attempts,
			LastError:   errorText(jobErr),
		})
	default:
		n, err = w.queries.RetryJob(ctx, store.RetryJobParams{
			OutboxJobID:  c.ID,
			Attempts:     c.attempts,
			LastError:    errorText(jobErr),
			DelaySeconds: delay.Seconds(),
		})
	}

	switch {
	case err != nil:
		// The claim runs out and the job runs again.
		logger.Error("record job outcome", slog.String("error", err.Error()))
	case n == 0:
		logger.Warn("job outcome discarded: its claim expired and another run took the job over")
	case jobErr == nil:
		logger.Info("job done")
	case lastAttempt:
		logger.Error("job failed on its last attempt and was parked", slog.String("error", jobErr.Error()))
	default:
		logger.Warn("job failed; will retry",
			slog.String("error", jobErr.Error()),
			slog.Duration("retry_in", delay),
		)
	}
}

// backoff is how long a job waits to run again after its attempt-th run
// failed: 30 seconds, doubling each time, at most an hour.
func backoff(attempt int) time.Duration {
	delay := retryBase
	for range attempt - 1 {
		delay *= 2
		if delay >= retryMax {
			return retryMax
		}
	}
	return delay
}

// errorText makes err storable: valid UTF-8 without NUL bytes, both of which
// PostgreSQL text rejects, and at most maxErrorLength characters.
func errorText(err error) string {
	s := strings.ReplaceAll(strings.ToValidUTF8(err.Error(), "�"), "\x00", "")
	if utf8.RuneCountInString(s) > maxErrorLength {
		s = string([]rune(s)[:maxErrorLength]) + "…"
	}
	return s
}

// purgeLoop deletes done jobs past Retention now and every purgeInterval,
// until ctx is cancelled.
func (w *Worker) purgeLoop(ctx context.Context) {
	ticker := time.NewTicker(purgeInterval)
	defer ticker.Stop()
	for {
		w.purge(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (w *Worker) purge(ctx context.Context) {
	n, err := w.queries.DeleteDoneJobs(ctx, w.opts.Retention.Seconds())
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("delete old outbox jobs", slog.String("error", err.Error()))
		}
		return
	}
	if n > 0 {
		w.logger.Info("deleted old outbox jobs", slog.Int64("count", n))
	}
}
