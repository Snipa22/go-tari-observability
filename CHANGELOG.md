# Changelog

## 1.0.0 (2026-08-18)


### ⚠ BREAKING CHANGES

* cmd/tari-exporter and cmd/tari-netstat probe now take --dsn (Postgres connection string, or TARI_OBSERVABILITY_DSN) instead of --config <yaml path>. The static config/nodes.yaml is no longer the runtime source of truth for either binary.

### Features

* add tari-p2p-exporter for P2P-layer (Noise_XX + identity) reachability stats ([597c4f0](https://github.com/Snipa22/go-tari-observability/commit/597c4f0cb1eb9555e97a63e05423ceb93f20fc73))
* expose node version as a Prometheus info-gauge (tari_node_info) ([feaa874](https://github.com/Snipa22/go-tari-observability/commit/feaa874d31267208069acf104aef6e4858b56ddb))
* initial go-tari-observability scaffold (exporter, CLI, deploy templates) ([e613dc4](https://github.com/Snipa22/go-tari-observability/commit/e613dc455b03531e21405a37ce2470b2e6480325))
* replace static YAML config with Postgres-backed node registry + mapping-tool importer ([855e3eb](https://github.com/Snipa22/go-tari-observability/commit/855e3eba8ff2c106f7c8a7da6b3df4fc39c59b3d))
