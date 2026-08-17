package p2pcollector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Snipa22/go-tari-lib/p2p"
	"github.com/Snipa22/go-tari-observability/internal/registry"
)

// TestPoll_ProbeError verifies that a probe failure (e.g. a dial timeout or handshake
// error) is reported as Reachable=false with Err set, rather than panicking or leaving
// Result in some other inconsistent state. This uses an injected fake probeFunc instead
// of live network access, mirroring how internal/collector's tests avoid needing a live
// gRPC endpoint.
func TestPoll_ProbeError(t *testing.T) {
	node := registry.Node{
		Name:    "unreachable-p2p-test-node",
		Tier:    "priority",
		IP:      "127.0.0.1",
		P2PPort: 18189,
	}

	fakeErr := errors.New("dial tcp 127.0.0.1:18189: connection refused")
	fakeProbe := func(ctx context.Context, addr string) (*p2p.PeerInfo, error) {
		return nil, fakeErr
	}

	res := pollWith(context.Background(), node, fakeProbe)

	if res.Reachable {
		t.Fatalf("expected Reachable=false for a probe error, got Reachable=true: %+v", res)
	}
	if res.Err == nil {
		t.Fatal("expected a non-nil Err for a probe error")
	}
	if res.Node.Name != node.Name {
		t.Errorf("Result.Node.Name = %q, want %q", res.Node.Name, node.Name)
	}
}

// TestPoll_ContextTimeout verifies Poll respects DefaultTimeout and returns promptly
// rather than blocking forever when the injected probe never returns until its context
// is cancelled.
func TestPoll_ContextTimeout(t *testing.T) {
	node := registry.Node{Name: "slow-node", IP: "127.0.0.1", P2PPort: 18189}

	slowProbe := func(ctx context.Context, addr string) (*p2p.PeerInfo, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	start := time.Now()
	res := pollWith(context.Background(), node, slowProbe)
	elapsed := time.Since(start)

	if res.Reachable {
		t.Fatalf("expected Reachable=false for a timed-out probe, got Reachable=true: %+v", res)
	}
	if res.Err == nil {
		t.Fatal("expected a non-nil Err for a timed-out probe")
	}
	if elapsed > DefaultTimeout+5*time.Second {
		t.Fatalf("pollWith took %s, expected it to respect DefaultTimeout (%s) and return promptly", elapsed, DefaultTimeout)
	}
}

// TestPoll_Success verifies a successful probe populates every Result field from the
// returned PeerInfo, including the hex-encoded pubkey and identity-signature presence.
func TestPoll_Success(t *testing.T) {
	node := registry.Node{Name: "healthy-node", Tier: "seed", IP: "127.0.0.1", P2PPort: 18189}

	pubKey := []byte{0xde, 0xad, 0xbe, 0xef}
	fakeInfo := &p2p.PeerInfo{
		Reachable:          true,
		RemoteStaticPubKey: pubKey,
		Addresses:          [][]byte{[]byte("addr1"), []byte("addr2")},
		Features:           7,
		UserAgent:          "tari-basenode/1.0.0",
		IdentitySignature:  &p2p.IdentitySignature{Version: 1},
		Latency:            42 * time.Millisecond,
	}
	fakeProbe := func(ctx context.Context, addr string) (*p2p.PeerInfo, error) {
		return fakeInfo, nil
	}

	res := pollWith(context.Background(), node, fakeProbe)

	if !res.Reachable {
		t.Fatalf("expected Reachable=true, got false: %+v", res)
	}
	if res.Err != nil {
		t.Fatalf("expected nil Err, got %v", res.Err)
	}
	if res.PubKeyHex != "deadbeef" {
		t.Errorf("PubKeyHex = %q, want %q", res.PubKeyHex, "deadbeef")
	}
	if res.UserAgent != fakeInfo.UserAgent {
		t.Errorf("UserAgent = %q, want %q", res.UserAgent, fakeInfo.UserAgent)
	}
	if res.Features != 7 {
		t.Errorf("Features = %d, want 7", res.Features)
	}
	if res.AddressCount != 2 {
		t.Errorf("AddressCount = %d, want 2", res.AddressCount)
	}
	if !res.HasIdentitySignature {
		t.Error("expected HasIdentitySignature=true")
	}
	if res.HandshakeLatency != 42*time.Millisecond {
		t.Errorf("HandshakeLatency = %s, want 42ms", res.HandshakeLatency)
	}
}

// TestPoll_MissingP2PPort verifies a node with no known P2P port (P2PPort == 0, the
// registry's "unknown"/NULL sentinel) is reported as an explicit, clean failure rather
// than attempting to dial ":0" or panicking.
func TestPoll_MissingP2PPort(t *testing.T) {
	node := registry.Node{Name: "no-p2p-port-node", IP: "127.0.0.1", P2PPort: 0}

	called := false
	fakeProbe := func(ctx context.Context, addr string) (*p2p.PeerInfo, error) {
		called = true
		return nil, errors.New("should not be called")
	}

	res := pollWith(context.Background(), node, fakeProbe)

	if called {
		t.Fatal("expected probe not to be called for a node with no P2P port")
	}
	if res.Reachable {
		t.Fatal("expected Reachable=false for a node with no P2P port")
	}
	if res.Err == nil {
		t.Fatal("expected a non-nil Err for a node with no P2P port")
	}
}

// TestPollAll_MixedReachability confirms one unreachable node in the registry does not
// prevent PollAll from returning a result for every node.
func TestPollAll_MixedReachability(t *testing.T) {
	nodes := []registry.Node{
		{Name: "node-a", IP: "127.0.0.1", P2PPort: 1},
		{Name: "node-b", IP: "127.0.0.1", P2PPort: 2},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results := PollAll(ctx, nodes)
	if len(results) != len(nodes) {
		t.Fatalf("expected %d results, got %d", len(nodes), len(results))
	}
	for i, r := range results {
		if r.Node.Name != nodes[i].Name {
			t.Errorf("result[%d].Node.Name = %q, want %q", i, r.Node.Name, nodes[i].Name)
		}
	}
}
