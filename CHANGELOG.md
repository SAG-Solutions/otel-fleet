# Changelog

## [0.10.1](https://github.com/SAG-Solutions/otel-fleet/compare/v0.10.0...v0.10.1) (2026-08-17)


### Miscellaneous Chores

* release 0.10.1 ([53638d9](https://github.com/SAG-Solutions/otel-fleet/commit/53638d9f329db21caf7eda9edeaea1a5eac06cf8))

## [0.10.0](https://github.com/SAG-Solutions/otel-fleet/compare/v0.9.0...v0.10.0) (2026-08-11)


### Features

* **regions:** multi-region Phase 1 — region model + registry ([4f24fa3](https://github.com/SAG-Solutions/otel-fleet/commit/4f24fa3d89320da820762c7bf7b9d479b8493518))
* **regions:** multi-region Phase 2 — region-aware read routing ([0b4acb8](https://github.com/SAG-Solutions/otel-fleet/commit/0b4acb8e7fcee6f07530b56009e890cb52843bc1))
* **regions:** Phase 2b — fan out fleet-wide reads across regions ([2405448](https://github.com/SAG-Solutions/otel-fleet/commit/2405448a5bba074c514e58ae5106bb5b23591e30))
* **regions:** region-aware cluster-wide PromQL alerting (fire per region) ([6cf3052](https://github.com/SAG-Solutions/otel-fleet/commit/6cf3052d5ce5ee353e3ec2c9b93a66487df4835a))

## [0.9.0](https://github.com/SAG-Solutions/otel-fleet/compare/v0.8.0...v0.9.0) (2026-08-10)


### Features

* **cluster-monitoring:** curated Grafana dashboards ([5b5b19c](https://github.com/SAG-Solutions/otel-fleet/commit/5b5b19c6ce528fcb37d6202ff67101d0f56ce1e4))
* **cluster-monitoring:** HA cluster tier via leader election + TA sharding ([7a991d1](https://github.com/SAG-Solutions/otel-fleet/commit/7a991d1090f0f3bb1300b81179d0a9f888a7321a))
* **cluster-monitoring:** recording rules via vmalert ([b3f2670](https://github.com/SAG-Solutions/otel-fleet/commit/b3f26703faa56b31acea1ff422ac608be83a4374))
* **cluster-monitoring:** Target Allocator for ServiceMonitor/PodMonitor ingest ([844447e](https://github.com/SAG-Solutions/otel-fleet/commit/844447e974b3ead9ddb8a989cbf053a87b9a0478))
* **webhooks:** PagerDuty and Opsgenie notification channels ([0b75baf](https://github.com/SAG-Solutions/otel-fleet/commit/0b75baf3f35a3e63d408540ffe896acf9f8106cd))


### Bug Fixes

* address pre-release code-review findings ([4e79b04](https://github.com/SAG-Solutions/otel-fleet/commit/4e79b04f62e19d9d0fc7f9c00a3d4588c866ff15))
* **cluster-e2e:** poll for k8s_cluster metrics in dashboard validation ([ba1dc55](https://github.com/SAG-Solutions/otel-fleet/commit/ba1dc556c0dd2754e47cc7c74daa6ba4977bba38))
* **cluster-e2e:** query series endpoint for dashboard validation ([c7a842a](https://github.com/SAG-Solutions/otel-fleet/commit/c7a842af0c00a7d2513e605d0f8e1fe5829897f3))
* **cluster-e2e:** robust dashboard-metric validation ([e0278ac](https://github.com/SAG-Solutions/otel-fleet/commit/e0278ac2008ed6e4060c3e4499bd5afc46851170))
* **cluster-monitoring:** Target Allocator RBAC for target discovery ([897a4d8](https://github.com/SAG-Solutions/otel-fleet/commit/897a4d8c08b4bfec697cbe79869e7cbc354ad1ab))

## [0.8.0](https://github.com/SAG-Solutions/otel-fleet/compare/v0.7.0...v0.8.0) (2026-07-31)


### Features

* **alerting:** maintenance windows (silence all firing while active) ([d460ccc](https://github.com/SAG-Solutions/otel-fleet/commit/d460ccc5eddcfb398da74f39eac4399c0f791740))
* **alerting:** maintenance windows UI + docs ([9dba6c5](https://github.com/SAG-Solutions/otel-fleet/commit/9dba6c5946c9ce374a9b7b1dd390a3a5a68d0680))
* **alerting:** per-rule severity (info/warning/critical) ([55afcf1](https://github.com/SAG-Solutions/otel-fleet/commit/55afcf10894a369775a658aa4a19daa5447c8375))
* **alerting:** severity select + badge in the alert rules UI ([fcd570b](https://github.com/SAG-Solutions/otel-fleet/commit/fcd570bd35b1a450482e0a7eccf12121e75ce380))
* **collector:** slim collector distro variant ([bed12fc](https://github.com/SAG-Solutions/otel-fleet/commit/bed12fc4e9fc922cf020115b73cb6c34d52a1698))
* **crypto:** zero-downtime master-key rotation ([747c82c](https://github.com/SAG-Solutions/otel-fleet/commit/747c82cbe7a5734149dcb07d2fbe5dd0cf15b758))
* **metrics:** per-customer Metrics tab (tenant-scoped PromQL) ([32240ba](https://github.com/SAG-Solutions/otel-fleet/commit/32240bacb778990ecb5f8bf4e1e7f1c5c92790b9))
* **metrics:** scoped per-customer PromQL query endpoint ([81a6fc7](https://github.com/SAG-Solutions/otel-fleet/commit/81a6fc76b916fd8491c3e1b4d2dd725c33b32e7a))
* **opamp:** HA OpAMP tier via advisory-lock leader election ([0a5f3ac](https://github.com/SAG-Solutions/otel-fleet/commit/0a5f3ac48063449d7a6452a1496fba7740892ead))

## [0.7.0](https://github.com/SAG-Solutions/otel-fleet/compare/v0.6.0...v0.7.0) (2026-07-30)


### Features

* **alerting:** PromQL alert rules evaluated against VictoriaMetrics ([43271a9](https://github.com/SAG-Solutions/otel-fleet/commit/43271a9f09be6eda3a1a76af29a24c87080eeae9))
* **alerting:** PromQL rule form + cluster-monitoring docs ([09b6e7b](https://github.com/SAG-Solutions/otel-fleet/commit/09b6e7b24ab8f4d2907000e97ff48adde4629532))
* **cluster-monitoring:** OTel-native kube-prometheus-stack alternative ([3f4acd5](https://github.com/SAG-Solutions/otel-fleet/commit/3f4acd5a25e695f62bdb4f76f1274c27613a6bcf))
* **metrics:** admin Infrastructure view (cluster metrics in the UI) ([d39279b](https://github.com/SAG-Solutions/otel-fleet/commit/d39279b1d9756c27b696a450646ceb6394ef92f0))
* **metrics:** admin PromQL query_range proxy to VictoriaMetrics ([a16e21b](https://github.com/SAG-Solutions/otel-fleet/commit/a16e21bf062ef13eac3ab9aec551eedeb72e7f86))
* **pipelines:** backend exporter presets + more exporters ([ddb8a22](https://github.com/SAG-Solutions/otel-fleet/commit/ddb8a2233aa53c28a5523434f7f9a02336c97578))
* **pipelines:** exporter preset gallery with backend icons ([efd7b77](https://github.com/SAG-Solutions/otel-fleet/commit/efd7b7701286b1bddfd19620016705295407e4e0))

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
