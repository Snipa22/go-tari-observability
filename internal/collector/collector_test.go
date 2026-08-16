package collector

import (
	"context"
	"testing"
	"time"

	"github.com/Snipa22/go-tari-observability/internal/registry"
)

// TestPoll_UnreachableNode verifies that polling a node with nothing listening reports
// Up=false and a populated Err, rather than panicking or blocking past the timeout —
// this is the behavior tari-exporter depends on to keep polling the rest of the
// registry when one node (e.g. a priority node) is down.
func TestPoll_UnreachableNode(t *testing.T) {
	node := registry.Node{
		Name:     "unreachable-test-node",
		Tier:     "priority",
		IP:       "127.0.0.1",
		GRPCPort: 1, // nothing listens on port 1
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res := Poll(ctx, node)
	elapsed := time.Since(start)

	if res.Up {
		t.Fatalf("expected Up=false for an unreachable node, got Up=true: %+v", res)
	}
	if res.Err == nil {
		t.Fatal("expected a non-nil Err for an unreachable node")
	}
	if elapsed > DefaultTimeout+5*time.Second {
		t.Fatalf("Poll took %s, expected it to respect DefaultTimeout (%s) and return promptly", elapsed, DefaultTimeout)
	}
}

// TestPollAll_MixedReachability confirms one unreachable node in the registry does not
// prevent PollAll from returning a result for every node.
func TestPollAll_MixedReachability(t *testing.T) {
	nodes := []registry.Node{
		{Name: "node-a", IP: "127.0.0.1", GRPCPort: 1},
		{Name: "node-b", IP: "127.0.0.1", GRPCPort: 2},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	results := PollAll(ctx, nodes)
	if len(results) != len(nodes) {
		t.Fatalf("expected %d results, got %d", len(nodes), len(results))
	}
	for i, r := range results {
		if r.Up {
			t.Errorf("result[%d] (%s): expected Up=false", i, nodes[i].Name)
		}
		if r.Node.Name != nodes[i].Name {
			t.Errorf("result[%d].Node.Name = %q, want %q", i, r.Node.Name, nodes[i].Name)
		}
	}
}
