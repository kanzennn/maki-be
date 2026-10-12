#!/bin/sh
# Deploys a maki-be image on the VPS: pulls it, applies its migrations to the
# server's shared PostgreSQL while the running api still serves, starts the
# api and the worker from it, and waits until the api is ready. Run from
# /srv/maki, beside compose.yaml and .env; maki-be's deploy job calls it over
# SSH, and rolling back is the same command with the previous image.
#
#   sh deploy.sh ghcr.io/<owner>/maki-be:<sha>
set -eu
cd "$(dirname "$0")"

image="${1:?usage: sh deploy.sh <image>}"
if [ ! -f .env ]; then
  echo "deploy.sh: .env is missing: copy .env.example to .env and fill it in" >&2
  exit 1
fi
# release.env names the image running; the first deploy has none yet.
[ -f release.env ] || : > release.env

compose() { docker compose --env-file .env --env-file release.env "$@"; }

docker pull "$image"

# Migrations first, from the new image (Go + Gin standard §6): each is safe
# beside the api still running, and goose's advisory lock keeps two deploys
# from racing. A failed migration stops here, the old version still up.
# Until they pass, the new image is named here only: release.env keeps the
# running one (and has none yet on the first deploy).
MAKI_BE_IMAGE="$image" compose run --rm --no-deps api api migrate

printf 'MAKI_BE_IMAGE=%s\n' "$image" > release.env
compose up -d --remove-orphans api worker caddy
# The Caddyfile this deploy brought: checked, then read again by the running
# Caddy without dropping a connection. A Caddy that just started read it
# already, and takes a moment before it answers a reload.
compose exec -T caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
tries=0
until compose exec -T caddy caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile; do
  tries=$((tries + 1))
  [ "$tries" -lt 10 ] || exit 1
  sleep 1
done

tries=0
until compose exec -T caddy wget -q -O /dev/null http://api:8080/readyz; do
  tries=$((tries + 1))
  if [ "$tries" -ge 30 ]; then
    echo "deploy.sh: the api isn't ready after a minute" >&2
    compose logs --tail 50 api >&2
    exit 1
  fi
  sleep 2
done
echo "deploy.sh: $image is up"
