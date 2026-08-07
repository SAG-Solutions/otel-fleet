#!/usr/bin/env bash
# Target Allocator e2e: prove that a prometheus-operator ServiceMonitor is
# discovered by the standalone Target Allocator, sharded to the cluster
# collector, scraped, and remote-written to VictoriaMetrics — the kube-
# prometheus-stack migration path (#59) verified end to end.
#
# Prereqs (same as run.sh): a kind cluster via $KCTX with the collector image
# ($COLLECTOR_IMAGE:$COLLECTOR_TAG) already loaded.
set -euo pipefail

CHART="${CHART:-deploy/charts/otel-fleet}"
NS="${NS:-otel-fleet-ta}"
KCTX="${KCTX:-kind-otelfleet-e2e}"
COLLECTOR_IMAGE="${COLLECTOR_IMAGE:-otel-fleet-collector}"
COLLECTOR_TAG="${COLLECTOR_TAG:-e2e}"
VM_IMAGE="${VM_IMAGE:-victoriametrics/victoria-metrics:v1.103.0}"
# ServiceMonitor/PodMonitor CRDs (installable standalone, no full operator).
PO_VERSION="${PO_VERSION:-v0.76.0}"
CRD_BASE="https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/${PO_VERSION}/example/prometheus-operator-crd"
SAMPLE_IMAGE="${SAMPLE_IMAGE:-quay.io/brancz/prometheus-example-app:v0.5.0}"
K="kubectl --context $KCTX"

echo "==> install ServiceMonitor + PodMonitor CRDs ($PO_VERSION)"
$K apply --server-side -f "${CRD_BASE}/monitoring.coreos.com_servicemonitors.yaml"
$K apply --server-side -f "${CRD_BASE}/monitoring.coreos.com_podmonitors.yaml"

echo "==> namespace $NS"
$K create namespace "$NS" --dry-run=client -o yaml | $K apply -f -

echo "==> VictoriaMetrics + sample app + ServiceMonitor"
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
spec: { selector: { app: victoriametrics }, ports: [{ port: 8428, targetPort: 8428 }] }
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: sample-app, labels: { app: sample-app } }
spec:
  replicas: 1
  selector: { matchLabels: { app: sample-app } }
  template:
    metadata: { labels: { app: sample-app } }
    spec:
      containers:
        - name: app
          image: $SAMPLE_IMAGE
          ports: [{ name: metrics, containerPort: 8080 }]
---
apiVersion: v1
kind: Service
metadata: { name: sample-app, labels: { app: sample-app } }
spec:
  selector: { app: sample-app }
  ports: [{ name: metrics, port: 8080, targetPort: metrics }]
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata: { name: sample-app, labels: { app: sample-app } }
spec:
  selector: { matchLabels: { app: sample-app } }
  endpoints:
    - { port: metrics, interval: 15s }
YAML
$K -n "$NS" rollout status deploy/victoriametrics --timeout=120s
$K -n "$NS" rollout status deploy/sample-app --timeout=120s

echo "==> cluster-monitoring bundle WITH the Target Allocator, HA (2 replicas)"
helm template otel-fleet "$CHART" -n "$NS" \
  --set external.databaseUrl=postgres://unused:unused@pg:5432/db \
  --set clusterMonitoring.enabled=true \
  --set clusterMonitoring.targetAllocator.enabled=true \
  --set clusterMonitoring.cluster.replicas=2 \
  --set images.collector.repository="$COLLECTOR_IMAGE" \
  --set images.collector.tag="$COLLECTOR_TAG" \
  --set images.collector.pullPolicy=IfNotPresent \
  --set external.victoriaMetrics.remoteWriteUrl=http://victoriametrics.$NS.svc:8428/api/v1/write \
  -s templates/cluster-monitoring/rbac.yaml \
  -s templates/cluster-monitoring/target-allocator.yaml \
  -s templates/cluster-monitoring/cluster-deployment.yaml \
  | $K apply -n "$NS" -f -

echo "==> wait for the Target Allocator and both cluster collector replicas"
$K -n "$NS" rollout status deploy/otel-fleet-target-allocator --timeout=150s
$K -n "$NS" rollout status deploy/otel-fleet-cluster --timeout=180s

echo "==> HA: leader election lease is held by exactly one of the two replicas"
holder=""
for i in $(seq 1 20); do
  holder="$($K -n "$NS" get lease otel-fleet-cluster-monitoring -o jsonpath='{.spec.holderIdentity}' 2>/dev/null || true)"
  [ -n "$holder" ] && break
  sleep 3
done
replicas="$($K -n "$NS" get pods -l app.kubernetes.io/component=cluster-monitoring-cluster --no-headers 2>/dev/null | wc -l | tr -d ' ')"
echo "  cluster replicas running: $replicas ; lease holder: ${holder:-<none>}"
if [ "$replicas" != "2" ] || [ -z "$holder" ]; then
  echo "FAIL: expected 2 cluster replicas and one leader-election lease holder"
  $K -n "$NS" get pods -l app.kubernetes.io/component=cluster-monitoring-cluster || true
  $K -n "$NS" get lease otel-fleet-cluster-monitoring -o yaml || true
  exit 1
fi

echo "==> generate a little traffic so the sample counter has a value"
$K -n "$NS" port-forward deploy/sample-app 18080:8080 >/tmp/app-pf.log 2>&1 &
APF=$!
sleep 3
for i in $(seq 1 5); do curl -s http://localhost:18080/ >/dev/null || true; done
kill $APF 2>/dev/null || true

echo "==> query VictoriaMetrics for the ServiceMonitor-discovered metric"
$K -n "$NS" port-forward svc/victoriametrics 18428:8428 >/tmp/vm-pf.log 2>&1 &
PF=$!
trap 'kill $PF 2>/dev/null || true' EXIT
sleep 3

found=0
for i in $(seq 1 40); do
  # http_requests_total is emitted only by the sample app; its presence proves
  # ServiceMonitor -> Target Allocator -> collector -> remote-write end to end.
  n="$(curl -s 'http://localhost:18428/api/v1/query?query=count(http_requests_total)' || true)"
  echo "  attempt $i: $n"
  if printf '%s' "$n" | grep -q '"result":\[{'; then found=1; break; fi
  sleep 5
done

if [ "$found" -ne 1 ]; then
  echo "FAIL: the sample app's http_requests_total never reached VictoriaMetrics via the Target Allocator"
  echo "--- target allocator logs ---"; $K -n "$NS" logs deploy/otel-fleet-target-allocator --tail=50 || true
  echo "--- cluster collector logs ---"; $K -n "$NS" logs deploy/otel-fleet-cluster --tail=50 || true
  exit 1
fi
echo "PASS: ServiceMonitor target scraped via the Target Allocator and stored in VictoriaMetrics"

# The leader still emits k8s_cluster metrics under HA (not silenced by election).
kcount=0
for i in $(seq 1 20); do
  kcount="$(curl -s 'http://localhost:18428/api/v1/label/__name__/values' | grep -o '"k8s_[^"]*"' | sort -u | wc -l | tr -d ' ')"
  [ "$kcount" -gt 0 ] && break
  sleep 5
done
if [ "$kcount" -eq 0 ]; then
  echo "FAIL: no k8s_* metrics in VictoriaMetrics — the leader replica is not emitting cluster metrics"
  exit 1
fi
echo "PASS: HA cluster tier — leader emits $kcount k8s_* metrics while the Target Allocator shards scrapes across both replicas"
