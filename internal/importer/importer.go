// Package importer periodically pulls supplementary node entries from a mapping-tool
// REST API and upserts them into the registry Store.
//
// IMPORTANT: the mapping-tool service does not exist yet. --mapping-api-url is a
// placeholder default (http://localhost:8080/api/nodes) and the expected response
// shape (see NodeEntry below) is provisional — it will need to change once the real
// mapping-tool API is designed. This package is forward-looking plumbing: it must
// degrade gracefully (log + skip one cycle) when the endpoint is unreachable or returns
// something unexpected, since that's the expected state until that subsystem is built.
package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Snipa22/go-tari-observability/internal/registry"
)

// Source records the value written to nodes.source for every node this importer
// upserts.
const Source = "mapping-tool-import"

// Tier records the value written to nodes.tier for every node this importer upserts.
// Nodes discovered via the mapping tool are, by definition, not part of the
// hand-curated seed/priority bootstrap list.
const Tier = "imported"

// NodeEntry is the provisional shape expected from the mapping-tool API: a JSON array
// of these objects. THIS SCHEMA IS A PLACEHOLDER — adjust field names/types here (and
// in README.md) once the real mapping-tool API is defined.
type NodeEntry struct {
	Pubkey   string `json:"pubkey"`
	IP       string `json:"ip"`
	P2PPort  int    `json:"p2p_port"`
	GRPCPort int    `json:"grpc_port"`
}

// Importer polls a mapping-tool API endpoint on an interval and upserts every entry it
// returns into a registry Store.
type Importer struct {
	Store      *registry.Store
	URL        string
	Interval   time.Duration
	HTTPClient *http.Client
}

// New returns an Importer with a sane default HTTP client/timeout. Callers should still
// set Interval explicitly.
func New(store *registry.Store, url string, interval time.Duration) *Importer {
	return &Importer{
		Store:      store,
		URL:        url,
		Interval:   interval,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Run polls once immediately, then again on every tick of imp.Interval, until ctx is
// cancelled. It never returns an error for a single failed cycle — a cycle's failure
// (endpoint down, malformed JSON, etc.) is logged and skipped, not fatal, since the
// mapping-tool service is expected to be absent/unreliable for the foreseeable future.
func (imp *Importer) Run(ctx context.Context) {
	imp.runOnce(ctx)

	ticker := time.NewTicker(imp.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			imp.runOnce(ctx)
		}
	}
}

func (imp *Importer) runOnce(ctx context.Context) {
	entries, err := imp.fetch(ctx)
	if err != nil {
		log.Printf("tari-registry-importer: skipping this cycle, fetch from %s failed: %v", imp.URL, err)
		return
	}

	upserted := 0
	for _, e := range entries {
		if e.IP == "" || e.GRPCPort == 0 {
			log.Printf("tari-registry-importer: skipping malformed entry (missing ip or grpc_port): %+v", e)
			continue
		}
		node := registry.Node{
			Name:     nameFor(e),
			Tier:     Tier,
			Pubkey:   e.Pubkey,
			IP:       e.IP,
			P2PPort:  e.P2PPort,
			GRPCPort: e.GRPCPort,
			Source:   Source,
			Enabled:  true,
		}
		if err := imp.Store.Upsert(ctx, node); err != nil {
			log.Printf("tari-registry-importer: upserting %s failed: %v", node.Addr(), err)
			continue
		}
		upserted++
	}
	log.Printf("tari-registry-importer: cycle complete, upserted %d/%d entries from %s", upserted, len(entries), imp.URL)
}

// nameFor derives a stable-ish name for an imported node lacking one in the API
// response: pubkey-prefixed if known, otherwise its address. This is provisional —
// once the real mapping-tool API is defined it may supply names directly.
func nameFor(e NodeEntry) string {
	if e.Pubkey != "" {
		if len(e.Pubkey) > 12 {
			return "imported-" + e.Pubkey[:12]
		}
		return "imported-" + e.Pubkey
	}
	return fmt.Sprintf("imported-%s-%d", e.IP, e.GRPCPort)
}

func (imp *Importer) fetch(ctx context.Context) ([]NodeEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imp.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := imp.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", imp.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("unexpected status %s from %s: %s", resp.Status, imp.URL, string(body))
	}

	var entries []NodeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decoding response from %s: %w", imp.URL, err)
	}
	return entries, nil
}
