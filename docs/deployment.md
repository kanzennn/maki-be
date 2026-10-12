# Deployment

How maki-be runs in production, and how a push to `main` gets there. The
shape comes from the spec (Infra & deploy) and the workspace standard
(Go + Gin §2, §6, §9, §10, §12).

## The shape

One VPS. Its own Nginx, on the host, holds ports 80 and 443 and the
domain's certificate, and passes the domain on to Maki's Docker Compose
stack, `maki`, in `/srv/maki`. The database is the server's shared
PostgreSQL 18, outside the stack:

| Service | What | From |
| --- | --- | --- |
| Nginx, the host's | TLS for the domain, with its certificate; the domain passed on to Caddy | the server's own, with [its site](#nginxs-site) |
| `caddy` | The web app's files and headers, and the API's paths passed to the api: plain HTTP on `127.0.0.1:<MAKI_HTTP_PORT>`, the host's loopback only | `caddy:2-alpine`, [`deploy/Caddyfile`](../deploy/Caddyfile) |
| `api` | `api`, on `:8080` inside the stack only | the maki-be image |
| `worker` | `worker`: the outbox's jobs (emails) | the maki-be image |
| PostgreSQL 18, the server's | Shared with the server's other apps: Maki's database on the `db-net` network, reached by the api and the worker with the `DB_` variables of the shared setup's env file for Maki | the server's own setup |
| `pg` | `pg_dump` and `pg_restore` for the backups; in the `tools` profile, so only ever `compose run` | `postgres:18` |

The web app and the API share one domain, as maki-fe expects
(`VITE_API_ORIGIN` empty): `/api/*`, `/healthz` and `/readyz` go to the api,
`/assets/*` are the web app's hashed files (cached for good, a missing one a
404), and anything else is its files or, for a client-side route,
`index.html` (`no-cache`, so a deploy shows at once). maki-fe's deploy puts
the files in `/srv/maki/web` ([maki-fe's deployment.md](../../maki-fe/docs/deployment.md)).

[`deploy/compose.yaml`](../deploy/compose.yaml) is the stack;
[`deploy/deploy.sh`](../deploy/deploy.sh) deploys an image;
[`deploy/backup.sh`](../deploy/backup.sh) backs the database up.

## The image

[`Dockerfile`](../Dockerfile) builds one image with both binaries (standard
§2), static (`CGO_ENABLED=0`) on `gcr.io/distroless/static-debian12:nonroot`:
no shell, the CA certificates and the time zone database included, running
as `nonroot`. Its commands:

| Command | What |
| --- | --- |
| `api` (the default) | The HTTP server |
| `worker` | The outbox worker |
| `api migrate` | Applies the pending migrations, under goose's advisory lock |
| `api create-owner` | Creates the Owner, once: it prompts, without echo |

Once the worker processes images (menu photos, govips), govips needs cgo:
the worker then gets an image of its own, Debian slim with libvips (spec;
standard §9), and this one keeps the api.

The pipeline pushes it as `ghcr.io/<owner>/maki-be:<commit sha>` and
`:latest`, `<owner>` being the GitHub account that holds the repository,
in lowercase.

## The pipeline

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml), one pipeline for
the repository:

| Job | When | What |
| --- | --- | --- |
| `lint` | every push and pull request | `go vet`, golangci-lint (the Makefile's version), and `sqlc diff`: the store code is what sqlc makes of the queries |
| `test` | every push and pull request | `go test -count=1 ./...` (never a result from the cache setup-go restores) against a PostgreSQL 18 service, production's version, `TEST_DATABASE_URL` set; with `CI` set, the integration tests fail rather than skip without it (standard §12) |
| `image` | a push to `main`, both passing | builds the image, pushes it to GHCR |
| `deploy` | after `image`, when the repository variable `DEPLOY_ENABLED` is `true` | through the `production` environment: copies the stack's files to `/srv/maki`, logs the server into GHCR with the job's own token (through stdin, logged out after), and runs `deploy.sh` with the new image |

Until `DEPLOY_ENABLED` is `true` the deploy is skipped, and everything
before it still runs. A pull request's older runs stop when a new push
comes; `main`'s always run to the end, and one deploy waits for another.

## Setting it up, once

### The server

1. A VPS (Debian or Ubuntu) with Docker Engine and its Compose plugin;
   ports 22, 80 and 443 open, nothing else. A port Docker publishes skips
   the host's firewall, which is why Caddy's is on the loopback only.
2. The deploy user (`DEPLOY_USER`), in the `docker` group, signing in by key
   only, who can read the shared PostgreSQL's env file for Maki (5.): the
   user who owns the shared setup is the simplest.
3. `/srv/maki`, owned by that user.
4. The domain's DNS A (and AAAA) record pointing at the server.
5. **In the shared PostgreSQL 18:** a `maki` database owned by a `maki`
   role, so that `api migrate` can create its tables, and the setup's env
   file for Maki with `DB_HOST` (PostgreSQL's name on `db-net`), `DB_PORT`,
   `DB_DATABASE`, `DB_USERNAME` and `DB_PASSWORD`, and `DB_SSLMODE` if
   need be. Compose reads it as an env file: a value with a `$` in it goes
   in single quotes, or Compose takes the `$` for a variable.
6. **Nginx's site** for the domain ([below](#nginxs-site)), with the
   certificate the way the server's other sites get theirs (certbot, say).
7. `/srv/maki/.env`, from [`deploy/.env.example`](../deploy/.env.example):
   the domain, `MAKI_HTTP_PORT`, `MAKI_DB_ENV_FILE` (the path of the env
   file in 5.), a `JWT_SECRET` (`openssl rand -base64 48`), the SMTP
   provider. It stays on the server; nothing else holds it.
8. `/srv/maki/backup.env`, from
   [`deploy/backup.env.example`](../deploy/backup.env.example), and the
   daily cron line (below).

### Nginx's site

Nginx passes everything on the domain to Caddy, which serves the files and
the API and sets the headers (maki-fe's
[security.md](../../maki-fe/docs/security.md)). Nginx adds none of its own,
HSTS included, so that none is sent twice. With `MAKI_HTTP_PORT=8081` and
the certificate certbot made:

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name pos.example.com;
    return 301 https://$host$request_uri;
}

server {
    # On Nginx 1.25.1 or newer: `listen 443 ssl;` and `http2 on;`.
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name pos.example.com;

    ssl_certificate     /etc/letsencrypt/live/pos.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/pos.example.com/privkey.pem;
    server_tokens off;
    # Above the api's 1 MB, so that the api answers a body too big itself,
    # in its own error format.
    client_max_body_size 2m;

    location / {
        proxy_pass http://127.0.0.1:8081;
        # Required: the api takes a request whose Origin is its Host as
        # same-origin (security.md), and Caddy passes Host on as it gets it.
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Then `nginx -t` and `systemctl reload nginx`. The client's address reaches
the api in `X-Forwarded-For`: Caddy keeps what Nginx sends, and the api
takes it from private addresses only (`TRUSTED_PROXIES`, below).

### GitHub

The three repositories, `maki-be`, `maki-fe` and `maki-mobile`, under one
account. In `maki-be` and `maki-fe` alike:

| Setting | Where | What |
| --- | --- | --- |
| `production` | Settings → Environments | The environment the deploys run in. Required reviewers there make each deploy wait for an approval |
| `DEPLOY_HOST`, `DEPLOY_USER` | variables of `production` | The server, and the deploy user |
| `DEPLOY_SSH_KEY` | secrets of `production` | The private key of a key pair made for the pipelines (`ssh-keygen -t ed25519`), its public half in `deploy`'s `authorized_keys` |
| `DEPLOY_KNOWN_HOSTS` | secrets of `production` | The server's host key line, from `ssh-keyscan -t ed25519 <host>`, checked against the fingerprint the server shows |
| `DEPLOY_ENABLED` | Settings → Variables (repository) | `true` once the above is done |

In `maki-fe` and `maki-mobile`, when `maki-be` is private: the secret
`MAKI_BE_TOKEN`, a fine-grained token that may read `maki-be`'s contents,
for their pipelines' checkout of it. The image's package on GHCR is private
with a private repository; the deploy logs in with the job's own token.

### The first deploy

1. A push to `main` of `maki-be` (or **Run workflow**): the image, then
   `deploy.sh`, which creates the tables in the shared PostgreSQL's `maki`
   database.
2. On the server, the Owner, once:

   ```sh
   cd /srv/maki
   docker compose --env-file .env --env-file release.env run --rm api api create-owner
   ```

3. A push to `main` of `maki-fe`: its files, and the domain shows the app.

## A deploy, step by step

`sh deploy.sh <image>`, in `/srv/maki`:

1. pulls the image;
2. **migrations first**, from the new image, on the shared PostgreSQL, while
   the running api still serves: every migration is safe beside the binary
   before it (add a column nullable first, backfill, then tighten; standard
   §6), and goose's advisory lock keeps two deploys from racing. One that
   fails stops the deploy, the old version still up;
3. writes the image to `release.env`, which names the image running;
4. starts the new api and worker (and Caddy, if it isn't up): a few seconds
   in which the API doesn't answer;
5. checks the Caddyfile the deploy brought, and has the running Caddy read
   it again without dropping a connection (an invalid one fails the
   deploy, the old configuration still serving);
6. waits until the api answers `/readyz`, or fails the deploy after a
   minute with the api's last log lines.

**Rolling back** is the same command with an earlier image:
`sh deploy.sh ghcr.io/<owner>/maki-be:<earlier sha>`. Migrations only go
forward, and by step 2's rule the earlier binary runs on the newer schema.

The `docker compose` commands on the server always take both files:
`docker compose --env-file .env --env-file release.env …`.

## Environment

The api and the worker read their configuration from the environment
([architecture.md](./architecture.md#configuration)). In production it comes
from two places:

| Variable | Set by | What |
| --- | --- | --- |
| `APP_ENV` | compose.yaml | `production`: Gin in release mode, the strict checks below |
| `DB_HOST`, `DB_PORT`, `DB_DATABASE`, `DB_USERNAME`, `DB_PASSWORD` (`DB_SSLMODE`) | the shared PostgreSQL's env file for Maki | The database: the config makes its URL from them, each part escaped ([architecture.md](./architecture.md#configuration)) |
| `WEB_BASE_URL` | compose.yaml | `https://` + `MAKI_DOMAIN`: the links in emails |
| `TRUSTED_PROXIES` | compose.yaml | The private ranges: Caddy reaches the api over the stack's network, and the shared database's other clients could over `db-net`; nothing from the internet is in them |
| `HTTP_ADDR` | compose.yaml | `:8080`, the api's |
| `MAKI_DOMAIN` | .env | The domain the app and the API share |
| `MAKI_HTTP_PORT` | .env | Caddy's port on the host's loopback, where Nginx's site passes the domain |
| `MAKI_DB_ENV_FILE` | .env | The path of the shared PostgreSQL's env file for Maki |
| `MAKI_DB_NETWORK` | .env, optional | That PostgreSQL's Docker network, when it isn't `db-net` |
| `JWT_SECRET` | .env | At least 32 bytes, this environment's own |
| `LOG_LEVEL` | .env | `info` |
| `SMTP_*` | .env | The email provider; outside development the worker won't start without `SMTP_HOST` |
| `CORS_ALLOWED_ORIGINS` | .env | Empty: the web app is on the same domain |
| `CDN_ORIGIN`, `STORAGE_ORIGIN` | .env | The object storage's origins for the web app's CSP, once menu photos come |
| `MAKI_BE_IMAGE` | release.env, by deploy.sh | The image running |

## Backups and restore

`sh backup.sh` dumps the database in pg_dump's custom format to
`/srv/maki/backups`, with the stack's `pg` service (PostgreSQL 18's own
`pg_dump`, on `db-net`, with the `DB_` variables), uploads it to the backup
bucket through the AWS CLI's
container (`amazon/aws-cli`, pulled on its first run) with the access key in
`backup.env`, and deletes local dumps older than a week. The bucket's
lifecycle rule keeps them 14–30 days (spec). Daily, in `deploy`'s crontab:

```cron
15 2 * * * cd /srv/maki && sh backup.sh >> backups/backup.log 2>&1
```

**Restore**, into the running database (the api and the worker stopped
first):

```sh
cd /srv/maki
docker compose --env-file .env --env-file release.env stop api worker
docker compose --env-file .env --env-file release.env run --rm -T pg sh -c \
  'PGPASSWORD="$DB_PASSWORD" PGSSLMODE="${DB_SSLMODE:-prefer}" exec pg_restore -w -h "$DB_HOST" -p "${DB_PORT:-5432}" -U "$DB_USERNAME" -d "$DB_DATABASE" --clean --if-exists --no-owner' \
  < backups/<dump>
docker compose --env-file .env --env-file release.env start api worker
```

**Each month, a restore test** (spec), into a PostgreSQL 18 that exists for
the test only, so that the shared server isn't touched:

```sh
cd /srv/maki
docker run -d --name maki-restore-test -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18
# Over TCP: the image's first start answers on its socket before it's ready.
until docker exec maki-restore-test pg_isready -h 127.0.0.1 -q; do sleep 1; done
docker exec -i maki-restore-test pg_restore -U postgres -d postgres --no-owner --no-privileges < backups/<dump>
docker exec maki-restore-test psql -U postgres -c 'SELECT count(*) FROM orders'
docker rm -f maki-restore-test
```

## Logs and health

- `docker compose --env-file .env --env-file release.env logs -f api` (or
  `worker`, `caddy`): JSON lines, each request with its request ID.
- `https://<domain>/healthz` answers while the process is up, `/readyz`
  while the database answers too: what an uptime check watches.

## Before launch

- [ ] DNS points at the server, and `https://<domain>` has Nginx's
      certificate; `http://<domain>` redirects to it.
- [ ] The shared PostgreSQL's `maki` database is owned by the `maki` role,
      and the first deploy's migrations ran.
- [ ] `.env` filled in: a fresh `JWT_SECRET`, `MAKI_HTTP_PORT`,
      `MAKI_DB_ENV_FILE`, the SMTP provider; a password reset arrives by
      email.
- [ ] The Owner created (`api create-owner`), and signed in.
- [ ] The headers as maki-fe's [security.md](../../maki-fe/docs/security.md)
      lists them, each once, Nginx's version not among them:
      `curl -sI https://<domain>/`.
- [ ] The backup's cron line in place, one backup in the bucket, and a
      restore tested.
- [ ] The `production` environment's reviewers, if deploys should wait for
      an approval.
- [ ] The server: key-only SSH, the firewall, automatic security updates.

## Not covered yet

- The worker's own image, with libvips, once it processes images.
- Sentry and the uptime check (spec, Monitoring).
- More than one server, or a deploy without its few seconds of downtime.
- WebSockets through Nginx, once the outlets' realtime hub comes (spec):
  its site then passes `Upgrade` and `Connection` on.
