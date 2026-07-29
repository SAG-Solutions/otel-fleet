# Build targets for the otel-fleet collector distribution.
# (Kept separate from the repo-root Makefile on purpose.)

OCB_VERSION ?= v0.156.0
BUF_VERSION ?= v1.57.2
DIST        := dist/otel-fleet-collector
TOOLS_BIN   := $(CURDIR)/.tools/bin

.PHONY: all build test proto validate docker clean

all: test build

# Build the distribution with the OpenTelemetry Collector Builder.
build:
	go run go.opentelemetry.io/collector/cmd/builder@$(OCB_VERSION) --config builder-config.yaml

# Unit-test the custom components.
test:
	cd extension/tenantauth && go build ./... && go test -race ./...
	cd processor/tenantstamp && go build ./... && go test -race ./...
	cd processor/tenantquota && go build ./... && go test -race ./...

# Regenerate the tenantauth module's AuthService stubs from proto/authservice.proto.
proto:
	mkdir -p $(TOOLS_BIN)
	GOBIN=$(TOOLS_BIN) go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	GOBIN=$(TOOLS_BIN) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	cd extension/tenantauth && PATH="$(TOOLS_BIN):$$PATH" \
		go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION) generate ../../../proto --template buf.gen.yaml

# Validate the dev gateway config and a representative forwarding-tier config
# (testdata/forwarding-sample.yaml is the contract test for control-plane
# rendered configs) against the built binary.
validate:
	OTEL_FLEET_AUTH_ENDPOINT=localhost:9443 \
	CLICKHOUSE_ENDPOINT='tcp://localhost:9000?database=otel' \
	./$(DIST) validate --config=../deploy/compose/otelcol-gateway.yaml
	./$(DIST) validate --config=testdata/forwarding-sample.yaml

# Build the gateway container image (repo root as context).
docker:
	docker build -f ../Dockerfile.collector -t otel-fleet-collector:dev ..

clean:
	rm -rf dist .tools
