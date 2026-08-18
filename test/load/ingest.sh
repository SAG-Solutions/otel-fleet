#!/usr/bin/env bash
# Ingest load test: mint a customer + API key via the control-plane API, blast
# OTLP through the gateway with telemetrygen for a fixed window, then read back
# the row count from ClickHouse to compute effective end-to-end throughput
# (ingest -> gateway auth/stamp/quota -> ClickHouse).
#
# Prereqs:
#   - the compose data plane is up:  docker compose -f deploy/compose/docker-compose.yaml up -d postgres clickhouse victoriametrics gateway
#   - the control plane runs on the host with OTEL_FLEET_DEV_LOGIN=true (see make run)
#   - telemetrygen: run via docker (default) or set TELEMETRYGEN=telemetrygen for a local binary
#
# Usage:
#   test/load/ingest.sh [signal] [duration_seconds] [workers]
#     signal            logs | traces | metrics   (default logs)
#     duration_seconds  how long to send           (default 30)
#     workers           telemetrygen workers       (default 8)
#
# This is a capacity-planning aid. Numbers from a dev laptop under Docker Desktop
# are indicative only — run against production-like hardware to size on.
set -euo pipefail

SIGNAL="${1:-logs}"
DURATION="${2:-30}"
WORKERS="${3:-8}"
BASE="${OTEL_FLEET_URL:-http://localhost:8080}"
EMAIL="${OTEL_FLEET_ADMIN_EMAIL:-js@sag-solutions.com}"
OTLP_HTTP="${OTEL_FLEET_OTLP_HTTP:-localhost:4318}"
CH_CONTAINER="${CH_CONTAINER:-otel-fleet-dev-clickhouse-1}"
TELEMETRYGEN="${TELEMETRYGEN:-}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1" >&2; exit 1; }; }
need curl; need jq

echo "== 1. authenticate + mint a customer/API key =="
JAR="$(mktemp)"; trap 'rm -f "$JAR"' EXIT
curl -sf -c "$JAR" -X POST "$BASE/api/v1/auth/dev-login" \
  -H 'Content-Type: application/json' -d "{\"email\":\"$EMAIL\"}" >/dev/null
CSRF="$(curl -sf -b "$JAR" "$BASE/api/v1/me" | jq -r .csrfToken)"
SLUG="load-$(date +%s)"
CREATE="$(curl -sf -b "$JAR" -X POST "$BASE/api/v1/customers" \
  -H 'Content-Type: application/json' -H "X-CSRF-Token: $CSRF" \
  -d "{\"name\":\"Load $SLUG\",\"slug\":\"$SLUG\"}")"
KEY="$(echo "$CREATE" | jq -r .initialApiKey.secret)"
CLIENT_ID="$(echo "$CREATE" | jq -r .customer.clientId)"
[ -n "$KEY" ] && [ "$KEY" != null ] || { echo "failed to mint API key: $CREATE" >&2; exit 1; }
echo "   customer=$CLIENT_ID slug=$SLUG"

echo "== 2. count rows before =="
table=otel_logs; [ "$SIGNAL" = traces ] && table=otel_traces; [ "$SIGNAL" = metrics ] && table=otel_metrics_sum
chq() { docker exec -i "$CH_CONTAINER" clickhouse-client --user otelfleet --password otelfleet -q "$1"; }
BEFORE="$(chq "SELECT count() FROM otel.$table WHERE TenantId = '$CLIENT_ID'")"

echo "== 3. send $SIGNAL for ${DURATION}s ($WORKERS workers) =="
gen() {
  if [ -n "$TELEMETRYGEN" ]; then
    "$TELEMETRYGEN" "$@"
  else
    docker run --rm --network host ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:latest "$@"
  fi
}
START=$(date +%s)
gen "$SIGNAL" \
  --otlp-endpoint "$OTLP_HTTP" --otlp-http --otlp-insecure \
  --otlp-header "Authorization=\"Bearer $KEY\"" \
  --duration "${DURATION}s" --workers "$WORKERS" --rate 0 || true
END=$(date +%s)

echo "== 4. wait for the batch/export + MV to settle =="
sleep 15

echo "== 5. count rows after =="
AFTER="$(chq "SELECT count() FROM otel.$table WHERE TenantId = '$CLIENT_ID'")"
INGESTED=$((AFTER - BEFORE))
ELAPSED=$((END - START)); [ "$ELAPSED" -gt 0 ] || ELAPSED=1
echo
echo "results:"
echo "  signal:        $SIGNAL  (table otel.$table)"
echo "  ingested rows: $INGESTED"
echo "  send window:   ${ELAPSED}s"
echo "  effective:     $((INGESTED / ELAPSED)) rows/s end-to-end (ingest -> ClickHouse)"
echo
echo "note: 'effective' is rows landed in ClickHouse over the send window — the"
echo "real end-to-end rate incl. auth, tenantstamp, quota, batching, and export."
