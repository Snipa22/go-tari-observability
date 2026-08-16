// Package collector polls a set of Tari base-node gRPC endpoints and reports
// per-node observability data.
//
// It intentionally does NOT use go-tari-grpc-lib's nodeGRPC wrapper package
// (nodeGRPC.InitNodeGRPC / package-level client functions) — that package keeps its
// gRPC connection behind a single unexported package-level global, which is unsafe to
// share across goroutines polling many nodes concurrently on a timer. Instead this
// package dials its own *grpc.ClientConn per node and builds a
// tari_generated.BaseNodeClient directly, reusing go-tari-grpc-lib's exported generated
// proto types. See AGENTS.md for the full rationale.
package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/Snipa22/go-tari-grpc-lib/v3/tari_generated"
	"github.com/Snipa22/go-tari-observability/internal/registry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DefaultTimeout bounds every RPC made against a single node during a poll. A single
// dead priority node must never hold up polling of the rest of the registry.
const DefaultTimeout = 5 * time.Second

// Result carries everything successfully (or not) collected from one node in one poll.
type Result struct {
	Node registry.Node

	Up  bool // false if the gRPC dial or the tip-info call failed.
	Err error

	Height      uint64
	PeerCount   int
	MempoolSize uint64

	// Version is best-effort: some base-node builds/network configs may not expose
	// GetVersion cleanly. Empty string + VersionOK=false means "we don't know", not
	// "empty version string".
	Version   string
	VersionOK bool

	// NetworkDifficulty is the total network difficulty at the tip, as reported by
	// GetNetworkDifficulty. The underlying RPC does NOT cleanly separate difficulty
	// per mining algorithm in this protocol version — it gives one difficulty value
	// plus separate per-algo *estimated hash rate* fields, which are exposed below
	// instead of a fabricated per-algo difficulty split.
	NetworkDifficulty uint64
	DifficultyOK      bool

	Sha3xHashRate         uint64
	MoneroRandomXHashRate uint64
	TariRandomXHashRate   uint64
	HashRateOK            bool

	PolledAt time.Time
}

// Poll dials node directly and gathers every metric this package knows how to collect.
// It never returns an error itself for a node-reachability problem — that's represented
// via Result.Up / Result.Err so that callers (the exporter's poll loop) can treat one
// dead node as data, not a fatal condition.
func Poll(ctx context.Context, node registry.Node) Result {
	res := Result{Node: node, PolledAt: time.Now()}

	dialCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	conn, err := grpc.NewClient(node.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		res.Err = fmt.Errorf("dial %s: %w", node.Addr(), err)
		return res
	}
	defer conn.Close()

	client := tari_generated.NewBaseNodeClient(conn)

	tipCtx, tipCancel := context.WithTimeout(dialCtx, DefaultTimeout)
	defer tipCancel()

	tip, err := client.GetTipInfo(tipCtx, &tari_generated.Empty{})
	if err != nil {
		res.Err = fmt.Errorf("GetTipInfo: %w", err)
		return res
	}

	res.Up = true
	if md := tip.GetMetadata(); md != nil {
		res.Height = md.GetBestBlockHeight()
	}

	// Everything past this point is best-effort: a partial poll (height known, peer
	// count unknown because that one RPC errored) is still valuable data and should
	// not flip Up back to false.

	res.PeerCount = pollPeerCount(ctx, client)
	res.MempoolSize = pollMempoolSize(ctx, client)
	res.Version, res.VersionOK = pollVersion(ctx, client)
	res.NetworkDifficulty, res.Sha3xHashRate, res.MoneroRandomXHashRate, res.TariRandomXHashRate, res.DifficultyOK, res.HashRateOK = pollDifficulty(ctx, client)

	return res
}

func pollPeerCount(ctx context.Context, client tari_generated.BaseNodeClient) int {
	peersCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	peers, err := client.ListConnectedPeers(peersCtx, &tari_generated.Empty{})
	if err != nil {
		return 0
	}
	return len(peers.GetConnectedPeers())
}

func pollMempoolSize(ctx context.Context, client tari_generated.BaseNodeClient) uint64 {
	mpCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	mp, err := client.GetMempoolStats(mpCtx, &tari_generated.Empty{})
	if err != nil {
		return 0
	}
	return mp.GetUnconfirmedTxs()
}

func pollVersion(ctx context.Context, client tari_generated.BaseNodeClient) (string, bool) {
	verCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	ver, err := client.GetVersion(verCtx, &tari_generated.Empty{})
	if err != nil {
		return "", false
	}
	return ver.GetVersion(), true
}

// pollDifficulty returns (totalDifficulty, sha3xHashRate, moneroRandomXHashRate,
// tariRandomXHashRate, difficultyOK, hashRateOK).
func pollDifficulty(ctx context.Context, client tari_generated.BaseNodeClient) (uint64, uint64, uint64, uint64, bool, bool) {
	diffCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	diffStream, err := client.GetNetworkDifficulty(diffCtx, &tari_generated.HeightRequest{FromTip: 1})
	if err != nil {
		return 0, 0, 0, 0, false, false
	}
	diff, err := diffStream.Recv()
	if err != nil {
		return 0, 0, 0, 0, false, false
	}
	return diff.GetDifficulty(), diff.GetSha3XEstimatedHashRate(), diff.GetMoneroRandomxEstimatedHashRate(), diff.GetTariRandomxEstimatedHashRate(), true, true
}

// PollAll polls every node in nodes sequentially and returns one Result per node, in
// the same order. Sequential (not per-node-goroutine) polling is deliberate: see
// AGENTS.md — go-tari-grpc-lib's generated client is safe to use this way since each
// node gets its own *grpc.ClientConn, but keeping the poll loop simple and sequential
// avoids any risk of exceeding this sandbox/network's outbound connection limits when
// polling ~20 nodes with 5s per-RPC timeouts. Exporters with a larger registry or a
// shorter poll-interval budget should parallelize this with a bounded worker pool.
func PollAll(ctx context.Context, nodes []registry.Node) []Result {
	results := make([]Result, len(nodes))
	for i, n := range nodes {
		results[i] = Poll(ctx, n)
	}
	return results
}
