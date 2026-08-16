// Command tari-exporter is a Prometheus exporter that periodically polls a configured
// registry of Tari base-node gRPC endpoints and exposes their observability data as
// Prometheus metrics.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Snipa22/go-tari-observability/internal/collector"
	"github.com/Snipa22/go-tari-observability/internal/registry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	nodeUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_node_up",
		Help: "1 if the node's base-node gRPC endpoint responded to GetTipInfo, 0 otherwise.",
	}, []string{"node_name", "tier", "ip"})

	nodeHeight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_node_height",
		Help: "Best known chain height reported by the node's GetTipInfo call.",
	}, []string{"node_name", "tier", "ip"})

	nodePeerCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_node_peer_count",
		Help: "Number of currently connected peers reported by ListConnectedPeers.",
	}, []string{"node_name", "tier", "ip"})

	nodeMempoolSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_node_mempool_size",
		Help: "Number of unconfirmed transactions in the node's mempool (GetMempoolStats.unconfirmed_txs).",
	}, []string{"node_name", "tier", "ip"})

	nodeSyncLag = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_node_sync_lag",
		Help: "Best-effort: max known height across all successfully polled nodes in this poll cycle, minus this node's height. 0 for the tallest node(s); absent (not set) if this node's height wasn't obtained this cycle.",
	}, []string{"node_name", "tier", "ip"})

	nodeLastScrapeSuccess = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_node_last_scrape_success_timestamp_seconds",
		Help: "Unix timestamp of the last poll cycle in which this node responded successfully (Up=1).",
	}, []string{"node_name", "tier", "ip"})

	nodeNetworkDifficulty = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_node_network_difficulty",
		Help: "Best-effort: total network difficulty at tip as reported by GetNetworkDifficulty. Not split per mining algorithm — this gRPC response doesn't cleanly separate difficulty per algo, only per-algo estimated hash rate (see tari_node_hash_rate).",
	}, []string{"node_name", "tier", "ip"})

	nodeHashRate = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_node_hash_rate",
		Help: "Best-effort per-algorithm estimated network hash rate as reported by GetNetworkDifficulty.",
	}, []string{"node_name", "tier", "ip", "algo"})
)

func init() {
	prometheus.MustRegister(
		nodeUp,
		nodeHeight,
		nodePeerCount,
		nodeMempoolSize,
		nodeSyncLag,
		nodeLastScrapeSuccess,
		nodeNetworkDifficulty,
		nodeHashRate,
	)
}

func main() {
	dsn := flag.String("dsn", envOr("TARI_OBSERVABILITY_DSN", ""), "Postgres connection string for the node registry (or set TARI_OBSERVABILITY_DSN)")
	listen := flag.String("listen", envOr("TARI_EXPORTER_LISTEN", ":9469"), "address to listen on for /metrics and /healthz")
	pollInterval := flag.Duration("poll-interval", envDurationOr("TARI_EXPORTER_POLL_INTERVAL", 30*time.Second), "how often to poll every node in the registry")
	registryRefreshInterval := flag.Duration("registry-refresh-interval", envDurationOr("TARI_EXPORTER_REGISTRY_REFRESH_INTERVAL", 30*time.Second), "how often to re-fetch the node list from Postgres; defaults to the same cadence as poll-interval since both are cheap and the registry is expected to change slowly")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("tari-exporter: --dsn (or TARI_OBSERVABILITY_DSN) is required")
	}

	store, err := registry.NewStore(*dsn)
	if err != nil {
		log.Fatalf("tari-exporter: failed to connect to node registry database: %v", err)
	}
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	registryHolder := newRegistryHolder(store)
	if err := registryHolder.refresh(ctx); err != nil {
		log.Fatalf("tari-exporter: failed to load initial node registry from Postgres: %v", err)
	}
	log.Printf("tari-exporter: loaded %d nodes from Postgres", len(registryHolder.get()))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runRegistryRefreshLoop(ctx, registryHolder, *registryRefreshInterval)
	}()
	go func() {
		defer wg.Done()
		runPollLoop(ctx, registryHolder, *pollInterval)
	}()

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{Addr: *listen, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("tari-exporter: listening on %s (poll-interval=%s, registry-refresh-interval=%s)", *listen, *pollInterval, *registryRefreshInterval)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("tari-exporter: server error: %v", err)
	}

	wg.Wait()
}

// registryHolder holds the most recently fetched node list behind a mutex so the poll
// loop and the registry-refresh loop can run independently: the exporter's node set can
// grow/shrink at runtime as Postgres changes, without restarting the process.
type registryHolder struct {
	store *registry.Store

	mu    sync.RWMutex
	nodes []registry.Node
}

func newRegistryHolder(store *registry.Store) *registryHolder {
	return &registryHolder{store: store}
}

func (h *registryHolder) refresh(ctx context.Context) error {
	nodes, err := h.store.List(ctx)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.nodes = nodes
	h.mu.Unlock()
	return nil
}

func (h *registryHolder) get() []registry.Node {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]registry.Node, len(h.nodes))
	copy(out, h.nodes)
	return out
}

// runRegistryRefreshLoop re-fetches the node list from Postgres on every tick of
// interval until ctx is cancelled. A failed refresh is logged and the previous node
// list is kept — a transient DB hiccup should not blank out what the exporter polls.
func runRegistryRefreshLoop(ctx context.Context, holder *registryHolder, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := holder.refresh(ctx); err != nil {
				log.Printf("tari-exporter: registry refresh from Postgres failed, keeping previous node list: %v", err)
			}
		}
	}
}

// runPollLoop polls every node once immediately, then again on every tick of interval,
// until ctx is cancelled. It fetches the current node list from holder on every cycle
// so a node added/removed in Postgres between refreshes of holder is still picked up
// promptly without restarting the exporter.
func runPollLoop(ctx context.Context, holder *registryHolder, interval time.Duration) {
	poll := func() {
		nodes := holder.get()
		results := collector.PollAll(ctx, nodes)
		recordMetrics(results)
	}

	poll()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

// recordMetrics updates every Prometheus metric from one completed poll cycle. It
// treats an unreachable node as a metric value (tari_node_up=0), never as a reason to
// skip that node's series entirely — an alerting rule needs the series to exist to
// alert on it going to 0.
func recordMetrics(results []collector.Result) {
	var maxHeight uint64
	haveMaxHeight := false
	for _, r := range results {
		if r.Up && r.Height > maxHeight {
			maxHeight = r.Height
			haveMaxHeight = true
		}
	}

	now := float64(time.Now().Unix())

	for _, r := range results {
		labels := prometheus.Labels{"node_name": r.Node.Name, "tier": r.Node.Tier, "ip": r.Node.IP}

		if r.Up {
			nodeUp.With(labels).Set(1)
			nodeLastScrapeSuccess.With(labels).Set(now)
		} else {
			nodeUp.With(labels).Set(0)
			if r.Err != nil {
				log.Printf("tari-exporter: node %s (%s) unreachable: %v", r.Node.Name, r.Node.Addr(), r.Err)
			}
		}

		nodeHeight.With(labels).Set(float64(r.Height))
		nodePeerCount.With(labels).Set(float64(r.PeerCount))
		nodeMempoolSize.With(labels).Set(float64(r.MempoolSize))

		if r.Up && haveMaxHeight {
			nodeSyncLag.With(labels).Set(float64(maxHeight - r.Height))
		}

		if r.DifficultyOK {
			nodeNetworkDifficulty.With(labels).Set(float64(r.NetworkDifficulty))
		}
		if r.HashRateOK {
			nodeHashRate.With(prometheus.Labels{"node_name": r.Node.Name, "tier": r.Node.Tier, "ip": r.Node.IP, "algo": "sha3x"}).Set(float64(r.Sha3xHashRate))
			nodeHashRate.With(prometheus.Labels{"node_name": r.Node.Name, "tier": r.Node.Tier, "ip": r.Node.IP, "algo": "monero_randomx"}).Set(float64(r.MoneroRandomXHashRate))
			nodeHashRate.With(prometheus.Labels{"node_name": r.Node.Name, "tier": r.Node.Tier, "ip": r.Node.IP, "algo": "tari_randomx"}).Set(float64(r.TariRandomXHashRate))
		}
		// r.VersionOK / r.Version is not currently exposed as a metric: Prometheus
		// labels are meant for low-cardinality, near-static dimensions, and a
		// version string changing across a fleet mid-rollout would churn label
		// sets. Surfacing it cleanly (e.g. an info-style gauge) is left as a
		// follow-up rather than bolted on here without a real usage pattern.
	}
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
