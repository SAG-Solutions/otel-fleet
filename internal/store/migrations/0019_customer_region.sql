-- +goose Up

-- Data-residency region a customer is pinned to (multi-region Phase 1). No CHECK
-- constraint: the valid regions are runtime configuration (OTEL_FLEET_REGIONS),
-- not a fixed set — the control plane validates the value at write time. Existing
-- rows (single-region deployments) backfill to 'default', matching the
-- synthesized default region when no registry is configured.
ALTER TABLE customers ADD COLUMN region TEXT NOT NULL DEFAULT 'default';

-- +goose Down
ALTER TABLE customers DROP COLUMN region;
