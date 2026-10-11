-- name: InsertJob :exec
-- Adds a job. It runs at run_at, or as soon as a worker is free when run_at
-- is null. Like every query here, it takes times from the database clock
-- (now()), never the application's, so all workers agree on what is due.
INSERT INTO outbox_jobs (outbox_job_id, kind, payload, run_at)
VALUES (@outbox_job_id, @kind, @payload, COALESCE(sqlc.narg(run_at)::timestamptz, now()));

-- name: ClaimJob :one
-- Takes the next due job of one of the given kinds and holds it for
-- lease_seconds. SKIP LOCKED lets several workers claim at once without
-- waiting on each other or taking the same job. A running job whose claim
-- has expired is claimed again: its worker stopped or lost track of it.
UPDATE outbox_jobs
SET status       = 'running',
    attempts     = attempts + 1,
    locked_until = now() + interval '1 second' * @lease_seconds::float8,
    updated_at   = now()
WHERE outbox_job_id = (
    SELECT outbox_job_id
    FROM outbox_jobs
    WHERE kind = ANY (@kinds::text[])
      AND ((status = 'pending' AND run_at <= now())
        OR (status = 'running' AND locked_until <= now()))
    ORDER BY run_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING outbox_job_id, kind, payload, attempts;

-- name: CompleteJob :execrows
-- Records a successful run. CompleteJob, RetryJob and FailJob only match the
-- attempt the worker claimed, so a worker whose claim expired and was taken
-- over can't overwrite the newer run's outcome.
UPDATE outbox_jobs
SET status       = 'done',
    locked_until = NULL,
    completed_at = now(),
    updated_at   = now()
WHERE outbox_job_id = @outbox_job_id
  AND status = 'running'
  AND attempts = @attempts;

-- name: RetryJob :execrows
-- Records a failed run and puts the job back to run again after delay_seconds.
UPDATE outbox_jobs
SET status       = 'pending',
    locked_until = NULL,
    run_at       = now() + interval '1 second' * @delay_seconds::float8,
    last_error   = @last_error::text,
    updated_at   = now()
WHERE outbox_job_id = @outbox_job_id
  AND status = 'running'
  AND attempts = @attempts;

-- name: FailJob :execrows
-- Records a failed run and parks the job as failed, for a person to inspect.
UPDATE outbox_jobs
SET status       = 'failed',
    locked_until = NULL,
    last_error   = @last_error::text,
    updated_at   = now()
WHERE outbox_job_id = @outbox_job_id
  AND status = 'running'
  AND attempts = @attempts;

-- name: DeleteDoneJobs :execrows
-- Deletes jobs that finished more than older_than_seconds ago. Failed jobs
-- are kept until someone deals with them.
DELETE FROM outbox_jobs
WHERE status = 'done'
  AND completed_at < now() - interval '1 second' * @older_than_seconds::float8;

-- name: GetJob :one
SELECT *
FROM outbox_jobs
WHERE outbox_job_id = @outbox_job_id;
