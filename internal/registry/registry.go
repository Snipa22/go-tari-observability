// Package registry loads the configured Tari node registry (config/nodes.yaml) that
// tari-exporter and tari-netstat poll.
package registry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Node describes a single Tari base-node entry in the registry.
type Node struct {
	Name     string `yaml:"name"`
	Tier     string `yaml:"tier"`
	Pubkey   string `yaml:"pubkey"`
	IP       string `yaml:"ip"`
	P2PPort  int    `yaml:"p2p_port"`
	GRPCPort int    `yaml:"grpc_port"`
}

// Addr returns the "ip:grpc_port" dial target for this node.
func (n Node) Addr() string {
	return fmt.Sprintf("%s:%d", n.IP, n.GRPCPort)
}

type nodesFile struct {
	Nodes []Node `yaml:"nodes"`
}

// LoadFromFile reads and parses a nodes.yaml registry file at path.
func LoadFromFile(path string) ([]Node, error) {
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
