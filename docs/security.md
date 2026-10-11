# Security

How maki-be applies §7 (authentication and authorization) and §11 (security
baseline) of the [Go + Gin standard](../../../docs/gin-architecture-standard.md),
as the code does it today. The sign-in flows live in `internal/auth`,
permissions and the rules for managing accounts in `internal/user`; the
building blocks in `internal/platform/authn`, `password`, `ratelimit` and
`middleware`.

## Accounts

- Everyone who signs in is a row in `users`, with a lowercase username
  (`^[a-z0-9][a-z0-9._-]{2,31}$`) that is never reused, even after a delete.
- There is exactly one **Owner**, created on the server with
  `api create-owner`. There is no sign-up page anyone could reach first. The
  database refuses a second Owner (`users_single_owner_key`), even from two
  runs at once, and refuses to deactivate or delete the Owner. The Owner has
  an email and no outlet; everyone else belongs to one outlet.
- Creating the Owner emails a link to confirm the address, since password
  reset only goes to a verified address.
- Managers and staff are created by someone else, with a temporary password
  ([below](#a-password-someone-else-set)). What each may do is in
  [Permissions](#permissions).

## Passwords

- **argon2id** (`golang.org/x/crypto/argon2`): 64 MiB, 3 passes, 2 lanes,
  a 16-byte salt and a 32-byte key. That's RFC 9106's second option, with 2
  lanes instead of 4 for a small server. Each hash is a PHC string that
  carries its parameters. After a successful sign-in with older parameters,
  the hash is replaced.
- At most 4 hashes run at once (`password.NewHasher(…, 4)`), so a burst of
  sign-ins waits its turn instead of taking 64 MiB each. A stored hash asking
  for more than 1 GiB or 20 passes is refused, not computed.
- A new password is 8 to 128 characters, any characters, and not the
  username (NIST SP 800-63B). Nothing else is required. There's no check
  against breached-password lists yet.
- An unknown username gets the same answer as a wrong password (401
  `invalid_credentials`) after the same work (`Hasher.Waste`). Neither the
  response nor its timing tells which usernames exist. A deactivated account
  is revealed (403 `account_disabled`) only to someone who typed its
  password.
- The API never returns a password hash to anyone.

## Sessions

A session is a family of refresh tokens: signing in starts one, and
`refresh_tokens.family_id` names it.

| Token | Form | Lifetime | Kept by the server |
| --- | --- | --- | --- |
| Access | HS256 JWT: `sub` (user), `sid` (session), `iss=maki`, `aud=maki-api`, `iat`, `exp` | 15 minutes | Not at all: only the signing key |
| Refresh | 256 random bits, base64url | 30 days from issue. Each refresh issues a new one, so a session used once a month never expires | SHA-256 of it, never the token |

- **Every request loads the principal.** `middleware.Authenticate` verifies
  the token: HS256 only, and issuer, audience, issue time and expiry all
  checked. Then it asks the database whether the user is active and the
  session still has a current token. A revoked session or a deactivated
  account stops working at once, not when the access token expires. A
  database failure there is a 500, so a hiccup never signs anyone out.
- **Rotation.** Each refresh replaces the family's current token. Presenting
  a token that was already replaced means a copy exists, so the whole family
  is revoked (`auth.session_revoked`, reason `refresh_token_reused`). The
  exception is within 30 seconds of the replacement: two browser tabs
  refreshing at once both send the old cookie, and the second gets 409
  `refresh_superseded` instead.
- **Delivery.**
  - **Web app:** the refresh token is a cookie, `maki_refresh`: `HttpOnly`,
    `SameSite=Strict`, `Path=/api/v1/auth` (refresh and sign-out need it),
    `Max-Age` of 30 days, and `Secure` everywhere but development over plain
    HTTP. A refresh that arrives with the cookie answers with a new cookie,
    never in the body, so no page script can obtain the token.
  - **Android app:** it signs in with `"client": "mobile"`, receives the
    refresh token in the body and sends it back in the body.
- **CSRF.** The refresh cookie is the only cookie. It is `SameSite=Strict`
  and only the auth endpoints read it. Everything else authenticates with
  the `Authorization` header, which another site's page can't set.
- **Sign-out** revokes the family named by the refresh token (body or
  cookie), or else by the access token's `sid`, and clears the cookie.
  Signing out twice isn't an error.
- **Changing the password** keeps the current session and ends every other.
  **Resetting it** ends them all.
- `JWT_SECRET` must be at least 32 bytes. Changing it only makes clients
  refresh early: refresh tokens don't depend on it.

The clients' side:

- **Web:** the access token lives in memory only. A 401 triggers one
  refresh, shared by every request that is waiting and, through Web Locks,
  by every tab. The request is then retried once. See
  [maki-fe's architecture](../../maki-fe/docs/architecture.md).
- **Android:** the refresh token is in `flutter_secure_storage` (Android
  Keystore) and the access token in memory. Backups and device transfers
  are off. See
  [maki-mobile's architecture](../../maki-mobile/docs/architecture.md).

## Permissions

RBAC with permissions, as the spec lays out: two fixed roles and custom roles
per outlet. The user module owns it (`internal/user`); the checks are
middleware in `internal/platform/middleware/access.go`.

**The catalog** (`internal/user/permissions.go`, `GET /permissions`) is 14
codes in 8 groups, from `dashboard.view` to `log.view`. Codes are stable:
roles store them and routes name them. A code a role holds but the catalog
no longer has is ignored.

**Who holds what:**

| Account | Permissions | Where |
| --- | --- | --- |
| Owner (`is_owner`) | Fixed in code: `dashboard.view`, `role.view`, `user.view`, `user.manage`, `menu.view`, `table.view`, `log.view`. The Owner looks into every outlet and manages its Managers, but doesn't run one: no checkout, no changes to menus, tables or payments | Every outlet |
| Manager (`is_manager`) | Fixed in code: every code in the catalog | Their own outlet |
| Staff | Their one custom role's codes (`user_roles` → `role_permissions`) | Their own outlet |

The Owner and Manager sets live in code, so nobody can change or delete
those roles. Custom roles are rows in `roles`, one outlet's each; the outlet
starts with "Kasir" (`checkout.create`), named in the outlet's language.

**Loaded on every request.** `Authenticate` builds the principal from the
database, not from the access token: the session is alive (auth), the
account is active and has these permissions (user), and its outlet is
active (outlet). So a changed role applies from the user's next request,
with the same token. Deactivating an outlet ends its users' access at
once: their next request gets 401, their refresh ends the session
(`auth.session_revoked`, reason `outlet_inactive`), and signing in answers
403 `outlet_inactive`, but only after the right password. The Owner is not
affected.

**The checks, in order:**

1. `Authenticate`, then `RequirePasswordChanged` (below).
2. The scope: `RequireOwner` for the outlet endpoints, or `OutletScope` for
   `/outlets/:oid/…`. The Owner may act in any outlet that exists,
   deactivated ones included; everyone else only in their own. Anything else
   is 404 `outlet_not_found`, the same answer for an outlet that doesn't
   exist and one that isn't theirs, so nobody learns which outlets exist.
3. The permission: `RequirePermission(code)`, or `RequireAnyPermission` for
   a route that more than one permission opens. Refusal is 403 `forbidden`.

Each fails closed: without a principal it answers 401. A database error while
checking the scope is a 500, not a 404.

| Route | Needs |
| --- | --- |
| `GET /permissions` | A session |
| `/outlets`, `/outlets/{oid}`, `/outlets/{oid}/status` | The Owner |
| `/vouchers`, `/vouchers/{vid}`, `…/status`, `…/redemptions` | The Owner |
| `GET /outlets/{oid}/vouchers` | The outlet's Manager or the Owner (`RequireManagerOrOwner`): the vouchers its cashier can take, naming no other outlet |
| `POST /outlets/{oid}/vouchers/validate` | `checkout.create`, from one of the outlet's devices in use, as a sale: codes can't be tried from anywhere |
| `GET /outlets/{oid}/roles` | `role.view` or `user.view`: whoever sees the outlet's users filters them by role and, managing them, gives one |
| `GET /outlets/{oid}/roles/{rid}` | `role.view` |
| `POST`, `PUT`, `DELETE` roles | `role.manage` |
| `GET /outlets/{oid}/users`, `GET …/users/{uid}` | `user.view` |
| Creating, changing, deactivating, resetting and deleting users | `user.manage` |
| `GET` categories and menus, `GET …/menus/{mid}/stock-movements` | `menu.view` |
| Creating, changing, reordering, activating, deactivating and deleting categories and menus | `menu.manage`, which covers a menu's stock mode, starting count and alert |
| `POST …/menus/{mid}/stock-adjustments` | `stock.adjust` |
| `PUT …/menus/{mid}/stock-alert` | `stock.adjust` or `menu.manage`: the alert is a field of the menu form too |
| `POST /outlets/{oid}/devices` | The outlet's Manager, on the device itself (`RequireManager`): not a permission, and not the Owner, who runs no till |
| `GET …/devices`, `DELETE …/devices/{did}` | The outlet's Manager or the Owner (`RequireManagerOrOwner`) |
| `GET …/pos/catalog` | `checkout.create` |
| `GET …/pos/device`, `POST …/orders` | `checkout.create`, from one of the outlet's devices in use ([below](#cashier-devices)) |
| `GET …/orders`, `GET …/orders/{orid}` | `checkout.create` or `order.void` |
| `POST …/orders/{orid}/void` | `order.void` |

**No escalation.** Nobody grants what they don't hold:

- A new role holds only codes its creator holds. Changing a role, only the
  codes it *adds* must be held: those it had may stay or go. Otherwise 403
  `permission_not_held`, with the codes in `details.permissions`.
- A role is given only by someone who holds all of its codes. The exception
  is the Owner making a Manager staff again: a Manager held every code, so
  any role takes some away.
- Staff are managed (changed, deactivated, reset, deleted) only by someone
  who holds all of the staff member's codes. Without this, a supervisor could
  reset the password of someone stronger and sign in as them.
- Nobody manages their own account through these endpoints (403
  `own_account`): nobody changes their own role or status, or deletes
  themselves. Their password, email and language have `/me`.
- Manager accounts are the Owner's: others can't create, change or delete
  one (403 `manager_account`). The Owner manages Manager accounts and, of a
  staff account, only promotes it (403 `owner_manages_managers`).
- A role being given, changed or deleted is locked (`FOR UPDATE`), so it
  can't be deleted halfway through being given. A role still held can't be
  deleted: 409 `role_in_use` lists who holds it.

**Sessions end at once** — in the same transaction, through
`auth.Sessions.EndAll` — when an account is deactivated, deleted, promoted
or demoted, or its password reset by someone else. A changed role doesn't
end them: the new permissions apply from the next request.

**Deleting** is soft. A deleted user's username and a deleted outlet's code
are never used again (409 `username_taken`, `outlet_code_taken`). An outlet
can only be deleted once it is inactive, with its name typed again as
`confirm_name`: trimmed, but the same letters and case.

The web and Android apps show only what a user may do, from `GET /me`'s
`permissions`; the server decides regardless. The Android app is for whoever
may check out (`checkout.create`).

## Cashier devices

A sale needs a user and a device: `checkout.create`, and a request from one
of the outlet's devices in use. A cashier's account that leaks can't sell
from anywhere else, and a revoked till stops at once.

- **Activation is the Manager's, on the device.** Signed in on it, the
  outlet's Manager names it and activates it; a cashier can't register a
  device of their own. The device's code (`K01`, `K02`, …) is taken under a
  per-outlet advisory lock, and never used again: receipt numbers carry it.
  A request whose device is the outlet's already, and in use, is 409
  `device_already_active`.
- **The token** is 32 random bytes (base64url), known to the device alone.
  The database keeps its SHA-256 (`token_hash`), as it does of refresh
  tokens: a leaked table holds no usable token.
  - A browser gets it as the `maki_device` cookie: `HttpOnly`, so no page
    script reads it; `SameSite=Strict`; `Secure` outside development; path
    `/api/v1`; 400 days, the longest a browser keeps one, sent again each
    time the cashier opens (`GET …/pos/device`), so a till in use keeps it.
  - The Android app gets it in the body, once, for its secure storage, and
    sends it as `X-Device-Token`. `authn.DeviceToken` reads the header
    first, then the cookie.
- **A token alone allows nothing.** Every cashier endpoint needs the
  access token too, which a browser sends in the `Authorization` header,
  never a cookie, so another site can't make a till's browser sell (no
  CSRF). Without a device token, with an unknown one or with another
  outlet's, the answer is 403 `device_not_registered`; a revoked device's is
  403 `device_revoked`.
- **Revoking** is the Manager's or the Owner's, at once: a sale holds its
  device's row (`FOR UPDATE`) until it commits, so a revocation waits for
  the sale on its way, and the next one is refused. A resend of an order
  taken before still gets its order back: the device made it, and knows it.
  The Android app, told `device_revoked`, deletes its device token and ends
  the session on it there and then
  ([maki-mobile's security.md](../../maki-mobile/docs/security.md#cashier-device)).
- **What the server checks of an order**, whatever the device says:
  - the receipt number's prefix is this device's and this outlet's, and its
    day is the outlet's day of `client_created_at`;
  - `client_created_at` is at most 5 minutes ahead of the server's clock, or
    a day behind;
  - every price is the menu's now, every menu on sale, the stock enough, and
    the total the server's own, from the outlet's rates now and the
    voucher's terms now. The device's figures are only ever compared, never
    stored on trust;
  - a voucher is checked again and its use taken under its lock, so its
    quota holds however many outlets sell at once;
  - the payment method is on; cash covers the total, and the change is the
    server's.
- **Transactions are kept as made.** Triggers refuse deleting an order, a
  line or a payment, changing a line, and changing an order or payment in
  any way but paid → void (SQLSTATE 42501), whoever holds the database
  credentials of the api.

## A password someone else set

A Manager's or staff member's first password is typed by whoever creates the
account, and so is a new one when a Manager or the Owner resets it. Both set
`users.must_change_password`, and a reset ends the user's sessions. While
the flag is set, `middleware.RequirePasswordChanged` answers 403
`password_change_required` on every route except `GET /me` and
`PUT /me/password`. Those two are the `authed` group in `cmd/api/router.go`;
everything else registers on `protected`. The web and Android apps take such
a user straight to the change-password screen. The middleware fails closed:
on a route wired without `Authenticate` it answers 401.

## Password reset and email changes

- **Who can reset by email:** the Owner only, by username or verified
  address. Staff ask a Manager or the Owner; the web app's forgot-password
  page and the Android sign-in screen say so. `POST /auth/password/forgot`
  answers 202 whether or not the account exists, so it confirms nothing.
  Only its per-IP limit changes the answer, to 429.
- **Link tokens** are 256 random bits, stored as SHA-256, and single-use.
  The worker creates them while it sends the email, so the plain token
  exists only in the email: never in the database, the outbox or a
  production log. They carry the token in the URL fragment
  (`/reset-password#token=…`), which browsers never send to a server, so it
  stays out of server and proxy logs. The web app removes it from the
  address bar once read.
  - A reset link lasts 30 minutes.
  - A verification link lasts 24 hours, and a newer one replaces any older
    one.
- **A reset** checks the new password, ends every session, and emails a
  "your password was changed" notice. A password change sends the same
  notice. The notice only goes to a verified address.
- **Changing the email** asks for the current password. The new address gets
  a confirmation link, and the old verified address gets a notice, since
  that's the one someone who took over the session doesn't control. The old
  address stays the account's email until the new one is confirmed.
- **Verifying the address** happens on a button press on the web page, not
  when the link is opened, so a mail scanner that follows links can't use
  the token up.

## Rate limits

`ratelimit.Limiter`, a sliding window per key:

| Endpoint | Key | Limit | What counts |
| --- | --- | --- | --- |
| Sign-in | IP | 20 per 15 min | Failed attempts |
| Sign-in | Username | 5 per 15 min | Failed attempts; a success clears them |
| Forgot password | IP | 5 per hour | Every request |
| Forgot password | Account | 3 emails per hour | Past it, requests are skipped silently |
| Reset password, verify email | IP | 10 per hour | Invalid tokens |
| Change password, change email | Account | 5 per 15 min | Wrong current passwords |
| Change email | Account | 5 per hour | Accepted requests |

A blocked request gets 429 `rate_limited`, with `Retry-After` and
`details.retry_after` in seconds. The counts live in memory: the API runs as
one process, and a restart clears them. A second API instance needs a shared
store first, or each instance allows its own share.

## Audit (the Log Action)

`action_logs` is append-only: triggers refuse `UPDATE`, `DELETE` and
`TRUNCATE` (SQLSTATE 42501). An entry is written in the same transaction as
the change it records, with the outlet it happened in. The auth module
records:

- `auth.owner_created`, `auth.login`, `auth.logout`
- `auth.login_failed`, with the reason for a known account. For an unknown
  username only the IP is recorded, since people type their password into
  the username field by mistake.
- `auth.session_revoked`, with the reason: `refresh_token_reused`,
  `account_disabled` or `outlet_inactive`
- `auth.password_changed`, `auth.password_reset_requested`,
  `auth.password_reset`
- `auth.email_change_requested` (old and new address), `auth.email_verified`

The outlet and user modules record every change, an update with only the
fields that changed (before and after):

- `outlet.created`, `outlet.updated`, `outlet.activated`,
  `outlet.deactivated`, `outlet.deleted`
- `role.created`, `role.updated`, `role.deleted`
- `user.created`, `user.updated`, `user.role_changed`, `user.promoted`,
  `user.demoted`, `user.activated`, `user.deactivated`,
  `user.password_reset`, `user.deleted`

The menu module records every change the same way, and every stock
adjustment:

- `category.created`, `category.updated`, `category.activated`,
  `category.deactivated`, `category.reordered` (the order before and after),
  `category.deleted`
- `menu.created` (with its starting count), `menu.updated` (the price and the
  alert included, whichever form changed them), `menu.activated`,
  `menu.deactivated`, `menu.deleted`
- `stock.adjusted`: the count before and after, the type, the reason and the
  note

The device and order modules record:

- `device.activated` (its code, name and platform), `device.revoked`
- `order.created`: the receipt number, type, total and payment method, with
  the device it came from in `device_id`. A resend records nothing more.
- `order.voided`: the receipt number and total, and the reason

There, a change of nothing records nothing, except a stock take: one that
finds the count as it was is still an entry, the proof it was counted. A
password never appears in an entry.

## HTTP baseline as shipped

| Area | In maki-be |
| --- | --- |
| TLS | Planned at Caddy, with the API on a private port. There's no deployment yet (no Caddyfile or Dockerfile), so nothing terminates TLS today |
| Trusted proxies | `TRUSTED_PROXIES`; empty trusts none, so `ClientIP()` (and the rate limits keyed on it) can't be spoofed |
| CORS | Exact origins from `CORS_ALLOWED_ORIGINS`, with credentials. A request whose `Origin` host equals its `Host` is same-origin and skips the list, so the web app served from the API's own host works (Caddy and the Vite proxy pass `Host` through) |
| Body size | 1 MB (`http.MaxBytesReader`), 413 past it |
| Response headers | `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`. A 401 for a missing or bad access token carries `WWW-Authenticate: Bearer realm="maki"` |
| Server timeouts | Read header 5 s, read 15 s, write 30 s, idle 60 s |
| SQL | sqlc-generated queries only |
| Errors | One envelope with a stable `code`; anything unexpected is a generic 500, with the details in the log |
| Logs | One line per request: the route pattern (never the raw path), status, latency, client IP, request ID and user ID. Never a body, password or token. The development log mailer prints emails, links included, and is refused outside development |

## Not built yet

- Offline sign-in and sync on the till, which change what a device's token
  may send: orders made offline, against the stock it last knew.
- Two-factor authentication (Next Dev in the spec).
- A breached-password check.
- A shared rate-limit store, needed before a second API instance.
- The deployment: Caddy with TLS in front of the API, the Docker images, and
  `TRUSTED_PROXIES` set to Caddy's address.

Re-audit when any of these lands, and when a payment integration, a webhook
receiver or a new kind of upload does (standard §11).

## Checks done

Checked on 2026-09-29, 2026-09-30 and 2026-10-01:

- **Integration tests:** 35 in `internal/auth`, against PostgreSQL. They
  cover every flow above, including refresh reuse inside and outside the
  grace period, rate limits, and sessions ending on password change, reset
  and deactivation. They pass three times in a row under `-race`.
- **Lint:** `golangci-lint`, including gosec, reports 0 issues.
- **The cashier** (2026-10-01): 30 integration tests in `internal/device`
  and `internal/order`, among them a cashier from another outlet, a revoked
  device, a cashier trying to activate one, another outlet's order under the
  same ID, and the races made to happen (the last one sold twice, an order
  and its resend, two voids). 56 mutations of their rules, each caught by a
  failing test. A curl script against the real binary, and Playwright
  against the web cashier, from activation to revocation.
- **By hand with curl**, the real api and worker binaries on a throwaway
  schema: every endpoint, including links taken from the worker's log.
- **The web app** in Edge with Playwright against the same binaries. This
  found two real bugs, both fixed: CORS refused same-origin requests, and the
  Vite proxy rewrote `Host`.
- **The Android app** on an emulator against the same binaries. The API
  restarted with a new `JWT_SECRET` showed the app's 401 → refresh → retry.
  Also checked: a restart keeps the session, and sign-out ends it on the
  server.

For permissions and outlets, on 2026-09-30:

- **Integration tests** against PostgreSQL:
  - an access matrix for the middleware (Owner, Manager, staff with and
    without a permission, another outlet, an unknown one, a database error);
  - 6 outlet tests, 7 role tests and 9 user tests, including every
    no-escalation rule above;
  - `migrations/schema_test.go` breaking each new constraint.
  
  They pass three times in a row under `-race`.
- **Mutations** — each rule removed in turn, and a test failing for it:
  - the outlet check in the principal and at sign-in;
  - the escalation checks;
  - deleting a role in use;
  - one's own account;
  - the Owner managing staff;
  - deactivating without ending the sessions;
  - listing roles with `role.view` alone.
- **By hand with curl** against the real binary on a throwaway schema, every
  step checking its answer:
  1. The Owner opens two outlets and gives one a Manager.
  2. The Manager changes the temporary password, then creates a role and a
     cashier.
  3. The cashier may only check out, and a new role applies from the next
     request.
  4. Deactivating the cashier, and then the outlet, ends their access.
- **The web app** with Playwright against the real binaries, the same story
  through the screens, and **the Android app** on an emulator:
  1. A Manager signs in.
  2. Staff without `checkout.create`, and the Owner, are told the app isn't
     theirs.
  3. A new role reaches the app with "Check again".

For the menu and stock, on 2026-09-30 and 2026-10-01:

- **Integration tests** against PostgreSQL:
  - 7 category, 5 menu and 6 stock tests, with an access matrix for each
    part: the Owner only looks, a storekeeper adjusts but doesn't edit the
    menu, a chef the reverse, a cashier sees none of it, and another outlet
    is 404;
  - one that adjusts a menu from 8 requests at once while the test holds the
    menu's lock, so a missing lock can't pass by luck ([architecture.md](./architecture.md#stock));
  - `migrations/schema_test.go` breaking each new constraint.
  
  They pass three times in a row under `-race`.
- **Mutations** — 33 rules broken in turn, each caught by a test, among them
  adjusting without the menu's lock, taking more than there is, an icon that
  isn't an emoji, every route's permission, and stamping movements when
  their transaction began.
- **By hand with curl** against the real binary on a throwaway schema:
  categories with emoji and their order, menus with and without stock, a
  storekeeper's adjustments and alert, and twenty adjustments at once from
  two people, none of them lost.
- **The web app** with Playwright against the real binaries: the Manager
  sets up categories and menus and takes stock down to low; the Owner looks
  it over and can't change it.
