// Command tari-netstat is a CLI companion to tari-exporter.
//
//   - `tari-netstat snapshot` (default): reads a running exporter's /metrics endpoint
//     and prints a human-readable table.
//   - `tari-netstat probe`: dials every node in the registry directly over gRPC,
//     bypassing the exporter entirely, and reports raw reachability.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/Snipa22/go-tari-observability/internal/collector"
	"github.com/Snipa22/go-tari-observability/internal/registry"
)

func main() {
	args := os.Args[1:]

	subcommand := "snapshot"
	if len(args) > 0 && !flagLike(args[0]) {
		subcommand = args[0]
		args = args[1:]
	}

	var err error
	switch subcommand {
	case "snapshot":
		err = runSnapshot(args)
	case "probe":
		err = runProbe(args)
	default:
		fmt.Fprintf(os.Stderr, "tari-netstat: unknown subcommand %q (expected \"snapshot\" or \"probe\")\n", subcommand)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "tari-netstat: %v\n", err)
		os.Exit(1)
	}
}

func flagLike(s string) bool {
	return len(s) > 0 && s[0] == '-'
}

func runProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	dsn := fs.String("dsn", envOr("TARI_OBSERVABILITY_DSN", ""), "Postgres connection string for the node registry (or set TARI_OBSERVABILITY_DSN)")
	filePath := fs.String("file", "", "path to a bootstrap-style node YAML file to probe instead of Postgres (offline/testing convenience; Postgres via --dsn is the default source)")
	timeout := fs.Duration("timeout", 5*time.Second, "per-node dial+RPC timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var (
		nodes []registry.Node
		err   error
		src   string
	)
	switch {
	case *filePath != "":
		nodes, err = registry.LoadYAML(*filePath)
		src = *filePath
		if err != nil {
			return fmt.Errorf("loading registry from file: %w", err)
		}
	case *dsn != "":
		store, storeErr := registry.NewStore(*dsn)
		if storeErr != nil {
			return fmt.Errorf("connecting to node registry database: %w", storeErr)
		}
		defer store.Close()
		nodes, err = store.List(context.Background())
		src = "Postgres"
		if err != nil {
			return fmt.Errorf("listing nodes from registry: %w", err)
		}
	default:
		return fmt.Errorf("either --dsn (or TARI_OBSERVABILITY_DSN) or --file must be set")
	}

	sort.SliceStable(nodes, func(i, j int) bool { return tierRank(nodes[i].Tier) < tierRank(nodes[j].Tier) })

	fmt.Printf("Probing %d nodes from %s (timeout=%s per node)...\n\n", len(nodes), src, *timeout)
	fmt.Printf("%-14s %-9s %-22s %-8s %s\n", "NAME", "TIER", "ADDR", "REACHED", "DETAIL")

	reached := 0
	for _, n := range nodes {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		res := collector.Poll(ctx, n)
		cancel()

		status := "NO"
		detail := ""
		if res.Up {
			status = "YES"
			reached++
			detail = fmt.Sprintf("height=%d", res.Height)
		} else if res.Err != nil {
			detail = res.Err.Error()
		}
		fmt.Printf("%-14s %-9s %-22s %-8s %s\n", n.Name, n.Tier, n.Addr(), status, detail)
	}

	fmt.Printf("\n%d/%d nodes reachable at their configured grpc_port.\n", reached, len(nodes))
	if reached == 0 {
		fmt.Println("(0 reachable is expected if this process has no outbound network access to these hosts — the point of this command is to run and report honestly, not to guarantee connectivity.)")
	}

	return nil
}

func tierRank(tier string) int {
	if tier == "priority" {
		return 0
	}
	return 1
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
