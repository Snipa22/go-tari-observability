package registry

import (
	"path/filepath"
	"testing"
)

func TestLoadYAML(t *testing.T) {
	path := filepath.Join("testdata", "nodes.yaml")

	nodes, err := LoadYAML(path)
	if err != nil {
		t.Fatalf("LoadYAML(%q) returned error: %v", path, err)
	}

	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	want := []Node{
		{Name: "seed-01", Tier: "seed", Pubkey: "abc123", IP: "10.0.0.1", P2PPort: 18189, GRPCPort: 18142},
		{Name: "priority-01", Tier: "priority", Pubkey: "def456", IP: "10.0.0.2", P2PPort: 18189, GRPCPort: 18142},
		{Name: "priority-02", Tier: "priority", Pubkey: "ghi789", IP: "10.0.0.3", P2PPort: 18189, GRPCPort: 19999},
	}

	for i, w := range want {
		got := nodes[i]
		if got != w {
			t.Errorf("node[%d] = %+v, want %+v", i, got, w)
		}
	}

	if addr := nodes[2].Addr(); addr != "10.0.0.3:19999" {
		t.Errorf("Addr() = %q, want %q", addr, "10.0.0.3:19999")
	}
}

func TestLoadYAML_MissingFile(t *testing.T) {
	_, err := LoadYAML(filepath.Join("testdata", "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadYAML_BadYAML(t *testing.T) {
	path := filepath.Join("testdata", "bad.yaml")

	_, err := LoadYAML(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestLoadYAML_MissingName(t *testing.T) {
	path := filepath.Join("testdata", "missing-name.yaml")

	_, err := LoadYAML(path)
	if err == nil {
		t.Fatal("expected error for node missing a name, got nil")
	}
}
