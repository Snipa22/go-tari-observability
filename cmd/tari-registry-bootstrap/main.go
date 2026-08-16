// Command tari-registry-bootstrap is a one-shot CLI that seeds a fresh Postgres
// database with the initial hand-curated node list (config/bootstrap-nodes.yaml).
//
// Run this once against a fresh database (after applying
// internal/registry/schema.sql). It is idempotent — re-running it upserts, it does not
// duplicate rows — but it exists to seed a fresh DB, not to be the ongoing way nodes
// get added. After bootstrap, tari-exporter and tari-netstat read live from Postgres,
// and cmd/tari-registry-importer adds supplementary nodes over time.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/Snipa22/go-tari-observability/internal/registry"
)

func main() {
	dsn := flag.String("dsn", envOr("TARI_OBSERVABILITY_DSN", ""), "Postgres connection string (or set TARI_OBSERVABILITY_DSN)")
	file := flag.String("file", "config/bootstrap-nodes.yaml", "path to the bootstrap node list YAML file")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("tari-registry-bootstrap: --dsn (or TARI_OBSERVABILITY_DSN) is required")
	}

	nodes, err := registry.LoadYAML(*file)
	if err != nil {
		log.Fatalf("tari-registry-bootstrap: loading %s: %v", *file, err)
	}
	log.Printf("tari-registry-bootstrap: loaded %d nodes from %s", len(nodes), *file)

	store, err := registry.NewStore(*dsn)
	if err != nil {
		log.Fatalf("tari-registry-bootstrap: connecting to database: %v", err)
	}
	defer store.Close()

	if err := store.Bootstrap(context.Background(), nodes); err != nil {
		log.Fatalf("tari-registry-bootstrap: bootstrap failed: %v", err)
	}

	log.Printf("tari-registry-bootstrap: bootstrap complete, %d nodes upserted", len(nodes))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
