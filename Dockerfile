# syntax=docker/dockerfile:1

# One image, two binaries (architecture standard §2): the api, with its
# migrate and create-owner subcommands, and the worker. Both build static
# (CGO_ENABLED=0), so a distroless base, with its CA certificates and time
# zone database, is all they need. Once the worker processes images, govips
# needs cgo: the worker then gets an image of its own, Debian slim with
# libvips (§9), and this one keeps the api.
#
#   docker build -t maki-be .
#   docker run --rm --env-file .env maki-be            # the api
#   docker run --rm --env-file .env maki-be worker     # the worker
#   docker run --rm --env-file .env maki-be api migrate

FROM golang:1.27-bookworm AS build
WORKDIR /src
# The modules first: a change to the code alone doesn't download them again.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/api ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /out/worker /usr/local/bin/
USER nonroot:nonroot
EXPOSE 8080
CMD ["api"]
