# Architecture

maki-be follows the workspace
[Go + Gin architecture standard](../../../docs/gin-architecture-standard.md).
This file records how this project applies it.

## Binaries

| Binary | Role |
| --- | --- |
| `cmd/api` | HTTP API: the middleware chain, the probes, and every module's routes. `api migrate` applies pending migrations and exits; `api create-owner` creates the one Owner account, asking for its details |
| `cmd/worker` | Runs background jobs from the outbox table ([below](#background-jobs-outbox)): so far the auth module's four emails |

Both load `.env` if present (without overriding real environment variables),
parse the configuration with `internal/platform/config`, and shut down
cleanly on Ctrl+C or SIGTERM; a second Ctrl+C kills them at once. Wiring is
manual: each `main.go` shows the whole object graph.

## Layout

```
cmd/
  api/main.go          config → logger → db pool → `migrate`, `create-owner`, or router → http.Server
  api/router.go        middleware chain, route groups, module routes, 404/405 handlers
  api/owner.go         the questions `api create-owner` asks
  worker/main.go       config → logger → db pool → mailer → outbox worker with each module's jobs
internal/
  audit/               the Log Action: Recorder writes action_logs rows inside the caller's transaction
  audit/store/         sqlc output for sqlc/queries/audit.sql
  auth/                sign-in, sessions, passwords, email flows, and their email jobs
  auth/store/          sqlc output for sqlc/queries/auth.sql
  device/              an outlet's cashier devices: activation, revoking, and which device a request comes from
  device/store/        sqlc output for sqlc/queries/device.sql
  health/              /healthz and /readyz
  menu/                an outlet's categories and menus, and their stock: adjustments, the alert, the history
  menu/store/          sqlc output for sqlc/queries/menu.sql
  order/               the cashier: its catalog, orders with their lines and payments, voids, the totals rule
  order/store/         sqlc output for sqlc/queries/order.sql
  outlet/              outlets: the Owner's CRUD, and whether an outlet is open
  outlet/store/        sqlc output for sqlc/queries/outlet.sql
  payment/             Payment Control: which payment methods each outlet's cashier takes
  payment/store/       sqlc output for sqlc/queries/payment.sql
  table/               an outlet's areas, tables and their layouts, schedules, each table's status, the cashier's floor
  table/store/         sqlc output for sqlc/queries/table.sql
  user/                accounts, the permission catalog, custom roles, and managing staff
  user/store/          sqlc output for sqlc/queries/user.sql
  voucher/             the Owner's vouchers: their rules, an outlet's list, the cashier's check, a sale's use
  voucher/store/       sqlc output for sqlc/queries/voucher.sql
  platform/
    config/            env parsing; every problem reported at once
    db/                pgx pool (lazy connect), WithTx, Migrate
    db/dbtest/         a migrated schema of its own for each integration test
    httpx/             error envelope, CodedError, request binding, list pages, request ID on the context
    i18n/              id/en catalogs for error messages and emails
    authn/             access tokens (HS256 JWT), the Principal on the context, the device token's cookie and header
    id/                CUID2 primary keys
    mail/              Mailer: SMTP, or the log in development; renders emails from i18n
    middleware/        RequestID, Logger, Recovery, SecurityHeaders, BodyLimit, CORS, Authenticate,
                       RequirePasswordChanged, RequireOwner, RequireManager, RequireManagerOrOwner,
                       OutletScope, RequirePermission
    outbox/            Enqueue, and the Worker that runs jobs
    outbox/store/      sqlc output for sqlc/queries/outbox.sql
    password/          argon2id hashing and the rules for a new password
    ratelimit/         sliding-window attempt counts, in memory
api/openapi.yaml
api/test-vectors/      calculations every client repeats (an order's totals, a voucher's discount), which the tests read too
migrations/            goose SQL files; migrations.go embeds them into the api binary
sqlc/                  sqlc.yaml and queries/<module>.sql
```

The modules so far are auth, user (with roles and permissions), outlet, menu
(with its stock), device, payment, voucher, table, order (the cashier), and audit,
the spec's log. Planned, from the spec: table, sync, upload. How sign-in and permissions work, and
the security baseline as shipped, is in [security.md](./security.md).

## Modules and their dependencies

A module talks to another only through a service interface that the
*consumer* declares, listing just the methods it uses; `cmd/api/main.go` and
`cmd/worker/main.go` pass in the real service. Methods that write in another
module's transaction take a `pgx.Tx`, so both changes commit together.

| Module | Owns | Uses |
| --- | --- | --- |
| auth | `refresh_tokens`, `auth_tokens` | user (`auth.Users`: accounts, passwords, permissions), outlet (`auth.Outlets`: open or not, and its language) |
| user | `users`, `roles`, `role_permissions`, `user_roles` | auth's sessions, through `user.Sessions` (`auth.Sessions` ends them), without importing auth |
| outlet | `outlets` | user (`outlet.Users`: a new outlet's cashier role, and its Managers), payment (`outlet.Payments`: a new outlet's methods), and whether an outlet has orders (`outlet.Orders`: `order.Records`), without importing order |
| menu | `menu_categories`, `menus`, `stock_movements` | user (`menu.Users`: the names of who changed the stock, deleted accounts included) |
| device | `devices` | user (`device.Users`: the names of who activated and revoked devices) |
| payment | `outlet_payment_methods` | nothing |
| voucher | `vouchers`, `voucher_outlets` | outlet (`voucher.Outlets`: outlets' codes and names, deleted ones too), device (`voucher.Devices`: the device a cashier checks a code from), and the sales that used a voucher (`voucher.Orders`: `order.Records`), without importing order |
| table | `table_areas`, `tables`, `table_schedules`, `table_schedule_items` | outlet (`table.Outlets`: an outlet's hours and time zone), user (`table.Users`: the names of who made and cancelled schedules, deleted accounts included), and the sales at a table (`table.Orders`: `order.Records`), without importing order |
| order | `orders`, `order_items`, `payments` | menu (`order.Menus`: the catalog, and the stock a sale takes and a void puts back), device (`order.Devices`: which device a request comes from, held for a sale), payment (`order.Payments`: the methods that are on), outlet (`order.Outlets`: the receipt's outlet and its rates, held for a sale), voucher (`order.Vouchers`: the use a sale takes and its void gives back), table (`order.Tables`: the table a sale is at, held for it), user (`order.Users`: cashiers' names) |
| audit | `action_logs` | nothing; every module records through its own `Auditor` interface |

The directions are one way: auth → user, auth → outlet, outlet → user,
outlet → payment, menu → user, device → user, voucher → outlet and device,
table → outlet and user, and order → menu, device, payment, outlet, voucher,
table and user. Four needs run against them, and are met the way
`auth.Sessions` meets the user module's: a type in the module that owns the
table, holding nothing and working in the caller's transaction, which
`main.go` hands over. `auth.Sessions` ends a user's sessions;
`order.Records` tells the outlet module whether an outlet has orders, which
lock its code, the voucher module which sales used a voucher
(`VoucherUses`), and the table module which sales were at a table
(`TableSales`), which make it occupied.

The user module's tests look outlets up in the database directly, since
importing the outlet module would make a cycle, and the outlet module's
tests count orders the same way; the menu module's tests do the same for
`OutletScope`, and name users through the real user service. The voucher
module's tests take uses with `Redeem`, as a sale does, and give the order
module's history as a fake; so do the table module's, with sales as a fake;
the order module's tests use every module it depends on, the voucher and
table modules included, for real.

## Request lifecycle

`cmd/api/router.go` installs the middleware in this order:

1. **RequestID** — reuses a well-formed incoming `X-Request-ID` (up to 64
   characters of `A–Z a–z 0–9 - _`) or generates one; puts it on the context
   and the response.
2. **Logger** — one JSON line per request with the route *pattern*, never the
   raw path, and the user's ID once they're signed in. It sits outside
   Recovery so a panicking request is still logged with its 500.
3. **Recovery** — turns a panic into the generic 500 envelope and logs the
   stack server-side.
4. **SecurityHeaders** — `X-Content-Type-Options: nosniff` and
   `Cache-Control: no-store` on every response.
5. **BodyLimit** — 1 MB; reading past it fails with `*http.MaxBytesError`,
   which becomes a 413.
6. **CORS** — exact origins from `CORS_ALLOWED_ORIGINS`, with credentials.
   A same-origin request, whose `Origin` host equals its `Host`, skips the
   list: browsers send `Origin` on every POST, and the web app is served from
   the API's own host. With no origins configured CORS is skipped entirely.

Routes under `/api/v1` sit in five groups:

| Group | Middleware | Holds |
| --- | --- | --- |
| public | none | Sign-in, refresh, sign-out, forgot and reset password, verify email |
| authed | `Authenticate`: a valid access token and a live session; loads the Principal — account, permissions, outlet — onto the context | `GET /me` and `PUT /me/password`: all a user with a temporary password may do |
| protected | also `RequirePasswordChanged`: 403 `password_change_required` until that password is replaced | The rest of `/me`, and `GET /permissions` |
| owner | also `RequireOwner`: 403 `forbidden` for anyone else | `/outlets`, `/outlets/:oid`, `/outlets/:oid/status` |
| scoped | also `OutletScope` on `/outlets/:oid`: 404 `outlet_not_found` for an outlet the principal may not act in | An outlet's roles, users, categories, menus and stock, devices, and the cashier; each route adds `RequirePermission` (or `RequireAnyPermission`) with its code, or for devices the fixed roles' `RequireManager` and `RequireManagerOrOwner` |

Modules register on protected, owner or scoped, and on authed only when an
endpoint must work before a temporary password is replaced. Which route needs
which permission is in [security.md](./security.md#permissions). The probes
stay public and outside `/api/v1`.

`TestRoutesMatchTheContract` (`cmd/api`) compares the routes Gin serves with
the paths and methods in `api/openapi.yaml`, both ways, so the contract the
clients generate from can't drift from the code.

## Lists

A list endpoint takes `?page=` (from 1) and `?page_size=` (1 to 100, 20 by
default) through `httpx.BindPage`, and answers `httpx.NewList`:
`{"data": [...], "meta": {"page", "page_size", "total"}}`, with `data` never
`null`. A value out of range is 422 `validation_failed` naming the parameter.
Search terms (`?q=`) match with `ILIKE`, their `%` and `_` escaped.

## Errors

Every error response is the same envelope:

```json
{ "error": { "code": "stock_insufficient", "message": "Only 3 left in stock.", "details": { "available": 3 }, "request_id": "…" } }
```

Handlers call `httpx.Fail(c, logger, err)`:

- An error that implements `httpx.CodedError` (usually declared with
  `httpx.NewError(status, code)` in a module's `errors.go`) is rendered with
  its own status and code, even when wrapped with `%w`. Its `Error()` string
  is the code, which is what logs show.
- `*http.MaxBytesError` becomes 413 `payload_too_large`.
- Anything else is logged with the request ID and rendered as a generic 500
  `internal_error`, so internal details never reach the client.

Unknown routes return 404 `not_found` and wrong methods return 405
`method_not_allowed`, both in the same envelope.

## Languages

`message` is never written in Go. It is the `error.<code>` entry of the
catalogs in `internal/platform/i18n/locales/` (`id.json`, `en.json`), in the
language of the request's `Accept-Language` header, with the error's
`details` filling template fields such as `{{.available}}`. The response's
`Content-Language` header says which language was used.

- Indonesian is the default: no header, or no language with a catalog,
  gives `id`. A key missing in one language falls back to `id`.
- Adding a language means adding `locales/<tag>.json`; nothing else changes.
- `TestCatalogsAgree` fails when a catalog lacks a key, has an extra one,
  or uses different template fields than `id.json`. That is the check the
  spec asks CI to make.
- `TestEveryErrorCodeHasAMessage` reads every Go file in the module and
  fails for a code passed to `httpx.NewError` or `httpx.WriteError` that has
  no message, or that isn't a string literal. A module's new error therefore
  fails the tests, naming the key to add, before a client can see a message
  ID.
- Emails use the same catalogs, in the recipient's stored language
  (`users.locale`, then the outlet's, then `id`). Logs stay in English.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `APP_ENV` | `production` | `development` or `production`; anything but development runs Gin in release mode |
| `HTTP_ADDR` | `:8080` | |
| `DATABASE_URL` | — | Required |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `CORS_ALLOWED_ORIGINS` | empty | Comma-separated exact origins; `*` and paths are rejected |
| `TRUSTED_PROXIES` | empty | IPs or CIDRs whose `X-Forwarded-For` is trusted; empty trusts none |
| `JWT_SECRET` | — | Required, at least 32 bytes (`openssl rand -base64 48`); signs access tokens. Changing it only makes clients refresh early |
| `WEB_BASE_URL` | `http://localhost:3002` in development | The web app's origin, where the links in emails lead. Required outside development |
| `SMTP_HOST` | empty | Worker only. Empty in development logs emails instead of sending them; the worker won't start without it elsewhere |
| `SMTP_PORT` | `587` (`465` with `SMTP_TLS=tls`) | |
| `SMTP_TLS` | `starttls` | `starttls`, `tls` (implicit), or `none` — development only, for a local catcher such as Mailpit |
| `SMTP_USERNAME`, `SMTP_PASSWORD` | empty | Set both or neither |
| `SMTP_FROM` | — | Sender as `Name <address>`; required with `SMTP_HOST` |

`APP_ENV` defaults to production so that a forgotten variable never turns on
development behaviour in a deploy.

## Email

Only the worker sends email, always from an outbox job, through the
`mail.Mailer` interface that `mail.New` picks at startup:

- **SMTP** (`SMTP_HOST` set): one connection per message. With `starttls`
  it refuses to send when the server doesn't offer STARTTLS, rather than
  fall back to plain text; `tls` connects over TLS from the start. The
  server has accepted a message once DATA is answered, so a failed QUIT
  isn't reported — reporting it would send the email twice.
- **Log** (development without `SMTP_HOST`): writes the recipient, subject
  and text body to the worker's log, so the link of a password reset can be
  opened from there.

`mail.Render` builds a message from the i18n catalogs in the recipient's
language: `email.<name>.subject`, `.body` (paragraphs separated by a blank
line) and `.action` (the button's label), inside the shared layout in
`internal/platform/mail/templates/layout.html`. Every message has a
plain-text part and an HTML alternative. It fails — so the job retries and
eventually parks — for an unknown email or missing template data, instead
of sending `<no value>` to someone. The emails so far are `password_reset`,
`email_verify`, `password_changed` and `email_changed`.

## Data access

- The pool connects lazily: the API starts even when PostgreSQL is down, and
  `/readyz` returns 503 `not_ready` until it answers.
- Services group writes with `db.WithTx`, which commits on success and rolls
  back on an error or a panic.
- Primary keys come from `id.New()` (CUID2), never from the database;
  `id.Valid` checks the shape of IDs that clients send.
- Queries are written in `sqlc/queries/<module>.sql` and generated into
  `internal/<module>/store/` — one package per module, so a module can't
  reach another module's queries. `omit_unused_structs` keeps each package
  to the row types its own queries use, and timestamps come out as
  `time.Time` (`*time.Time` when nullable) instead of pgx wrappers.
- sqlc runs from its Docker image (`make generate`). From Git Bash without
  make, stop MSYS rewriting the container paths:
  `MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/src" -w /src/sqlc sqlc/sqlc:1.31.1 generate`.

## Migrations

- One goose SQL file per change in `migrations/`, named
  `<UTC timestamp>_<name>.sql`; `make migration name=<name>` creates one. A
  migration that has run anywhere shared is never edited — write a new one.
- `migrations/migrations.go` embeds the files, so the api binary carries
  them: `api migrate` (`make migrate` locally) applies the pending ones to
  `DATABASE_URL`, each in its own transaction, and logs each file. Deploys
  run it before starting the new version; no goose CLI or source tree is
  needed on the server.
- A PostgreSQL advisory lock, keyed by schema, makes a second `api migrate`
  started at the same time wait for the first instead of racing it.
- goose records what has run in `goose_db_version`. sqlc reads the same files
  as its schema and ignores the `-- +goose Down` sections.
- `migrations/schema_test.go` breaks each rule the schema enforces and checks
  the right constraint refuses it.

## Data model

| Table | Owner | Notes |
| --- | --- | --- |
| `users` | user | Everyone who signs in. Username lowercase, unique forever (`users_username_key`, never reused after a delete). Exactly one Owner (`users_single_owner_key`), who has an email, no outlet, and can't be deactivated or deleted; everyone else belongs to one outlet (`users_outlet_id_fkey`). `is_manager` marks a Manager; `must_change_password` a password someone else set. Soft delete |
| `outlets` | outlet | Code `^[A-Z0-9]{2,10}$`, unique forever (`outlets_code_key`, printed in receipt numbers). IANA time zone and default language. Opening hours both set or both empty, and not equal (`outlets_hours_check`); a closing time before the opening time runs past midnight. Tax and service charge in basis points (`tax_bps`, `service_bps`, 0–10000), so money is never computed in floats; the API speaks percentages with two decimals. Soft delete, only once inactive (`outlets_deleted_check`) |
| `roles` | user | An outlet's custom roles; the Owner's and Managers' are fixed in code, not rows. Name unique per outlet, in any case, among roles not deleted (`roles_outlet_name_key`). Soft delete |
| `role_permissions` | user | A role's permission codes (`role_permissions_code_check` checks their shape; the catalog is in code) |
| `user_roles` | user | A staff member's role: one per user for now (`user_roles_user_key`), kept in a table of its own so several roles need no data migration. Its composite foreign keys (`user_roles_user_fkey`, `user_roles_role_fkey`) make the role's outlet the user's own, keep the Owner, who has no outlet, out, and stop a user moving outlet while holding a role |
| `refresh_tokens` | auth | One row per refresh token, hashed. `family_id` is the session: alive while the family has a token that is neither replaced, revoked nor expired |
| `auth_tokens` | auth | Single-use email tokens (`password_reset`, `email_verify`), hashed. An email change waits in `email` here until confirmed; the user's current address stays in place |
| `menu_categories` | menu | An outlet's categories. Name unique per outlet, in any case, among categories not deleted (`menu_categories_outlet_name_key`). An emoji icon of 1–8 characters (`menu_categories_icon_check`); the service also refuses letters, spaces and anything without a pictograph. `sort_order` is the cashier's order. Soft delete, only while it holds no menu that isn't deleted |
| `menus` | menu | Price in whole Rupiah, 0–100.000.000 (`menus_price_check`). Its category is its outlet's own (`menus_category_fkey`, on the category and outlet together). SKU `^[A-Za-z0-9._-]{1,32}$`, unique per outlet in any case among menus not deleted (`menus_outlet_sku_key`). `stock_qty` is a cache: always the last stock movement's `stock_after` ([below](#stock)). It has no CHECK ≥ 0, so a sale made offline can take it below zero. `stock_alert` ≥ 0, where 0 or NULL is off. `image_key` waits for uploads. Soft delete |
| `stock_movements` | menu | Every change to a tracked menu's count. `type` is `initial` (the count tracking starts from), `in`, `out` or `set` (a stock take), or `sale` and `void` for the cashier, which name their order and nothing else does (`stock_movements_order_check`, `stock_movements_order_fkey`). Each row keeps the change and the count it left, the user, and a sale's order. `reason` is `initial` for a start, one of `restock`, `damaged`, `correction` and `other` for an adjustment, and none for a sale or void (`stock_movements_reason_check`). The change goes the way its type says (`stock_movements_direction_check`), and only a sale may leave the count below zero (`stock_movements_stock_after_check`). The menu is its outlet's own (`stock_movements_menu_fkey`). Append-only like `action_logs` |
| `devices` | device | An outlet's cashier devices. `code` `^K[0-9]{2,4}$`, unique per outlet and never reused, since receipt numbers carry it (`devices_outlet_code_key`). The token is kept as its SHA-256 (`token_hash`, unique). `platform` `web` or `android`. Who activated it and when it was last seen; revoked by whom and when, both or neither (`devices_revoked_check`). The app never deletes one: a revoked device stays, with its code |
| `outlet_payment_methods` | payment | One row per outlet and method (`cash`, `qris`, `debit`), on or off. A new outlet gets cash on and the others off, in the transaction that creates it; the migration gave existing outlets the same |
| `vouchers` | voucher | The Owner's discount vouchers. Code `^[A-Z0-9]{3,20}$`, unique among vouchers not deleted (`vouchers_code_key`): a deleted one was never used, so its code is free again. `type` `percent` (`value` in basis points, at most 10000) or `amount` (rupiah) (`vouchers_value_check`); `max_discount` only for a percentage (`vouchers_max_discount_check`); `min_spend` optional. `quota` 1–1.000.000 uses in every outlet together, `used_count` 0 to the quota (`vouchers_used_count_check`). `expired_at` after `start_at`, which may be empty (`vouchers_period_check`). `first_used_at` is set by the first sale and never cleared: a voucher with uses has one (`vouchers_first_used_check`), and only a voucher without one may be deleted (`vouchers_deleted_check`). `voucher_status(...)` is the one definition of a voucher's status at a moment ([below](#vouchers)). Soft delete |
| `voucher_outlets` | voucher | The outlets a voucher is for when it isn't `all_outlets`. Both keys are foreign keys |
| `table_areas` | table | An outlet's areas (Indoor, Lantai 2), each with its own layout. Name 1–50, unique per outlet in any case among areas not deleted (`table_areas_outlet_name_key`). `sort_order` is the cashier's order. `decorations` is the layout's walls, cashier and doors, a JSON array (`table_areas_decorations_check`). `layout_version` goes up with every save of the layout and every table of the area made, changed or deleted, never with a table turned on or off. Soft delete, only while it holds no table |
| `tables` | table | Name 1–30, unique per outlet in any case among tables not deleted (`tables_outlet_name_key`). Its area is its outlet's own (`tables_area_fkey`, on the area and outlet together). Seats 1–99 or none (`tables_capacity_check`); shape `square`, `round` or `rectangle` (`tables_shape_check`). Its place on its area's layout is a box of whole cells, `x`, `y`, `w`, `h` all set or none (a table not placed), 2–16 cells a side, inside the 48 × 32 grid (`tables_box_check`), with a rotation of 0, 90, 180 or 270 (`tables_rotation_check`). `released_at` is when the cashier last released it. Soft delete |
| `table_schedules` | table | A time the outlet's chosen tables are out: a title 1–100, `end_at` after `start_at` (`table_schedules_period_check`), and a cancellation's time and who, both or neither (`table_schedules_cancelled_check`). `table_schedule_status(...)` is the one definition of its status at a moment ([below](#tables)). Kept as history: cancelled, never deleted |
| `table_schedule_items` | table | A schedule's tables; the table and the schedule are the same outlet's (both foreign keys carry it) |
| `dine_in_settings` | table | How an outlet seats its dine-in customers: `mode` is `table`, at the tables of its layout, or `number`, by the number stands the cashier hands out (`dine_in_settings_mode_check`), with who changed it and when. An outlet without a row seats them at tables |
| `orders` | order | The cashier's transactions. `order_id` and `order_no` come from the device. `order_no` is `OUTLETCODE-KCODE-YYYYMMDD-NNNN` (`orders_order_no_check`), unique per outlet (`orders_outlet_order_no_key`); its device is the outlet's own (`orders_device_fkey`, on the device and outlet together). The rates it was charged at are kept with it, and CHECKs hold its figures to the totals rule: the service on the subtotal less the voucher's discount, the tax on that plus the service, each rounded half up, and the total their sum (`orders_service_check`, `orders_tax_check`, `orders_total_check`). A voucher's sale names it (`orders_voucher_fkey`) and copies its code (`voucher_code`), both or neither, and a discount needs a voucher (`orders_voucher_check`); a voucher may give none, rounded to zero on a small subtotal. A sale at a table names it (`orders_table_fkey`, a table of the same outlet) and copies its name (`table_name`), both or neither, and only a dine-in order has one (`orders_table_check`). A sale by a number stand keeps the stand's number (`stand_number`: 1–10 capitals, digits and hyphens, a hyphen only between them), only a dine-in one, and never with a table (`orders_stand_number_check`). `created_at` is `clock_timestamp()`, the moment the row is written, not the transaction's start: with `released_at`, written the same way under the table's lock, "sold after it was released" follows the order the locks were taken in. `request_hash` is the SHA-256 of the order as the device sent it. `client_created_at` is when the device made it. A void has its time, who and why, all three or none (`orders_void_check`). Kept as made: triggers refuse DELETE and TRUNCATE, and any UPDATE but paid → void of the void columns (SQLSTATE 42501) |
| `order_items` | order | An order's lines, in order (`position`), with the menu's name and price copied at the sale; qty 1–999, `line_total` = price × qty. The menu is the order's outlet's own (`order_items_menu_fkey`). Triggers refuse UPDATE, DELETE and TRUNCATE |
| `payments` | order | One per order (`payments_order_key`). Cash keeps what was handed over and the change, never less than the amount (`payments_cash_check`). Changes only from paid to void |
| `action_logs` | audit | The Log Action. Append-only: triggers refuse UPDATE, DELETE and TRUNCATE (SQLSTATE 42501). `outlet_id` references the outlet it happened in (`action_logs_outlet_id_fkey`) |
| `outbox_jobs` | platform/outbox | Background jobs ([below](#background-jobs-outbox)) |

A table has one owning module, the only one with queries on it; other
modules go through that module's service.

## Stock

- **One way in.** A tracked menu's count changes only through
  `recordMovement` (`internal/menu/stock.go`): it writes the movement and sets
  `menus.stock_qty` to its `stock_after`, in the caller's transaction, so the
  cache always equals the history. A new tracked menu, and one whose
  tracking is turned on, start with an `initial` movement.
- **One at a time.** Whatever changes a count first locks the menu
  (`LockMenu`, `SELECT … FOR UPDATE`, standard §6), so adjustments made
  together each start from the count the one before left. None is lost:
  `TestConcurrentAdjustments` holds the menu's lock until every request waits
  for it (it counts them in `pg_locks`), so without the lock they would all
  read the same count, and the test fails every time.
- **In order.** `stock_movements.created_at` is `clock_timestamp()`, when the
  row is written, not `now()`, when its transaction began: a change that
  waited for the lock lands, and sorts, after the one it waited for. The
  history reads newest first by it.
- **Its limits.** An adjustment never takes the count below zero (409
  `stock_insufficient`, with `details.available`) nor above 1,000,000, the
  most a count may be, so it always fits the `INTEGER` column. An untracked
  menu has no count to adjust and no alert (409 `stock_not_tracked`).
- **Its condition** — `untracked`, `out` (0 or below), `low` (at or below the
  alert) or `ok` — is the `menu_stock_status()` SQL function, the one
  definition the list's filter and every menu in a response use.

## The cashier

- **A device sells, not just a user.** Taking an order needs `checkout.create`
  and one of the outlet's devices in use. The outlet's Manager activates a
  device on the device itself (`POST /outlets/{oid}/devices`), which gets
  the next code (`K01`, `K02`, …, under a per-outlet advisory lock, never
  reused) and a token only it holds: a browser as the `maki_device` cookie,
  the Android app in the body, for its secure storage and the
  `X-Device-Token` header. `authn.DeviceToken` reads either.
  ([security.md](./security.md#cashier-devices) has the rest.)
- **The catalog** (`GET /outlets/{oid}/pos/catalog`) is one answer, so a
  device can later keep it offline whole: the outlet for the receipt with
  its rates in basis points, the payment methods on, how the outlet seats
  its dine-in customers (`dine_in_mode`), and the active menus of active
  categories in the cashier's order (`ListPosMenus`: what can be
  sold first, out of stock last, then by category and name), with the
  categories that hold them. Their item counts come from that list, so the
  two always agree.
- **The device numbers its receipts.** `GET /outlets/{oid}/pos/device` gives
  the prefix (`BDG01-K01`), the outlet's day and the next count: one past
  the highest the device used that day (`LastOrderNumber`, over the
  outlet's day of `client_created_at`). An order's number must carry the
  device's prefix and the outlet's day of `client_created_at`: an online
  device can't number for another, and a device's count of a day is its
  orders made that day. A number another order took (a second tab) is 409
  `order_no_taken`; the cashier asks for the next one and sends again.
- **The ID is the idempotency key.** `POST /outlets/{oid}/orders` hashes the
  order as sent, checked and normalised (notes trimmed, the time in UTC), and
  a resend of a taken order is answered 200 with it as it was taken, from a
  revoked device too. The same ID with anything else, or from another
  outlet, is 409 `order_id_conflict`, which tells nothing of the order.
  Whatever doesn't fit as sent is a 422 with each field's rule, a line's by
  its path (`items.0.qty`).
- **One transaction, locks in one order.** The device first (`Hold`,
  `FOR UPDATE`, which also marks it seen): a revocation waits for the sale,
  and the device's sales go one at a time, so a resend that came while the
  first was on its way finds its order once it gets the lock. Then the
  outlet (`HoldForSale`, `FOR SHARE`): its code, time zone and rates stay as
  the sale reads them, and an Owner's change waits for the sale. Then the
  menus, `FOR UPDATE` in the order of their IDs (`LockMenusForSale`), so two
  sales of the same menus never deadlock and the last of a menu is sold
  once. Then the voucher, when the order names one (`voucher.Redeem`,
  `LockVoucherByCode`, `FOR UPDATE`): two outlets after its last use, whose
  other locks are apart, wait for each other only there, and the second
  finds it used up. Then the outlet's dine-in mode (`table.HoldDineInMode`,
  an advisory lock of the outlet's mode, shared with its other sales): a
  change of it waits for the sale, which is saved under the mode it was
  checked against. Where the outlet seats its customers by number stands
  a dine-in order carries its stand's number, and where at tables none: a
  table or a dine-in order without a number in the one, a number in the
  other, is 409 `dine_in_mode_changed` with the mode (`details.mode`), and
  nothing is taken. Last the table, when the order names one
  (`table.HoldForSale`, `FOR SHARE OF t`): two sales at a table don't wait
  for each other, but deleting or changing the table, or releasing it,
  waits for the sale. A table deleted, turned off, scheduled off, or
  another outlet's is 409 `table_unavailable`, with its status
  (`inactive`, `scheduled`, `missing`); a takeaway with a table is a 422
  (`table_id: dine_in`). A stand's number is kept trimmed and in capitals;
  one with a takeaway, beside a table or off the pattern is a 422
  (`stand_number: dine_in`, `with_table`, `format`).
- **The server decides.** The order is charged with the figures on the
  cashier's screen, so it is taken only while they are right, and is
  otherwise refused naming what changed: `menu_unavailable`,
  `prices_changed` (each menu's price now), `order_stock_insufficient`
  (what each menu has, its lines together), a voucher the sale can't take
  (as the cashier's check refuses it, and `voucher_min_spend` with the
  minimum), `total_changed` (the server's total from `order.ComputeTotals`,
  the subtotal less the voucher's discount, the rule
  `api/test-vectors/order-totals.json` pins down for every client) and
  `payment_method_unavailable`. Then the order with its voucher's code and
  its table's name or its stand's number, its lines, its cash payment with
  the change, the sale movements (`menu.RecordSale`), the voucher's use,
  and `order.created` in the Log Action, with its device, its voucher, and
  its table or its stand's number.
- **A resend takes no second use.** The voucher's code is in the hash, in
  capitals, the table's ID too, and the stand's number as it is kept; each
  is left out when there is none, so the orders taken before vouchers,
  tables or stands keep their hashes.
- **Voids** need `order.void` and a reason. The order is locked `FOR
  UPDATE`; it and its payment become void, and what it sold goes back to
  the menus still tracked (`menu.RecordVoid`: a deleted menu, or one no
  longer tracked, keeps its count). Its voucher's use goes back too
  (`voucher.Release`, after the menus, as in a sale), the voucher turned off
  since or not. `order.voided` in the Log Action.
- **Today's orders** (`GET /outlets/{oid}/orders`) are the outlet's day, in
  its time zone, newest first, searchable by part of the receipt number.
- **The outlet's code locks** with the first order (409
  `outlet_code_locked`), since every receipt number carries it: `Update`
  holds the outlet `FOR UPDATE`, which waits for a sale holding it, then
  asks `order.Records` whether there is an order.
- **Tested for real.** The order module's tests run every module it uses,
  and its races are made to happen: a test holds the lock (a menu, the
  device, an order, the outlet, a voucher, a table) until every request
  waits for it in `pg_locks`, then lets them go. Two cashiers buying the
  last one sell it once; an order and its resend at once make one order;
  two voids at once put the stock back once; two outlets taking a voucher's
  last use take it once; deleting a table waits for the sale at it, and is
  then refused; releasing a table waits for a sale at it, and releases it;
  a change of the dine-in mode waits for the sale checked against it.
  Every test checks a menu's count against its history.

## Vouchers

- **The Owner's.** `GET/POST /vouchers`, `GET/PUT/DELETE /vouchers/{vid}`,
  `PATCH /vouchers/{vid}/status` and `GET /vouchers/{vid}/redemptions` are
  on the Owner's group. An outlet's Manager reads the ones its cashier can
  take (`GET /outlets/{oid}/vouchers`, `RequireManagerOrOwner`): those for
  every outlet and those for it among others, naming no other outlet. The
  Log Action keeps `voucher.created`, `.updated` (the fields that changed),
  `.activated`, `.deactivated` and `.deleted`, in the Owner's log, without
  an outlet.
- **A status at a moment.** `voucher_status(is_active, start_at,
  expired_at, used_count, quota, at)` in SQL, in this order: `inactive`,
  `expired` (at or past `expired_at`), `used_up`, `scheduled` (before
  `start_at`), `active`. Every query reads it at the service's clock, the
  list filters on it, and tests move that clock.
- **Once used, it stays.** Before a sale has used a voucher every field
  may change. From `first_used_at` on, its code and type are `locked`, its
  quota may only grow (`decrease`) and its expiry only move later
  (`earlier`); a new expiry is in the future either way (`future`). The
  update locks the voucher first, so a sale under way takes its use before
  the rules are checked, and every problem is reported at once. A used
  voucher can't be deleted (409 `voucher_used`): it is deactivated instead.
- **The discount.** `voucher.Terms.Discount`: a percentage of the subtotal
  rounded half up, held to its maximum; an amount as it is; either never
  past the subtotal; nothing short of the minimum spend.
  `api/test-vectors/voucher-discounts.json` pins it for the web and Android
  cashiers, which work it out again as the cart changes.
- **The cashier's check.** `POST /outlets/{oid}/vouchers/validate` needs
  `checkout.create` and one of the outlet's devices, as a sale does, so
  codes can't be tried from anywhere. It answers the voucher's terms and
  its discount on the subtotal sent; it refuses a voucher for another
  outlet first, then by its status. A subtotal short of the minimum spend
  isn't refused there (`applies: false`), since the cart may grow; the sale
  refuses it. Checking takes nothing.
- **Times are absolute.** The admin shows and takes them in WIB for every
  outlet; the API speaks RFC 3339, kept to the second.

## Tables

- **Areas and tables.** `GET/POST /outlets/{oid}/areas`, `PUT /areas/order`,
  `PUT/DELETE /areas/{aid}`, and `GET/POST /tables` (by area, status and
  search, a page at a time, in the cashier's order: by area, then by name
  as people read numbers, Meja 2 before Meja 10), `GET/PUT/DELETE
  /tables/{tid}`, `PATCH /tables/{tid}/status`. Reading needs `table.view`,
  which the Owner has; changing needs `table.manage`. A table moved to
  another area leaves the old area's layout. A table customers are seated
  at can't be deleted (409 `table_occupied`), nor an area holding tables
  (409 `area_not_empty`, with how many).
- **Layouts** (`GET/PUT /areas/{aid}/layout`) are saved whole, on the
  `version` the editor opened: a stale one is 409 `layout_changed`, with
  the version now, so an editor left open never overwrites someone else's
  change. Every problem with a layout is reported at once — a box off the
  grid or of the wrong size, a table on top of another, a table of another
  area — and tables left out of it are taken off it.
- **Locks.** A change to a table locks its area, by ID, then the table
  `FOR UPDATE`, the order a layout's save takes them in; a table that moved
  to another area while its area was locked makes the transaction start
  again, at most twice more, so it never holds a table while waiting for an
  area.
- **A table's status, read when asked** — no cron — in this order:
  `occupied`, a paid dine-in sale at it since it was last released and
  since the outlet's last opening or closing time in its zone (midnight
  there, for an outlet without hours), so a table empties at closing time
  on its own and a void of its last sale empties it too; `inactive`,
  turned off, which waits for an occupied table to be released; `scheduled`,
  a schedule not cancelled under way; `available`. The cashier can sell at
  an available or occupied table: an order more there is a sale of its own.
- **Releasing** (`POST /tables/{tid}/release`, `checkout.create`) writes
  `released_at` with `clock_timestamp()` under the table's lock, and
  `table.released` in the Log Action with since when and how many sales; a
  table nobody is seated at is left as it is.
- **Schedules.** `GET/POST /table-schedules` (by status, newest first),
  `GET/PUT/DELETE /table-schedules/{sid}`. A start, new or changed, isn't
  before the current minute, so a form filled to the minute can start
  "now"; the end is after the start and to come. Another schedule not over
  that has one of the same tables out at an overlapping time is 409
  `schedule_overlaps`, naming it, until the request says
  `confirm_overlap`: a warning, not a refusal. A schedule changes or is
  cancelled only while it isn't over (409 `schedule_closed`); cancelled, it
  stays as history and its tables are back. `table_schedule_status(...)`
  in SQL is its status: `cancelled`, `finished`, `ongoing`, `upcoming`.
- **The dine-in mode** (`GET/PUT /outlets/{oid}/dine-in-mode`): how the
  outlet seats its dine-in customers, `table` (an outlet that never chose)
  or `number`. Reading needs `table.view`, which the Owner has; changing
  needs `table.manage`. A change takes the mode's advisory lock alone
  (`LockDineInMode`), once the sales under way are done, and reads the mode
  after it: two changes at once change it and log it once, an outlet that
  never chose too, with no setting row to lock. Saving the mode already set
  changes and logs nothing. It holds from the next sale on: sales taken
  keep their table or their stand's number, and the areas, layouts and
  tables stay for when the outlet goes back to them.
- **The cashier's floor** (`GET /outlets/{oid}/pos/tables`,
  `checkout.create`): the areas that hold tables, in order, with their
  decorations and each table's status and since when it is occupied; none
  where the outlet seats its customers by number stands.
- **The Log Action** (the outlet's): `area.created`, `.updated`,
  `.reordered`, `.deleted`, `.layout_updated` (the tables that moved, by
  name, and how many decorations), `table.created`, `.updated`,
  `.activated`, `.deactivated`, `.deleted`, `.released`,
  `table_schedule.created`, `.updated`, `.cancelled`, and
  `dine_in_mode.updated` (with the mode before).

## Background jobs (outbox)

Work that follows a committed change but needn't finish before the response
goes through the `outbox_jobs` table:

1. A service calls `outbox.Enqueue(ctx, tx, kind, payload)` with the
   transaction that makes the change. It takes a `pgx.Tx`, so a job can't be
   enqueued outside one, and it exists only if the change commits.
   `EnqueueAt` schedules a job for later.
2. The worker claims a due job whenever one of its slots is free (4 by
   default), with `FOR UPDATE SKIP LOCKED`, so several workers never take the
   same job. It only claims kinds it has a handler for, so during a deploy an
   older worker leaves a new kind of job to the newer one.
3. A claim is a lease: the job stays `running` until `locked_until` — the job
   timeout (2 minutes) plus a minute to record the outcome. If the worker
   dies, the lease runs out and another worker claims the job again. Every
   claim counts as an attempt.
4. The handler's result decides what happens next. `nil` marks the job
   `done`. An error (a panic or a timeout counts as one) puts it back to
   `pending` with the error in `last_error`, to run again 30 seconds later,
   doubling up to an hour. After the 10th attempt it is parked as `failed`,
   about three hours after the first.
5. An outcome is only recorded if the attempt number still matches the
   claim, so a worker whose lease was taken over can't overwrite the newer
   run's result.
6. Done jobs are deleted after 7 days. Failed jobs stay until someone deals
   with them.

Rules for handlers:

- **Idempotent.** A job can run twice: a crash after the work but before the
  outcome is recorded, or a run that outlives its lease.
- **Kinds** are named `<module>.<action>`, declared as constants in the
  module that owns them; that module's handler is registered in
  `cmd/worker/main.go`.
- **Payloads carry IDs**, not copies of data that may change before the job
  runs. They are plain JSON in the database and kept for 7 days after the job
  is done, so a secret belongs in one only if it expires quickly, like a
  password reset token.
- **Shutdown** stops claiming and waits for running jobs, each within its own
  timeout. Give the worker's container a stop grace period of at least the
  job timeout, or an interrupted job runs again when its lease expires.

To run a failed job again by hand:

```sql
UPDATE outbox_jobs SET status = 'pending', attempts = 0, run_at = now()
WHERE outbox_job_id = '…';
```

## Testing

- `go test ./...` runs every test, integration tests included.
- Integration tests call `dbtest.New(t)`. It creates a schema of the test's
  own in `TEST_DATABASE_URL`, applies every migration to it, and drops it
  when the test ends. That schema is the connection's whole `search_path`,
  so a test sees neither other tests' rows nor the development data in
  `public`, and integration tests run in parallel.
- `TEST_DATABASE_URL` comes from the environment or from `.env`, where the
  example points it at the development database. Without it integration
  tests are skipped — unless `CI` is set, where they fail instead.
- A test run that is killed before its cleanup leaves its schema behind; the
  next run drops schemas more than an hour old.
- A test binary holds at most four schemas at once, and each test's pool at
  most ten connections, so the packages' tests running together stay under
  PostgreSQL's `max_connections`. Schemas are dropped table by table: one
  `DROP SCHEMA … CASCADE` takes a lock per object, and several at once ran
  the server out of shared memory.
- The race detector works on Windows with a MinGW gcc on `PATH`:
  `go test -race ./...`. Its binaries take a lot of memory; on a development
  machine with little to spare, `go test -race -p 3 ./...` runs three
  packages at a time instead of one per core.
