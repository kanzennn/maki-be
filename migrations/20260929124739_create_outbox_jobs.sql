-- +goose Up

-- Background jobs for cmd/worker (internal/platform/outbox). A service inserts
-- a job in the same transaction as the change that needs it, so the job exists
-- only if that change commits.
CREATE TABLE outbox_jobs (
    outbox_job_id VARCHAR(32) PRIMARY KEY,
    -- Names the handler, as <module>.<action>.
    kind          TEXT        NOT NULL,
    payload       JSONB       NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'pending'
        CONSTRAINT outbox_jobs_status_check
            CHECK (status IN ('pending', 'running', 'done', 'failed')),
    -- Runs started so far; a claim counts as one even if its worker dies.
    attempts      INTEGER     NOT NULL DEFAULT 0,
    -- Earliest time the job may run: now, a scheduled time, or a retry.
    run_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- While running: when the claim expires and another worker may take over.
    locked_until  TIMESTAMPTZ,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at  TIMESTAMPTZ
);

-- The worker's claim query: pending jobs that are due, and running jobs whose
-- claim has expired.
CREATE INDEX outbox_jobs_due_idx ON outbox_jobs (run_at)
    WHERE status IN ('pending', 'running');

-- The sweep that deletes old finished jobs.
CREATE INDEX outbox_jobs_done_idx ON outbox_jobs (completed_at)
    WHERE status = 'done';

-- +goose Down

DROP TABLE outbox_jobs;
