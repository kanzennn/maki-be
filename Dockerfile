# syntax=docker/dockerfile:1

# Two images from one module (architecture standard §2, §9). The api, with its
# migrate and create-owner subcommands, builds static (CGO_ENABLED=0) on a
# distroless base. The worker converts photos with libvips through govips,
# which needs cgo, so it gets a Debian slim base with libvips.
#
#   docker build -t maki-be .                             # the api, the last stage
#   docker build --target worker -t maki-be-worker .
#   docker run --rm --env-file .env maki-be               # the api
#   docker run --rm --env-file .env maki-be api migrate
#   docker run --rm --env-file .env maki-be-worker        # the worker

FROM golang:1.27-bookworm AS base
WORKDIR /src
# The modules first: a change to the code alone doesn't download them again.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .

FROM base AS build-worker
RUN apt-get update && apt-get install -y --no-install-recommends libvips-dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -tags vips -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM debian:bookworm-slim AS worker
RUN apt-get update && apt-get install -y --no-install-recommends libvips42 ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 65532 --no-create-home nonroot
COPY --from=build-worker /out/worker /usr/local/bin/worker
USER nonroot:nonroot
CMD ["worker"]

FROM base AS build-api
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot AS api
COPY --from=build-api /out/api /usr/local/bin/api
USER nonroot:nonroot
EXPOSE 8080
CMD ["api"]
