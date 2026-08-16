// Command tari-registry-importer periodically pulls supplementary node entries from a
// (currently placeholder) mapping-tool REST API and upserts them into the Postgres
// node registry.
//
// The mapping-tool service does not exist yet. --mapping-api-url defaults to a
// placeholder localhost URL; the expected response schema (see
// internal/importer.NodeEntry) is provisional and will need adjusting once that
// subsystem is actually built. Until then, this binary is meant to run continuously
// and simply log-and-skip every cycle where the endpoint is unreachable.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Snipa22/go-tari-observability/internal/importer"
	"github.com/Snipa22/go-tari-observability/internal/registry"
)

func main() {
	dsn := flag.String("dsn", envOr("TARI_OBSERVABILITY_DSN", ""), "Postgres connection string (or set TARI_OBSERVABILITY_DSN)")
	mappingURL := flag.String("mapping-api-url", envOr("TARI_MAPPING_API_URL", "http://localhost:8080/api/nodes"), "mapping-tool API URL to poll for supplementary nodes (PLACEHOLDER: this service doesn't exist yet)")
	interval := flag.Duration("interval", envDurationOr("TARI_MAPPING_IMPORT_INTERVAL", 5*time.Minute), "how often to poll the mapping-tool API")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("tari-registry-importer: --dsn (or TARI_OBSERVABILITY_DSN) is required")
	}

	store, err := registry.NewStore(*dsn)
	if err != nil {
		log.Fatalf("tari-registry-importer: connecting to database: %v", err)
	}
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	imp := importer.New(store, *mappingURL, *interval)
	log.Printf("tari-registry-importer: polling %s every %s", *mappingURL, *interval)
	imp.Run(ctx)
	log.Println("tari-registry-importer: shutting down")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
