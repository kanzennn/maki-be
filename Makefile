# Commands shared by every Go backend in the workspace (architecture standard
# §15). Tools run through `go run tool@version`, so their versions are pinned
# here without adding them to go.mod. sqlc runs in Docker. goose is also a
# library in go.mod (the api binary applies the migrations); its CLI is only
# used to create migration files, so keep its version in step with go.mod.

-include .env
export

GOOSE := go run github.com/pressly/goose/v3/cmd/goose@v3.28.0
AIR   := go run github.com/air-verse/air@v1.67.4
LINT  := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
SQLC  := docker run --rm -v "$(CURDIR):/src" -w /src/sqlc sqlc/sqlc:1.31.1

.PHONY: dev worker build test lint generate migrate migration

dev: ## Run the API with live reload
	$(AIR)

worker: ## Run the worker
	go run ./cmd/worker

build: ## Build both binaries into bin/
	go build -o bin/ ./cmd/...

test: ## Run every test
	go test ./...

lint: ## go vet and golangci-lint
	go vet ./...
	$(LINT) run

generate: ## Generate the sqlc packages (needs Docker running)
	$(SQLC) generate

migrate: ## Apply pending migrations to DATABASE_URL, as a deploy does
	go run ./cmd/api migrate

migration: ## Create a migration: make migration name=create_outlets
	@test -n "$(name)" || (echo "usage: make migration name=<name>" && exit 1)
	$(GOOSE) -dir migrations create $(name) sql
