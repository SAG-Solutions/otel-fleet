#!/usr/bin/env bash
# Cluster-monitoring e2e: deploy the otel-fleet cluster-monitoring bundle onto a
# real Kubernetes cluster (kind), point it at an in-cluster VictoriaMetrics, and
# assert that the node DaemonSet (hostmetrics + kubeletstats) and the singleton
# Deployment (k8s_cluster + prometheus) actually scrape and remote-write real
# cluster metrics — the kube-prometheus-stack replacement, verified end to end
# rather than helm-template-only.
#
# Prereqs (provided by the caller / CI): a running cluster reachable via the
# given kube context, `kubectl` and `helm`, and the collector image already
# loaded into the cluster (e.g. `kind load docker-image`).
#
#   COLLECTOR_IMAGE=otel-fleet-collector COLLECTOR_TAG=e2e \
#   KCTX=kind-otelfleet-e2e test/e2e-cluster/run.sh
set -euo pipefail

CHART="${CHART:-deploy/charts/otel-fleet}"
NS="${NS:-otel-fleet}"
KCTX="${KCTX:-kind-otelfleet-e2e}"
COLLECTOR_IMAGE="${COLLECTOR_IMAGE:-otel-fleet-collector}"
COLLECTOR_TAG="${COLLECTOR_TAG:-e2e}"
VM_IMAGE="${VM_IMAGE:-victoriametrics/victoria-metrics:v1.103.0}"
K="kubectl --context $KCTX"

echo "==> namespace $NS"
$K create namespace "$NS" --dry-run=client -o yaml | $K apply -f -

echo "==> VictoriaMetrics (in-cluster remote-write target)"
$K apply -n "$NS" -f - <<YAML
apiVersion: apps/v1
kind: Deployment
metadata: { name: victoriametrics, labels: { app: victoriametrics } }
spec:
  replicas: 1
  selector: { matchLabels: { app: victoriametrics } }
  template:
    metadata: { labels: { app: victoriametrics } }
    spec:
      containers:
        - name: vm
          image: $VM_IMAGE
          args: ["--retentionPeriod=1", "--selfScrapeInterval=0s"]
          ports: [{ containerPort: 8428 }]
          readinessProbe: { httpGet: { path: /health, port: 8428 }, initialDelaySeconds: 2 }
---
apiVersion: v1
kind: Service
metadata: { name: victoriametrics }
spec:
  selector: { app: victoriametrics }
  ports: [{ port: 8428, targetPort: 8428 }]
YAML
$K -n "$NS" rollout status deploy/victoriametrics --timeout=120s

echo "==> cluster-monitoring bundle (rbac + daemonset + cluster deployment)"
helm template otel-fleet "$CHART" -n "$NS" \
  --set external.databaseUrl=postgres://unused:unused@pg:5432/db \
  --set clusterMonitoring.enabled=true \
  --set images.collector.repository="$COLLECTOR_IMAGE" \
  --set images.collector.tag="$COLLECTOR_TAG" \
  --set images.collector.pullPolicy=IfNotPresent \
  --set external.victoriaMetrics.remoteWriteUrl=http://victoriametrics.$NS.svc:8428/api/v1/write \
  -s templates/cluster-monitoring/rbac.yaml \
  -s templates/cluster-monitoring/node-daemonset.yaml \
  -s templates/cluster-monitoring/cluster-deployment.yaml \
  | $K apply -n "$NS" -f -

echo "==> wait for the collectors to become ready"
$K -n "$NS" rollout status daemonset/otel-fleet-cluster-node --timeout=150s
$K -n "$NS" rollout status deploy/otel-fleet-cluster --timeout=150s

echo "==> query VictoriaMetrics for scraped metrics"
$K -n "$NS" port-forward svc/victoriametrics 18428:8428 >/tmp/vm-pf.log 2>&1 &
PF=$!
trap 'kill $PF 2>/dev/null || true' EXIT
sleep 3

kcount=0; scount=0
for i in $(seq 1 40); do
  names="$(curl -s 'http://localhost:18428/api/v1/label/__name__/values' || true)"
  kcount="$(printf '%s' "$names" | grep -o '"k8s_[^"]*"' | sort -u | wc -l | tr -d ' ')"
  scount="$(printf '%s' "$names" | grep -o '"system_[^"]*"' | sort -u | wc -l | tr -d ' ')"
  echo "  attempt $i: k8s_* metrics=$kcount  system_* metrics=$scount"
  if [ "$kcount" -gt 0 ] && [ "$scount" -gt 0 ]; then break; fi
  sleep 5
done

echo "==> sample metric names in VictoriaMetrics:"
printf '%s' "$names" | grep -oE '"(k8s_pod[^"]*|k8s_node[^"]*|system_cpu[^"]*|system_memory[^"]*)"' | sort -u | head -12 || true

if [ "$kcount" -eq 0 ] || [ "$scount" -eq 0 ]; then
  echo "FAIL: expected both k8s_* (k8s_cluster/kubeletstats) and system_* (hostmetrics) metrics in VictoriaMetrics"
  echo "--- cluster collector logs ---";  $K -n "$NS" logs deploy/otel-fleet-cluster --tail=40 || true
  echo "--- node collector logs ---";     $K -n "$NS" logs daemonset/otel-fleet-cluster-node --tail=40 || true
  exit 1
fi
echo "PASS: cluster-monitoring scraped $kcount k8s_* + $scount system_* metric series into VictoriaMetrics"

# Every metric referenced by the shipped Grafana dashboards must actually exist
# in VictoriaMetrics — this is what guarantees the curated dashboards are not
# built against stale/guessed names. Metric tokens end in a word that is not
# "name" (k8s_*_name tokens are labels, e.g. k8s_namespace_name, not metrics).
echo "==> validating shipped dashboards reference real metrics"
dash_metrics="$(grep -hE '"expr"' "$CHART"/dashboards/*.json | grep -ohE '(k8s_|system_|container_)[a-z0-9_]+' | grep -vE '_name$' | sort -u)"
# Poll: the k8s_cluster receiver emits a bit later than kubeletstats/hostmetrics
# (leader-election lease + informer sync + collection interval), so retry until
# every dashboard metric has appeared, up to ~150s.
missing_list=""
for attempt in $(seq 1 30); do
  missing_list=""
  for m in $dash_metrics; do
    n="$(curl -s "http://localhost:18428/api/v1/series?match[]=$m" \
         | python3 -c 'import sys,json;print(len(json.load(sys.stdin).get("data",[])))' 2>/dev/null || echo 0)"
    [ "$n" = "0" ] && missing_list="$missing_list $m"
  done
  [ -z "$missing_list" ] && break
  echo "  attempt $attempt: still missing:$missing_list"
  sleep 5
done
if [ -n "$missing_list" ]; then
  echo "FAIL: dashboard metric(s) absent from VictoriaMetrics — dashboards would render empty panels:$missing_list"
  echo "--- cluster collector logs ---"; $K -n "$NS" logs deploy/otel-fleet-cluster --tail=40 || true
  exit 1
fi
echo "PASS: all dashboard-referenced metrics exist in VictoriaMetrics"
