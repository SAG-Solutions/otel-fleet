# Changelog

## [0.6.0](https://github.com/SAG-Solutions/otel-fleet/compare/v0.5.2...v0.6.0) (2026-07-29)


### ⚠ BREAKING CHANGES

* the OTELFLEET_* environment variables are now OTEL_FLEET_*; Prometheus metric names are now otel_fleet_*; image and chart names changed. Existing deployments must update their configuration.

### Features

* **alerting:** metric-threshold alert rules with periodic evaluation ([e6646bf](https://github.com/SAG-Solutions/otel-fleet/commit/e6646bf311fc726a662f2611182f2029d4c9c807))
* **helm:** hardened securityContext + PodDisruptionBudgets ([95fd2c6](https://github.com/SAG-Solutions/otel-fleet/commit/95fd2c6fb2f9240afff88c9720562d34138f0ad6))
* **helm:** opt-in control-plane NetworkPolicy ([34e2956](https://github.com/SAG-Solutions/otel-fleet/commit/34e2956668ea079a841e41438692498b8b2770fe))
* **portal:** tenant self-service portal for scoped users ([ef28d75](https://github.com/SAG-Solutions/otel-fleet/commit/ef28d755a5b94762a2e835c631d8cdd37d77b397))
* **security:** per-IP rate limiting + HTTP request hardening ([1006d8d](https://github.com/SAG-Solutions/otel-fleet/commit/1006d8dcc6be4d3a3e12dc81bc3478a5a59141ee))
* **security:** structured audit + metric for denied requests ([10f7985](https://github.com/SAG-Solutions/otel-fleet/commit/10f79853a18f67495a40fcfc0dcb370daeecd288))


### Bug Fixes

* regenerate collector proto stubs + clickhouse healthcheck creds ([14ea4c1](https://github.com/SAG-Solutions/otel-fleet/commit/14ea4c1f9e4ae48f33aec2fae381a7e857801061))


### Code Refactoring

* rename otelfleet -&gt; otel-fleet, org -&gt; sag-solutions ([b0388ea](https://github.com/SAG-Solutions/otel-fleet/commit/b0388eab6e79dd6c83ee515da2cc7338f1ccfa4c))
