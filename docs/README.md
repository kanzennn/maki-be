# maki-be documentation

| Doc | Covers |
| --- | --- |
| [architecture.md](./architecture.md) | Binaries, layout, modules and their dependencies, request lifecycle and route groups, lists, errors, configuration, data access, migrations, data model, stock, the cashier, vouchers, background jobs, testing |
| [security.md](./security.md) | Accounts, passwords, sessions and tokens, permissions and outlet scope, cashier devices, temporary passwords, password reset and email changes, rate limits, the audit log, the HTTP baseline as shipped, what isn't built yet, and the checks done |
| [deployment.md](./deployment.md) | The production stack (Caddy, api, worker, PostgreSQL on one VPS), the image, the pipeline and its deploy, setting the server and GitHub up, a deploy step by step and its rollback, the environment, backups and restore, logs and health, the pre-launch checklist |

`api.md` and `database.md` are added when there is something
project-specific to put in them.

## The short version

- Follows the workspace standard in
  [`docs/gin-architecture-standard.md`](../../../docs/gin-architecture-standard.md);
  this project is its reference implementation.
- The product spec is
  [`Spesifikasi Fitur POS Multi-Outlet.md`](../../Spesifikasi%20Fitur%20POS%20Multi-Outlet.md).
- Modular monolith with two binaries (`cmd/api`, `cmd/worker`), wired by
  hand in each `main.go`. The modules so far are auth, user, outlet, menu,
  device, payment, voucher, order and audit; one talks to another only
  through an interface it declares.
- Sign-in is a username and password, hashed with argon2id. A session is a
  family of refresh tokens that rotate on every use. The 15-minute access
  token is checked against the database on every request, so ending a
  session takes effect at once. The one Owner account comes from
  `api create-owner`.
- Permissions are RBAC: the Owner and Managers hold fixed sets, staff a
  custom role of their outlet. They are loaded with the account on every
  request, an outlet the user may not act in answers 404, and nobody grants
  a permission they don't hold.
- `api/openapi.yaml` is the contract that maki-fe and maki-mobile generate
  their API types from.
- Every error leaves the API as one envelope with a stable `code`.
- A menu's stock is a history of movements, append-only, and a count cached
  on the menu, changed together with the menu locked: adjustments made at
  once never lose one another.
- A sale needs a user who may check out and one of the outlet's devices,
  proving itself with a token only it holds. The device makes the order's
  ID, which is its idempotency key, and its receipt number; the server
  checks every figure against the menus and rates as they are, locking the
  device, the outlet, the menus and the voucher in one order, so the last
  one is sold once, a voucher's last use is taken once, and a resend is
  never a second sale.
- The Owner's vouchers are a percentage or an amount off the subtotal, for
  every outlet or some, with a quota for all of them together. Once a sale
  has used one, its code and type stay, its quota only grows and its expiry
  only moves later; a void gives its sale's use back.
- Background work goes through an outbox table, written in the same
  transaction as the change that needs it; `cmd/worker` runs it with
  retries.

## Known trade-offs and open items

- **Go version.** `go.mod` targets Go 1.27.1, the latest stable release. A
  machine with an older Go downloads 1.27.1 automatically for this module
  (`GOTOOLCHAIN=auto`, the default), so the system install doesn't have to
  change; point the IDE's Go SDK at that toolchain.
- **PostgreSQL in development.** There is no compose file: development uses
  the PostgreSQL 16 server that already runs in Docker on the development
  machine, with its own `maki` database. Production runs PostgreSQL 16 too,
  so nothing that needs a newer major version (such as `uuidv7()` or
  `JSON_TABLE`) goes into the schema.
- **Object storage in development.** There is no local S3 server. Uploads in
  development go to a pair of dev buckets at IDCloudHost, separate from
  production and with their own access key, so they need internet access.
  Tests use an in-memory fake of `Storage`. The dev buckets and key still
  have to be created before uploads are built; their private bucket's CORS
  must allow `http://localhost:3002`.
- **Integration tests use the development PostgreSQL.** The standard starts
  a PostgreSQL container per test run with testcontainers. This project
  already has a PostgreSQL 16 server in Docker, so each integration test
  gets a throwaway schema in it instead (`TEST_DATABASE_URL`, normally the
  `maki` database). CI will run the same tests against a PostgreSQL 16
  service container.
- **Access first, business modules next.** The platform is in place (health
  probes, migrations with `api migrate`, the outbox and its worker, CUID2
  IDs), and so are sign-in, sessions, password and email flows, the Log
  Action, outlets, permissions with roles and staff accounts, and the menu
  with its stock, the cashier's devices and payment methods, and the
  cashier online: orders, cash, voids. Menu photos come with uploads; the
  cashier offline, with sync, comes next;
  [security.md](./security.md#not-built-yet) lists what the security baseline
  still lacks.
- **An outlet's code locks with its first order**, as the spec has it. A
  device numbering its receipts offline will need it locked sooner, from
  the first device activated: an order made offline before the first one
  arrives carries the code as it was.
- **Rate limits live in memory.** That holds while the API runs as a single
  process. A second instance needs a shared store for them first.
- **Tools.** Development tools run through `go run tool@version` from the
  `Makefile` instead of `go.mod` tool directives, and sqlc runs in Docker.
  goose is the exception: its library is a dependency, because the api
  binary applies the migrations itself; its CLI only creates new files.
