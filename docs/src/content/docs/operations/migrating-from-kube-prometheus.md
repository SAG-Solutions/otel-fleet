---
title: "Migrating from kube-prometheus-stack"
description: "Replace Prometheus + node-exporter + kube-state-metrics + Alertmanager with the OpenTelemetry-native cluster-monitoring bundle, and move your rules and dashboards across."
---

otel-fleet's cluster-monitoring bundle is an **OpenTelemetry-native** replacement
for the kube-prometheus-stack (kps). It collects the same cluster/node/pod
signals with OTel receivers, stores them in VictoriaMetrics, and keeps your
existing Prometheus-operator CRDs working — but it is a **re-platform, not a
byte-for-byte drop-in**. The one thing you actively change is the **metric-name
model**: a complete OTel setup uses OpenTelemetry semantic-convention names, not
Prometheus/kube-state-metrics names. This guide covers the whole move.

## What replaces what

| kube-prometheus-stack | otel-fleet |
| --- | --- |
| node-exporter | `hostmetrics` receiver (per-node DaemonSet) |
| cAdvisor / kubelet | `kubeletstats` receiver (per-node DaemonSet) |
| kube-state-metrics | `k8s_cluster` receiver (leader-elected singleton) |
| Prometheus scrape of annotated/CRD targets | `prometheus` receiver + **Target Allocator** (ingests your `ServiceMonitor`/`PodMonitor` CRDs and `prometheus.io/scrape` annotations) |
| Prometheus TSDB | VictoriaMetrics (remote-write) |
| PrometheusRule recording rules | vmalert (`recordingRules`) + your `additionalRuleConfigMaps` |
| PrometheusRule alerting + Alertmanager | otel-fleet native PromQL alert rules → channels, **or** keep Alertmanager via `recordingRules.notifierUrl` (see below) |
| Grafana (bundled) | bring your own Grafana → point it at VictoriaMetrics; the bundle ships curated dashboards as sidecar ConfigMaps |

Turn it on:

```yaml
clusterMonitoring:
  enabled: true
  targetAllocator: { enabled: true }   # ingest your existing ServiceMonitors/PodMonitors
  recordingRules: { enabled: true }
  dashboards: { enabled: true }
```

## The metric-name change (the one thing you must do)

The receivers emit **OTel semantic-convention** names, so your Prometheus-era
dashboards and alerts must be re-pointed. Representative mapping (names shown as
they land in VictoriaMetrics, i.e. dots → underscores):

| Prometheus / kube-state-metrics | otel-fleet (OTel) | Note |
| --- | --- | --- |
| `node_cpu_seconds_total` (counter, per-mode) | `k8s_node_cpu_usage` (gauge, cores) | shape differs — use the gauge directly, no `rate()` |
| `node_memory_MemAvailable_bytes` | `k8s_node_memory_usage_bytes` / `system_memory_usage` | |
| `node_filesystem_avail_bytes` | `k8s_node_filesystem_available` / `system_filesystem_usage` | |
| `container_cpu_usage_seconds_total` | `k8s_pod_cpu_usage`, `k8s_container_cpu_usage` (gauge) | |
| `container_memory_working_set_bytes` | `k8s_pod_memory_usage_bytes` | |
| `kube_pod_status_phase` | `k8s_pod_phase` | |
| `kube_node_status_condition{condition="Ready"}` | `k8s_node_condition_ready` | |
| `kube_deployment_status_replicas_available` | `k8s_deployment_available` | |

Because OTel models several node metrics as **gauges** where node-exporter uses
**counters**, there is no faithful `rate()`-preserving rename — this is why
otel-fleet does not ship a Prometheus-name compatibility shim. Confirm exact
names for your version with `count by (__name__)({__name__=~"k8s_.+"})` against
VictoriaMetrics, and see the
[k8sclusterreceiver](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/k8sclusterreceiver)
and [kubeletstatsreceiver](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/kubeletstatsreceiver)
docs for the full metric lists. The bundled **Nodes** and **Workloads**
dashboards are already keyed to these names — start from them.

## Moving your rules across

A `PrometheusRule` CRD's `.spec` is **Prometheus rule format**, which vmalert
reads natively. Copy the `.spec.groups` into a ConfigMap and reference it:

```sh
kubectl -n otel-fleet create configmap my-kube-rules \
  --from-file=kube-rules.yaml=<(kubectl get prometheusrule my-rule -o jsonpath='{.spec}' | yq -P)
```

```yaml
clusterMonitoring:
  recordingRules:
    enabled: true
    additionalRuleConfigMaps: [my-kube-rules]   # *.yaml keys are all loaded
```

Update the `expr:`s to the OTel metric names above. **Recording** rules write
their series straight back to VictoriaMetrics.

### Starter alert pack

The bundle ships a small **OTel-native starter alert pack**
(`files/cluster-alerts.yaml`) — collection-health `absent()` alerts, a
namespace-lost-all-pods alert, and a tunable cluster-CPU alert, all built on the
recording-rule rollups so they're safe on any cluster. Turn it on (it evaluates
in the same vmalert, so it routes via `notifierUrl`):

```yaml
clusterMonitoring:
  recordingRules:
    enabled: true
    clusterAlerts: { enabled: true }
    notifierUrl: http://alertmanager.monitoring.svc:9093   # or recreate natively
```

It's a starter set, not the full kubernetes-mixin — extend it with your own
rules via `additionalRuleConfigMaps`.

### Keeping Alertmanager (optional bridge)

If you'd rather keep your existing Alertmanager and `PrometheusRule` *alerts*
during the transition, point vmalert at it:

```yaml
clusterMonitoring:
  recordingRules:
    enabled: true
    additionalRuleConfigMaps: [my-kube-alerts]
    notifierUrl: http://alertmanager.monitoring.svc:9093
```

vmalert then evaluates the alerting rules and routes firing alerts to
Alertmanager. Leave `notifierUrl` empty to keep alerting **native** in otel-fleet
instead — recreate the alerts as PromQL rules under **Settings → Alert rules**
(fired to Slack / PagerDuty / Opsgenie / webhook, with severities and
maintenance windows). See [Alerting](/otel-fleet/guides/alerting/).

## Scraping control-plane components

The Target Allocator ingests **any** `ServiceMonitor`/`PodMonitor` in the
cluster, so scraping apiserver / etcd / scheduler / controller-manager / CoreDNS
is a matter of applying (or keeping) their ServiceMonitors — the same objects
kps used. kubelet/cAdvisor is already covered by the `kubeletstats` DaemonSet, so
you do **not** need a kubelet ServiceMonitor. Apply the ServiceMonitors for the
components you want, then confirm targets appear in the Target Allocator.

## Grafana

Bring your own Grafana and add VictoriaMetrics as a Prometheus datasource
(`http://<victoriametrics>:8428`). With a dashboard sidecar, the bundle's
dashboards import automatically (`dashboards.enabled: true`). Re-point any of
your own dashboards to the OTel metric names above.

## What this does *not* replace (yet)

Set expectations honestly:

- **A batteries-included alert/dashboard corpus** equal to the full
  kubernetes-mixin. The bundle ships a curated starter set — the **Kubernetes
  Cluster / Nodes / Workloads** dashboards and the starter alert pack above — not
  the entire mixin; bring the rest via `additionalRuleConfigMaps` and your own
  dashboards.
- **Alertmanager routing/grouping/inhibition** inside otel-fleet's native
  alerting — it has channels, severities, and maintenance windows. Keep
  Alertmanager via `notifierUrl` if you rely on routing trees or inhibition.
- **Long-term storage / HA storage** (Thanos/Cortex-style). Scale or cluster
  VictoriaMetrics per your retention needs.

Everything else — collection, storage, scrape-CRD ingestion, recording rules,
native alerting, dashboards — is covered.
