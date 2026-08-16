# go-tari-observability

Prometheus-based network observability toolkit for the [Tari](https://github.com/tari-project/tari)
network. Part of Alex Blair's (`Snipa22`) `go-tari-*` ecosystem of Go tooling for Tari
Core Contributor (Infrastructure) work.

Polls a configured registry of Tari base-node gRPC endpoints — seed nodes and priority
nodes — for chain height, sync state, peer count, mempool size, and per-algo network
hashrate, and exposes it as Prometheus metrics for alerting. The critical use case: know
immediately when a priority node goes down, since losing enough priority nodes can halt
the network for on the order of 30 minutes.

## Binaries

- **`cmd/tari-exporter`** — long-running Prometheus exporter. Reads the current node
  list live from Postgres and polls every node on an interval, exposing `/metrics`
  (Prometheus text exposition format) and `/healthz`. The node list can grow/shrink at
  runtime as Postgres changes — no restart required.
- **`cmd/tari-netstat`** — CLI companion.
  - `tari-netstat snapshot` (default) — reads a running exporter's `/metrics` and prints
    a human-readable table, priority tier first. Unaffected by the registry redesign
    below — it talks to the exporter, not the registry.
  - `tari-netstat probe` — dials every node directly over gRPC (bypassing the exporter)
    with a short timeout and reports raw reachability. Reads the node list from
    Postgres via `--dsn` by default; `--file <bootstrap-nodes.yaml>` is an offline/testing
    convenience fallback.
- **`cmd/tari-registry-bootstrap`** — one-shot CLI. Loads a bootstrap YAML file
  (`config/bootstrap-nodes.yaml`) and upserts it into Postgres. Run this once against a
  fresh database.
- **`cmd/tari-registry-importer`** — long-running importer. Polls a (currently
  placeholder) mapping-tool REST API on an interval and upserts what it finds into
  Postgres as supplementary, `tier=imported` nodes.

## Architecture: Postgres is the node-list source of truth

Earlier versions of this tool read a static `config/nodes.yaml` at exporter startup.
That design was wrong: "Static assumptions are going to be bad" — a hand-maintained
YAML file is not the canonical node list, and the exporter's registry needs to be able
to grow via other means (an internal team adding nodes by hand, and eventually an
automated mapping-tool integration) without a restart or a PR to this repo.

The current design:

1. **Postgres holds the live node list.** `internal/registry.Store` wraps a `nodes`
   table (schema in [`internal/registry/schema.sql`](internal/registry/schema.sql)) with
   `List` (all enabled nodes), `Upsert` (insert-or-update, keyed by `pubkey` when known,
   else `name`), and `Bootstrap` (idempotent bulk-seed).
2. **YAML is only a one-time bootstrap seed**, not a runtime config. Apply
   `internal/registry/schema.sql` to a fresh database, then run:

   ```sh
   tari-registry-bootstrap --dsn "$TARI_OBSERVABILITY_DSN" --file config/bootstrap-nodes.yaml
   ```

   [`config/bootstrap-nodes.yaml`](config/bootstrap-nodes.yaml) holds the same
   hand-curated seed/priority mainnet node list as before (sourced from the live mainnet
   `config_basenodes.toml`, with `grpc_port` corrected to the verified values: `18142`
   for seed tier, `18102` for priority tier). Bootstrap is idempotent — safe to re-run,
   it upserts rather than duplicates.
3. **`tari-registry-importer` pulls supplementary nodes from a mapping-tool API.** This
   is forward-looking plumbing: **the mapping-tool service does not exist yet.**
   `--mapping-api-url` defaults to a placeholder (`http://localhost:8080/api/nodes`),
   and the expected JSON response shape —
   `[{"pubkey": "...", "ip": "...", "p2p_port": N, "grpc_port": N}, ...]` — is
   **provisional** (see `internal/importer.NodeEntry`). It *will* need adjusting once
   the real mapping-tool API is designed. The importer polls this endpoint on an
   interval (`--interval`, default `5m`) and upserts everything it gets back with
   `source="mapping-tool-import"`, `tier="imported"`. An unreachable/malformed endpoint
   is logged and that cycle is skipped — it never crashes the importer, since "the
   endpoint doesn't exist yet" is the expected steady state for now.
4. **`tari-exporter` and `tari-netstat probe` read live from Postgres**, not YAML.
   `tari-exporter` fetches the node list once at startup and again on a
   `--registry-refresh-interval` (default `30s`, independent from `--poll-interval`),
   so nodes added/removed in Postgres are picked up without restarting the exporter.

## Running

```sh
go build -o bin/tari-exporter ./cmd/tari-exporter
go build -o bin/tari-netstat ./cmd/tari-netstat
go build -o bin/tari-registry-bootstrap ./cmd/tari-registry-bootstrap
go build -o bin/tari-registry-importer ./cmd/tari-registry-importer

export TARI_OBSERVABILITY_DSN="postgres://user:pass@localhost:5432/tari_observability?sslmode=disable"

# Apply the schema once against a fresh database:
psql "$TARI_OBSERVABILITY_DSN" -f internal/registry/schema.sql

# Seed it with the hand-curated node list (idempotent, safe to re-run):
./bin/tari-registry-bootstrap --file config/bootstrap-nodes.yaml

# Run the exporter — it now reads its node list from Postgres, not YAML:
./bin/tari-exporter --listen :9469 --poll-interval 30s &
curl localhost:9469/metrics
curl localhost:9469/healthz

# Probe reachability directly (also reads from Postgres by default):
./bin/tari-netstat probe
./bin/tari-netstat snapshot --exporter-url http://localhost:9469

# Run the importer to pick up supplementary nodes from the (placeholder) mapping-tool API:
./bin/tari-registry-importer --mapping-api-url http://localhost:8080/api/nodes --interval 5m &
```

**Breaking change from earlier versions:** `tari-exporter` and `tari-netstat probe` no
longer take `--config <yaml>`. They take `--dsn <postgres-connection-string>` (or the
`TARI_OBSERVABILITY_DSN` env var) instead. `tari-netstat probe --file <yaml>` remains as
an offline/testing convenience only — it is not the default path.

## Node registry

Postgres is the source of truth (see Architecture above).
[`config/bootstrap-nodes.yaml`](config/bootstrap-nodes.yaml) is only the one-time seed
list consumed by `tari-registry-bootstrap` — it is not read by the exporter or by
`tari-netstat probe`'s default (`--dsn`) path.

## Metrics exposed by `tari-exporter`

All series are labeled `node_name`, `tier`, `ip` (plus one extra label noted below).

| Metric | Source RPC | Notes |
|---|---|---|
| `tari_node_up` | `GetTipInfo` | 1/0 reachability |
| `tari_node_height` | `GetTipInfo` | chain tip height |
| `tari_node_peer_count` | `ListConnectedPeers` | connected peer count |
| `tari_node_mempool_size` | `GetMempoolStats` | unconfirmed tx count |
| `tari_node_sync_lag` | derived | max height across the poll cycle minus this node's height |
| `tari_node_last_scrape_success_timestamp_seconds` | derived | last time this node responded successfully |
| `tari_node_network_difficulty` | `GetNetworkDifficulty` | total, not split per algo (see below) |
| `tari_node_hash_rate` | `GetNetworkDifficulty` | per-algo estimated hash rate, extra label `algo` (`sha3x`\|`monero_randomx`\|`tari_randomx`) |
| `tari_node_info` | `GetVersion` | standard Prometheus "info" pattern — always `1`, version carried as the extra label `version`. Query rollout state with `count(tari_node_info) by (version)`. Absent for a node/cycle where the version wasn't obtained (best-effort, never fabricated). |

Deeper per-node scraping (sync-state detail, block-propagation timing, consensus/fork
divergence) is scoped as a fast-follow, not yet implemented — see Known gaps below.

## Deploy templates

- [`deploy/prometheus/scrape-config.yml`](deploy/prometheus/scrape-config.yml) — scrape
  config snippet for the exporter's `/metrics`.
- [`deploy/grafana/tari-observability-dashboard.json`](deploy/grafana/tari-observability-dashboard.json)
  — importable Grafana dashboard (node up/down, height, peer count, sync lag).
- [`deploy/alertmanager/tari-priority-node-rules.yml`](deploy/alertmanager/tari-priority-node-rules.yml)
  — Prometheus alerting *rules* (loaded by Prometheus, not Alertmanager itself):
  `TariPriorityNodeDown` and `TariNodeSyncLagHigh`.

## Known gaps

See `AGENTS.md` for the design note on why this repo dials its own gRPC connections
per-node instead of using `go-tari-grpc-lib`'s `nodeGRPC` package directly (that
package's wrapper functions sit behind a single package-level global connection, unsafe
for polling many nodes on a timer).

Per-algorithm network **difficulty** isn't cleanly separable from the base-node gRPC
API as currently wrapped — `GetNetworkDifficulty` returns one `difficulty` value plus
separate per-algo *estimated hash rate* fields. `tari-exporter` exposes total network
difficulty and per-algo hash rate; it does not fabricate a per-algo difficulty split.

Node software version is exposed via a best-effort `GetVersion` call; if a given
base-node build doesn't implement it, the exporter logs the failure and omits the
version label rather than guessing.
