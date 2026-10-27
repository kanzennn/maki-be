# maki-be

Backend for Maki, the multi-outlet POS. One Go module builds two binaries:
`api` (HTTP) and `worker` (background jobs).

**Stack:** Go 1.27 · Gin · PostgreSQL (pgx, sqlc, goose) · log/slog

An older installed Go is fine: the `go` command downloads the 1.27.1
toolchain for this module by itself.

## Run locally

1. Start the `postgres16` container (PostgreSQL 16, database `maki`) in
   Docker Desktop
2. `cp .env.example .env` — the defaults point at that database — and set
   `JWT_SECRET` in it to the output of `openssl rand -base64 48`
3. `make migrate` — or `go run ./cmd/api migrate` without make
4. `go run ./cmd/api create-owner`, once: it asks for the name, username,
   email and password of the Owner account you sign in with
5. `make dev` — or `go run ./cmd/api` without make; `make worker` (or
   `go run ./cmd/worker`) runs the background jobs and, in development,
   writes each email to its log instead of sending it

The API listens on <http://localhost:8080>: `GET /healthz`, `GET /readyz`,
and the endpoints under `/api/v1` described in `api/openapi.yaml`.

## Commands

| Command | Purpose |
| --- | --- |
| `make dev` | API with live reload |
| `make worker` | Run the worker |
| `make build` | Build both binaries into `bin/` |
| `make test` | `go test ./...`, including integration tests against `TEST_DATABASE_URL` |
| `make lint` | `go vet` and golangci-lint |
| `make generate` | Generate sqlc packages (needs Docker) |
| `make migrate` | Apply migrations to `DATABASE_URL` (`api migrate`, as a deploy does) |
| `make migration name=<name>` | Create a new migration |

On Windows without `make`, run the command from the `Makefile` directly
(for example `go test ./...`), or install it with
`winget install ezwinports.make`.

## CI/CD and deployment

`.github/workflows/ci.yml` lints and tests every push and pull request, and
from `main` pushes the image (`Dockerfile`, `docker build -t maki-be .`) and
deploys it to the VPS once deploys are enabled. The production stack is in
`deploy/`; [docs/deployment.md](docs/deployment.md) sets it all up.

## Structure

Split by layer, not by domain, with one file per resource in each layer
(`voucher_controller.go`, `voucher_service.go`, `voucher_repository.go`):

```
cmd/api, cmd/worker        the two binaries and their wiring
internal/routes/           the router: middleware chain, route groups, <resource>_routes.go
internal/middlewares/      request ID, logging, recovery, the error envelope, security headers, auth, permissions, outlet scope
internal/controllers/      bind the request, call a service, write the response
internal/services/         the business rules and their transactions
internal/repositories/     the queries on each resource's tables (store/: sqlc output)
internal/validations/      request bodies and field rules
internal/models/           entities and responses
internal/jobs/             background jobs' handlers
internal/config/           env, the database pool and transactions, migrations
internal/utils/            apperror (the error catalog), response, jwt, password, i18n, mail, outbox, ratelimit, … — no business rules
internal/tests/            integration tests per resource, and the test that keeps the layers' imports one way
api/openapi.yaml           the API contract
api/test-vectors/          calculations every client repeats, such as an order's totals
migrations/, sqlc/         schema migrations and SQL queries
```

Everything else is in [docs/README.md](docs/README.md).
