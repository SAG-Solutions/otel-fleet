# otel-fleet control plane: Go backend + web UI + bundled collector binary
# (the collector is required for `otelcol validate` during pipeline editing).

# --- web build ---
FROM node:24-alpine AS web
RUN corepack enable
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY api /src/api
COPY web .
RUN pnpm build

# --- collector build (OCB; same as Dockerfile.collector) ---
FROM golang:1.26 AS collector
WORKDIR /src
COPY proto/ proto/
COPY collector/ collector/
RUN cd collector && CGO_ENABLED=0 go run go.opentelemetry.io/collector/cmd/builder@v0.156.0 --config builder-config.yaml

# --- backend build ---
FROM golang:1.26 AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/otel-fleet ./cmd/otel-fleet

# --- runtime ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /out/otel-fleet /usr/local/bin/otel-fleet
COPY --from=collector /src/collector/dist/otel-fleet-collector /usr/local/bin/otel-fleet-collector
COPY --from=web /src/web/dist /srv/otel-fleet/web
ENV OTEL_FLEET_WEB_DIR=/srv/otel-fleet/web \
    OTEL_FLEET_OTELCOL_BIN=/usr/local/bin/otel-fleet-collector
EXPOSE 8080 9443 9090 4320
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/otel-fleet"]
