package main

import (
	"strings"
	"testing"
)

const sampleMetrics = `# HELP tari_node_up 1 if the node's base-node gRPC endpoint responded to GetTipInfo, 0 otherwise.
# TYPE tari_node_up gauge
tari_node_up{ip="1.2.3.4",node_name="priority-01",tier="priority"} 1
tari_node_up{ip="5.6.7.8",node_name="seed-01",tier="seed"} 0
# HELP tari_node_height Best known chain height reported by the node's GetTipInfo call.
# TYPE tari_node_height gauge
tari_node_height{ip="1.2.3.4",node_name="priority-01",tier="priority"} 100
tari_node_height{ip="5.6.7.8",node_name="seed-01",tier="seed"} 0
# HELP tari_node_peer_count Number of currently connected peers.
# TYPE tari_node_peer_count gauge
tari_node_peer_count{ip="1.2.3.4",node_name="priority-01",tier="priority"} 42
# HELP tari_node_sync_lag best-effort sync lag
# TYPE tari_node_sync_lag gauge
tari_node_sync_lag{ip="1.2.3.4",node_name="priority-01",tier="priority"} 0
`

func TestParseMetricsAndBuildRows(t *testing.T) {
	families, err := parseMetrics(strings.NewReader(sampleMetrics))
	if err != nil {
		t.Fatalf("parseMetrics returned error: %v", err)
	}

	rows := buildSnapshotRows(families)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}

	byName := make(map[string]snapshotRow)
	for _, r := range rows {
		byName[r.Name] = r
	}

	p, ok := byName["priority-01"]
	if !ok {
		t.Fatal("expected a row for priority-01")
	}
	if !p.HaveUp || !p.Up {
		t.Errorf("priority-01: expected Up=true, got %+v", p)
	}
	if !p.HaveH || p.Height != 100 {
		t.Errorf("priority-01: expected Height=100, got %+v", p)
	}
	if !p.HaveP || p.Peers != 42 {
		t.Errorf("priority-01: expected Peers=42, got %+v", p)
	}
	if !p.HaveL || p.Lag != 0 {
		t.Errorf("priority-01: expected Lag=0, got %+v", p)
	}

	s, ok := byName["seed-01"]
	if !ok {
		t.Fatal("expected a row for seed-01")
	}
	if !s.HaveUp || s.Up {
		t.Errorf("seed-01: expected Up=false, got %+v", s)
	}
	// seed-01 never reported peer_count or sync_lag in the sample data.
	if s.HaveP {
		t.Errorf("seed-01: expected HaveP=false since no peer_count series was present")
	}
	if s.HaveL {
		t.Errorf("seed-01: expected HaveL=false since no sync_lag series was present")
	}
}

func TestParseMetrics_MalformedInput(t *testing.T) {
	_, err := parseMetrics(strings.NewReader("this is not valid prometheus exposition format {{{"))
	if err == nil {
		t.Fatal("expected an error for malformed exposition-format input")
	}
}

func TestTierRank(t *testing.T) {
	if tierRank("priority") != 0 {
		t.Errorf("tierRank(priority) = %d, want 0", tierRank("priority"))
	}
	if tierRank("seed") != 1 {
		t.Errorf("tierRank(seed) = %d, want 1", tierRank("seed"))
	}
	if tierRank("unknown") != 1 {
		t.Errorf("tierRank(unknown) = %d, want 1", tierRank("unknown"))
	}
}
