# Backup & recovery

otel-fleet keeps state in two stores with very different recovery value:

| Store | Holds | Recovery priority |
|---|---|---|
| **PostgreSQL** | customers, API keys (hashes), pipelines + versions, agents, users, SSO providers, webhooks, audit log | **Critical** — irreplaceable source of truth |
| **ClickHouse** | ingested logs/traces/metrics + per-tenant rollups | Secondary — TTL-bounded (30 days) and re-fillable by re-ingesting |
| VictoriaMetrics | collector self-telemetry | Disposable — rebuilds from scrapes |

Back up PostgreSQL always. Back up ClickHouse only if you must retain telemetry
across a cluster loss.

The Helm chart ships scheduled backups (disabled by default):

```yaml
backup:
  postgres:  { enabled: true }          # nightly pg_dump to a PVC, 14-day retention
  clickhouse: { enabled: true, envSecretName: ch-backup-creds }  # clickhouse-backup → S3
```

## PostgreSQL

### Backup
The `-pg-backup` CronJob runs `pg_dump --format=custom` to the backup PVC. To
take an ad-hoc dump (any environment with the database URL):

```sh
pg_dump --format=custom --file=otel-fleet-$(date -u +%Y%m%dT%H%M%SZ).dump "$OTEL_FLEET_DATABASE_URL"
```

The custom format is compressed and restores selectively with `pg_restore`.

### Restore
1. Scale the control plane to zero so nothing writes during the restore:
   ```sh
   kubectl scale deploy -l app.kubernetes.io/name=otel-fleet --replicas=0
   ```
2. Restore into an empty database (drops and recreates objects):
   ```sh
   pg_restore --clean --if-exists --no-owner --dbname "$OTEL_FLEET_DATABASE_URL" otel-fleet-<ts>.dump
   ```
3. Scale the control plane back up. It runs migrations at startup and is
   idempotent, so a dump taken at an older schema is migrated forward on boot.

> The `OTEL_FLEET_MASTER_KEY` is **not** in the database — it encrypts secrets
> stored there (SSO client secrets, pipeline credentials). Restore is useless
> without the same master key, so store it in your secret manager alongside the
> backups. Losing it means re-entering every stored secret.

## ClickHouse

Telemetry is TTL-bounded; most operators accept re-ingesting after a loss and
do **not** back ClickHouse up. If you need retention across a cluster loss, use
[`clickhouse-backup`](https://github.com/Altinity/clickhouse-backup) (the
`-ch-backup` CronJob):

```sh
# backup (full) to configured remote storage
clickhouse-backup create_remote otel-fleet-<ts>
# restore
clickhouse-backup restore_remote --rm otel-fleet-<ts>
```

The schema is owned by `deploy/clickhouse/schema/*.sql` and re-applied on a
fresh instance via the init scripts, so a bare-schema recovery (no data) needs
only a fresh ClickHouse with those scripts mounted.

## Disaster-recovery drill

Quarterly, verify the PostgreSQL path end to end: restore the latest dump into a
scratch database, point a throwaway control plane at it, and confirm customers,
pipelines and agents load. A backup you have never restored is not a backup.
