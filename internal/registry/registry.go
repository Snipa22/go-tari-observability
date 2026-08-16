// Package registry is the Postgres-backed node registry for go-tari-observability.
//
// Postgres (via Store) is the runtime source of truth for the node list that
// tari-exporter and tari-netstat poll. It can grow or shrink at runtime — via manual
// inserts, the one-shot bootstrap command, or the periodic mapping-tool importer —
// without restarting anything that reads it.
//
// LoadYAML is kept only as a helper for seeding a fresh database (see
// cmd/tari-registry-bootstrap and config/bootstrap-nodes.yaml) and for tests that don't
// want to depend on a live Postgres instance. It is NOT read by the exporter or
// tari-netstat at runtime anymore.
package registry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Node describes a single Tari base-node entry in the registry. Field names/tags
// mirror the `nodes` table columns (see schema.sql).
type Node struct {
	ID     int64  `yaml:"-"`
	Name   string `yaml:"name"`
	Tier   string `yaml:"tier"`
	Pubkey string `yaml:"pubkey"`
	IP     string `yaml:"ip"`

	// P2PPort is nullable in the DB (unknown for some imported nodes); zero means
	// "unknown" here, mirroring a NULL column.
	P2PPort  int `yaml:"p2p_port"`
	GRPCPort int `yaml:"grpc_port"`

	// Source records where this row came from: "bootstrap", "manual", or
	// "mapping-tool-import". Not set by LoadYAML — callers (bootstrap/importer) set it.
	Source  string `yaml:"-"`
	Enabled bool   `yaml:"-"`
}

// Addr returns the "ip:grpc_port" dial target for this node.
func (n Node) Addr() string {
	return fmt.Sprintf("%s:%d", n.IP, n.GRPCPort)
}

type nodesFile struct {
	Nodes []Node `yaml:"nodes"`
}

// LoadYAML reads and parses a bootstrap-style nodes YAML file at path. It is used to
// seed a fresh database (cmd/tari-registry-bootstrap) or in tests — it is not consulted
// by the exporter or tari-netstat at runtime; Postgres is.
func LoadYAML(path string) ([]Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("registry: reading %s: %w", path, err)
	}

	var parsed nodesFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("registry: parsing %s: %w", path, err)
	}

	for i, n := range parsed.Nodes {
		if n.Name == "" {
			return nil, fmt.Errorf("registry: node at index %d is missing name", i)
		}
		if n.IP == "" {
			return nil, fmt.Errorf("registry: node %q is missing ip", n.Name)
		}
	}

	return parsed.Nodes, nil
}
