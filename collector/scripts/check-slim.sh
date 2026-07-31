#!/usr/bin/env bash
# Enforce the slim/full collector-distro invariant.
#
#   1. slim ⊆ full — every `gomod:` line in builder-config.slim.yaml must also
#      appear in builder-config.yaml, so slim can never reference a component
#      the full distro lacks.
#   2. no heavy exporters in slim — the five external-SaaS/cloud backend
#      exporters that motivate the split stay full-only.
#
# Run from the collector/ directory (make -C collector check-slim).
set -euo pipefail
cd "$(dirname "$0")/.."

FULL=builder-config.yaml
SLIM=builder-config.slim.yaml

# Heavy exporters that must never appear in the slim manifest.
HEAVY=(
  exporter/datadogexporter
  exporter/elasticsearchexporter
  exporter/kafkaexporter
  exporter/awss3exporter
  exporter/splunkhecexporter
)

fail=0

# 1. subset check
gomods() { grep -oE 'gomod:[[:space:]]*[^[:space:]]+' "$1" | awk '{print $2}' | sort -u; }
while IFS= read -r mod; do
  [ -z "$mod" ] && continue
  if ! gomods "$FULL" | grep -qxF "$mod"; then
    echo "ERROR: $SLIM references '$mod' which is missing from $FULL (slim ⊄ full)"
    fail=1
  fi
done < <(gomods "$SLIM")

# 2. no heavy exporter in slim
for h in "${HEAVY[@]}"; do
  if grep -q "$h " "$SLIM"; then
    echo "ERROR: heavy exporter '$h' must not appear in the slim manifest $SLIM"
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "check-slim: FAILED"
  exit 1
fi
echo "check-slim: OK (slim ⊆ full; no heavy exporters in slim)"
