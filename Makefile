SHELL := /bin/bash
COMPOSE := docker compose -f deploy/compose/docker-compose.yaml

.PHONY: dev-up dev-down run build cli test lint gen gen-go gen-web clean

dev-up:
	$(COMPOSE) up -d --build

dev-down:
	$(COMPOSE) down -v

run:
	go run ./cmd/otel-fleet

build:
	mkdir -p bin
	go build -o bin/otel-fleet ./cmd/otel-fleet

test:
	go test ./...
	cd collector/extension/tenantauth && go test ./...
	cd collector/processor/tenantstamp && go test ./...

lint:
	golangci-lint run ./...

gen: gen-go gen-web

gen-go:
	go tool oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	cd proto && go tool buf generate

gen-web:
	cd web && pnpm gen

clean:
	rm -rf bin collector/dist web/dist

# --- docs (MkDocs Material) ---------------------------------------------------
# Requires uv (https://docs.astral.sh/uv/); uvx runs mkdocs in an ephemeral env.
.PHONY: docs-serve docs-build

docs-serve:
	uvx --with mkdocs-material mkdocs serve

docs-build:
	uvx --with mkdocs-material mkdocs build --strict

cli:
	mkdir -p bin
	go build -o bin/otel-fleetctl ./cmd/otel-fleetctl
