// Package p2pcollector probes the P2P layer (Noise_XX handshake + identity exchange) of
// registry nodes via github.com/Snipa22/go-tari-lib's p2p package, and reports per-node
// observability data.
//
// This is deliberately separate from internal/collector (the existing gRPC-based
// application-layer collector): it probes a different port (P2PPort, not GRPCPort),
// speaks a different wire protocol entirely (Noise_XX + Tari's comms identity-exchange,
// not gRPC), and is meant to be operated as its own independent binary
// (cmd/tari-p2p-exporter) for isolated blast radius. See that package's doc comment for
// the full rationale.
package p2pcollector

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Snipa22/go-tari-lib/p2p"
	"github.com/Snipa22/go-tari-observability/internal/registry"
)

// DefaultTimeout bounds a single node's P2P probe (dial + Noise_XX handshake + identity
// exchange). It is deliberately larger than internal/collector.DefaultTimeout: the
// identity-exchange protocol itself applies its own internal 10s timeout (see
// go-tari-lib/p2p, ExchangeIdentity), so this wrapper timeout needs enough headroom
// past that not to race it — 15s gives ~5s of slack for the dial + Noise handshake that
// happen before the identity exchange even starts.
const DefaultTimeout = 15 * time.Second

// probeFunc matches p2p.Probe's signature. It exists so tests can substitute a fake
// prober and exercise Poll's error/timeout handling without live network access — the
// same pattern internal/collector's tests use to avoid needing a live gRPC endpoint.
type probeFunc func(ctx context.Context, addr string) (*p2p.PeerInfo, error)

// defaultProbe is p2p.Probe, used by Poll unless overridden (tests only).
var defaultProbe probeFunc = p2p.Probe

// Result carries everything successfully (or not) collected from probing one node's
// P2P layer in one poll cycle.
type Result struct {
	Node registry.Node

	// Reachable is true only if the Noise_XX handshake and identity exchange both
	// completed successfully. False for any dial/handshake/protocol failure — the
	// specific reason is in Err, never a panic.
	Reachable bool
	Err       error

	HandshakeLatency time.Duration
	UserAgent        string

	// PubKeyHex is the peer's RemoteStaticPubKey, hex-encoded. Empty if the peer
	// wasn't reachable. Callers should only ever expose a short prefix of this in a
	// Prometheus label (see cmd/tari-p2p-exporter) — the full value is fine to log,
	// not to carry as unbounded-cardinality label data.
	PubKeyHex string

	// Features is the raw PeerIdentityMsg.features bitmask as reported by the peer.
	// Bit-meaning is not yet decoded/known by this package — it is surfaced as-is.
	Features uint32

	AddressCount         int
	HasIdentitySignature bool

	PolledAt time.Time
}

// Poll probes node's P2P layer and gathers everything this package knows how to
// collect. It never returns an error itself for a probe failure — that's represented
// via Result.Reachable / Result.Err so callers (the exporter's poll loop) can treat one
// dead/slow peer as data, not a fatal condition that blocks polling the rest of the
// registry.
func Poll(ctx context.Context, node registry.Node) Result {
	return pollWith(ctx, node, defaultProbe)
}

func pollWith(ctx context.Context, node registry.Node, probe probeFunc) Result {
	res := Result{Node: node, PolledAt: time.Now()}

	if node.P2PPort == 0 {
		res.Err = fmt.Errorf("p2pcollector: node %q has no known P2P port", node.Name)
		return res
	}

	addr := fmt.Sprintf("%s:%d", node.IP, node.P2PPort)

	probeCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	info, err := probe(probeCtx, addr)
	if err != nil {
		res.Err = fmt.Errorf("probe %s: %w", addr, err)
		return res
	}

	res.Reachable = info.Reachable
	res.HandshakeLatency = info.Latency
	res.UserAgent = info.UserAgent
	res.PubKeyHex = hex.EncodeToString(info.RemoteStaticPubKey)
	res.Features = info.Features
	res.AddressCount = len(info.Addresses)
	res.HasIdentitySignature = info.IdentitySignature != nil

	return res
}

// PollAll polls every node in nodes sequentially and returns one Result per node, in
// the same order. Sequential polling (not one goroutine per node) is deliberate here for
// the same reason as internal/collector.PollAll: it keeps the poll loop simple and avoids
// any risk of exceeding this sandbox/network's outbound connection limits, and P2P
// probes are heavier (a full Noise handshake) than a gRPC call, so a shorter
// poll-interval / larger registry may need a bounded worker pool instead — not needed at
// current registry size.
func PollAll(ctx context.Context, nodes []registry.Node) []Result {
	results := make([]Result, len(nodes))
	for i, n := range nodes {
		results[i] = Poll(ctx, n)
	}
	return results
}
