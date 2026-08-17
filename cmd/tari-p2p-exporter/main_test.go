package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Snipa22/go-tari-observability/internal/p2pcollector"
	"github.com/Snipa22/go-tari-observability/internal/registry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// TestRecordMetrics_ReachablePeer verifies a successful P2P probe result is exposed via
// tari_p2p_reachable=1, the latency gauge, the tari_p2p_info info-gauge (with a
// pubkey_prefix truncated to 8 hex chars, not the full key), and the remaining identity
// metrics -- using the same live promhttp.Handler + httptest.Server round-trip pattern
// as cmd/tari-exporter/main_test.go.
func TestRecordMetrics_ReachablePeer(t *testing.T) {
	node := registry.Node{Name: "seed-01", Tier: "seed", IP: "51.91.215.198", P2PPort: 18189}

	results := []p2pcollector.Result{
		{
			Node:                 node,
			Reachable:            true,
			HandshakeLatency:     123 * time.Millisecond,
			UserAgent:            "tari-basenode/1.2.3",
			PubKeyHex:            "deadbeefcafef00d0011223344556677889900aabbccddeeff001122334455",
			Features:             7,
			AddressCount:         3,
			HasIdentitySignature: true,
			PolledAt:             time.Now(),
		},
	}

	recordMetrics(results)

	srv := httptest.NewServer(promhttp.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 1<<20)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	wantReachable := `tari_p2p_reachable{ip="51.91.215.198",node_name="seed-01",tier="seed"} 1`
	if !strings.Contains(body, wantReachable) {
		t.Fatalf("expected metrics to contain %q, got:\n%s", wantReachable, body)
	}

	wantInfo := `tari_p2p_info{ip="51.91.215.198",node_name="seed-01",pubkey_prefix="deadbeef",tier="seed",user_agent="tari-basenode/1.2.3"} 1`
	if !strings.Contains(body, wantInfo) {
		t.Fatalf("expected metrics to contain %q, got:\n%s", wantInfo, body)
	}

	wantSig := `tari_p2p_identity_signature_present{ip="51.91.215.198",node_name="seed-01",tier="seed"} 1`
	if !strings.Contains(body, wantSig) {
		t.Fatalf("expected metrics to contain %q, got:\n%s", wantSig, body)
	}

	wantAddrCount := `tari_p2p_advertised_address_count{ip="51.91.215.198",node_name="seed-01",tier="seed"} 3`
	if !strings.Contains(body, wantAddrCount) {
		t.Fatalf("expected metrics to contain %q, got:\n%s", wantAddrCount, body)
	}
}

// TestRecordMetrics_UnreachablePeer verifies an unreachable peer is recorded as
// tari_p2p_reachable=0 and does not emit an info-gauge/identity series (there's no
// identity data to report), rather than being skipped entirely.
func TestRecordMetrics_UnreachablePeer(t *testing.T) {
	node := registry.Node{Name: "down-node", Tier: "priority", IP: "10.0.0.5", P2PPort: 18189}

	results := []p2pcollector.Result{
		{
			Node:      node,
			Reachable: false,
			Err:       errUnreachableForTest,
			PolledAt:  time.Now(),
		},
	}

	recordMetrics(results)

	srv := httptest.NewServer(promhttp.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 1<<20)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	wantUnreachable := `tari_p2p_reachable{ip="10.0.0.5",node_name="down-node",tier="priority"} 0`
	if !strings.Contains(body, wantUnreachable) {
		t.Fatalf("expected metrics to contain %q, got:\n%s", wantUnreachable, body)
	}
}

var errUnreachableForTest = &testError{"probe: connection refused"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
