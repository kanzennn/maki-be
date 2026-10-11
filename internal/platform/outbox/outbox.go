// Package outbox runs work that should happen because a change committed but
// doesn't need to finish before the response: processing an upload, sending
// an email, a nightly summary.
//
// A service calls Enqueue with the transaction that makes the change, so the
// job exists only if the change commits. cmd/worker runs a Worker, which
// claims due jobs, runs the Handler registered for each job's kind, and
// retries failures with exponential backoff before parking a job as failed.
//
// A job can run more than once — after a crash, or when a run outlives its
// claim — so every Handler must be idempotent.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kervzent/maki-be/internal/platform/id"
	"github.com/kervzent/maki-be/internal/platform/outbox/store"
)

// Job is one run of a job, as its Handler receives it.
type Job struct {
	ID      string
	Kind    string
	Payload json.RawMessage
	// Attempt counts runs from 1. A higher number means earlier runs failed
	// or were cut off, and may have done part of the work.
	Attempt int
}

// Handler runs one kind of job. Returning nil marks the job done; an error
// schedules another attempt. ctx expires after the worker's JobTimeout.
type Handler func(ctx context.Context, job Job) error

// Enqueue adds a job inside tx, so it exists only if tx commits, and returns
// the job's ID. A worker runs it as soon as it is free.
//
// kind names the handler, as <module>.<action>. payload is stored as JSON:
// prefer IDs to copies of data that may change before the job runs. It is
// stored in plain text and kept for a while after the job is done, so a
// secret belongs in it only if it expires quickly, like a password reset
// token.
func Enqueue(ctx context.Context, tx pgx.Tx, kind string, payload any) (string, error) {
	return EnqueueAt(ctx, tx, kind, payload, time.Time{})
}

// EnqueueAt is Enqueue for a job that must not run before runAt, such as
// scheduled work. A zero runAt means now.
func EnqueueAt(ctx context.Context, tx pgx.Tx, kind string, payload any, runAt time.Time) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode %s payload: %w", kind, err)
	}
	params := store.InsertJobParams{OutboxJobID: id.New(), Kind: kind, Payload: data}
	if !runAt.IsZero() {
		params.RunAt = &runAt
	}
	if err := store.New(tx).InsertJob(ctx, params); err != nil {
		return "", fmt.Errorf("enqueue %s job: %w", kind, err)
	}
	return params.OutboxJobID, nil
}
