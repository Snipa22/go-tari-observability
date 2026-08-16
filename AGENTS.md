---
description: go-tari-* ecosystem repo — Tari network Go tooling
---

# AGENTS.md

Instructions for AI coding agents (OpenCode, Claude Code, or any `agents.md`-compatible tool) working in this repository. Read this before making changes.

## Project

- **What this repo is:** Prometheus exporter + CLI for polling Tari base-node gRPC endpoints (sync height/state, peer count, version, uptime, mempool size, per-algo difficulty) across a configured registry of seed and priority nodes, for network-health observability and alerting (critical use case: detecting when Alex's priority nodes are down, which can halt the network for ~30 minutes).
- **Module path:** `github.com/Snipa22/go-tari-observability`
- **Depends on:** `go-tari-grpc-lib` (GRPC wrapper / generated Tari protobuf types)

## Commands

- **Build:** `go build ./...`
- **Test:** `go test ./...`
- **Vet:** `go vet ./...`
- **Format:** `gofmt -l .` (should return nothing; `gofmt -w .` to fix)
- **Tidy:** `go mod tidy`

Run build + vet + gofmt + test before considering any change complete. CI will re-check all four; catch failures locally first.

## Design note: gRPC client construction

`go-tari-grpc-lib`'s `nodeGRPC` package wraps the Tari base-node gRPC client behind a
single **package-level global connection** (`InitNodeGRPC` sets an unexported global
`*grpc.ClientConn`). That's fine for a single-node CLI tool but unsafe for this repo's
core use case — polling ~20+ nodes, some concurrently, on a timer. Re-calling
`InitNodeGRPC` from multiple goroutines races on that global.

Because of this, `internal/collector` does **not** call `nodeGRPC.InitNodeGRPC`/the
package-level wrapper functions. It dials its own `*grpc.ClientConn` per node and
constructs `tari_generated.NewBaseNodeClient(conn)` directly, reusing the exported
generated proto types from `go-tari-grpc-lib`'s `tari_generated` subpackage. This still
depends on `go-tari-grpc-lib` (for the generated stubs) without fighting its singleton
wrapper. If a future `go-tari-grpc-lib` release exposes a non-global, per-connection
client constructor, prefer switching to it and deleting this workaround — flag it in a
PR description rather than silently keeping the workaround past its need.

## Conventions

- **Conventional Commits** required — commit type (`feat`/`fix`/`chore`/etc.) drives automated SemVer via release-please. Don't guess the type; pick the one that matches the actual change.
- **Rebase, never merge.** No merge commits in PR branches. Rebase onto `main` before pushing updates.
- **No direct commits/pushes to `main`.** Always via PR.
- Follow existing package structure and naming — don't introduce a new pattern without checking how sibling `go-tari-*` repos do it first (they should be consistent; if they're not, that's a bug to flag, not a license to add a third way).
- Pin dependency versions explicitly in `go.mod` — this ecosystem has a known history of version skew across repos on `go-tari-grpc-lib`; don't make it worse.

## Don't

- Don't push directly to `main` or force-push shared branches.
- Don't add merge commits — rebase instead.
- Don't touch generated/vendored code (anything under a `_generated`, `tari_generated`, `tari_protos`, or similar directory) by hand — regenerate it from source instead.
- Don't silently change the licensing header or LICENSE file — that's a human decision, flag it instead.
- Don't skip tests because "there weren't any before" — add coverage for what you touch.
- Don't guess `config/nodes.yaml`'s `grpc_port` values as verified — they're an unconfirmed placeholder (see the file's own header comment). Run `cmd/tari-netstat probe` to confirm reachability before trusting it.

## Disclosure

If you (the agent) are making a substantial autonomous contribution, make sure the human operator adds a disclosure note to the PR per `CONTRIBUTING.md`. Don't assume this happens automatically — mention it if it's about to be skipped.
