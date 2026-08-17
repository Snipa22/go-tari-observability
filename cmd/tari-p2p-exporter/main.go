// Command tari-p2p-exporter is a Prometheus exporter that periodically probes the P2P
// layer (Noise_XX handshake + identity exchange) of a configured registry of Tari nodes
// and exposes reachability/latency/identity data as Prometheus metrics.
//
// This is deliberately a separate binary/process from cmd/tari-exporter (the existing
// gRPC application-layer exporter), not a shared process — per Alex's explicit call,
// isolating these two probes gives each an independent blast radius and independent
// restart/deploy lifecycle. It reads the SAME Postgres node registry as tari-exporter,
// just probes a different port (P2PPort, historically 18189 in this ecosystem) with a
// different protocol (Noise_XX + Tari's comms identity-exchange, not gRPC).
//
// This is an early/basic P2P integration: connect + identity exchange only. It does not
// implement RPC-over-P2P or deeper P2P stats — see README.md for what's out of scope for
// now.
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

	"github.com/Snipa22/go-tari-observability/internal/p2pcollector"
	"github.com/Snipa22/go-tari-observability/internal/registry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	p2pReachable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_p2p_reachable",
		Help: "1 if the node's P2P endpoint completed a Noise_XX handshake and identity exchange, 0 otherwise.",
	}, []string{"node_name", "tier", "ip"})

	p2pHandshakeLatency = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_p2p_handshake_latency_seconds",
		Help: "Wall-clock time for the full P2P probe (dial + Noise_XX handshake + identity exchange) against a reachable node.",
	}, []string{"node_name", "tier", "ip"})

	// p2pInfo is the standard Prometheus "info" pattern for near-static string data
	// that shouldn't be a metric value itself: always set to 1, with user_agent and
	// pubkey_prefix carried as labels. pubkey_prefix is only the first 8 hex chars of
	// the peer's recovered static public key — enough to spot-correlate a peer across
	// scrapes without putting an unbounded/high-entropy value (the full pubkey) into
	// a Prometheus label. The full pubkey is available via Result.PubKeyHex in logs
	// if ever needed, not as a metric label.
	p2pInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_p2p_info",
		Help: "Always 1. Carries the node's reported P2P user_agent and the first 8 hex chars of its recovered static pubkey (pubkey_prefix) as labels. Absent for a node/cycle where the P2P probe didn't succeed.",
	}, []string{"node_name", "tier", "ip", "user_agent", "pubkey_prefix"})

	// p2pPeerFeatures is the raw PeerIdentityMsg.features bitmask as reported by the
	// peer. Bit-meaning is NOT yet decoded/known by go-tari-lib's p2p package or this
	// exporter -- this exposes the raw value as-is for now, pending that being
	// documented upstream.
	p2pPeerFeatures = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_p2p_peer_features",
		Help: "Raw features bitmask reported by the peer in its PeerIdentityMsg. Bit-meaning is not yet decoded/known -- this is the undecoded raw value.",
	}, []string{"node_name", "tier", "ip"})

	p2pAdvertisedAddressCount = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_p2p_advertised_address_count",
		Help: "Number of addresses the peer advertised in its PeerIdentityMsg.",
	}, []string{"node_name", "tier", "ip"})

	p2pIdentitySignaturePresent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tari_p2p_identity_signature_present",
		Help: "1 if the peer included an identity_signature in its PeerIdentityMsg, 0 otherwise.",
	}, []string{"node_name", "tier", "ip"})
)

func init() {
	prometheus.MustRegister(
		p2pReachable,
		p2pHandshakeLatency,
		p2pInfo,
		p2pPeerFeatures,
		p2pAdvertisedAddressCount,
		p2pIdentitySignaturePresent,
	)
}

func main() {
	dsn := flag.String("dsn", envOr("TARI_OBSERVABILITY_DSN", ""), "Postgres connection string for the node registry (or set TARI_OBSERVABILITY_DSN)")
	listen := flag.String("listen", envOr("TARI_P2P_EXPORTER_LISTEN", ":9470"), "address to listen on for /metrics and /healthz")
	pollInterval := flag.Duration("poll-interval", envDurationOr("TARI_P2P_EXPORTER_POLL_INTERVAL", 60*time.Second), "how often to probe every node's P2P layer; P2P handshakes are heavier than a gRPC call, so this defaults slower than tari-exporter")
	registryRefreshInterval := flag.Duration("registry-refresh-interval", envDurationOr("TARI_P2P_EXPORTER_REGISTRY_REFRESH_INTERVAL", 60*time.Second), "how often to re-fetch the node list from Postgres")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("tari-p2p-exporter: --dsn (or TARI_OBSERVABILITY_DSN) is required")
	}

	store, err := registry.NewStore(*dsn)
	if err != nil {
		log.Fatalf("tari-p2p-exporter: failed to connect to node registry database: %v", err)
	}
	defer store.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	registryHolder := newRegistryHolder(store)
	if err := registryHolder.refresh(ctx); err != nil {
		log.Fatalf("tari-p2p-exporter: failed to load initial node registry from Postgres: %v", err)
	}
	log.Printf("tari-p2p-exporter: loaded %d nodes from Postgres", len(registryHolder.get()))

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

	log.Printf("tari-p2p-exporter: listening on %s (poll-interval=%s, registry-refresh-interval=%s)", *listen, *pollInterval, *registryRefreshInterval)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("tari-p2p-exporter: server error: %v", err)
	}

	wg.Wait()
}

// registryHolder holds the most recently fetched node list behind a mutex so the poll
// loop and the registry-refresh loop can run independently. Same pattern as
// cmd/tari-exporter.
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
// list is kept.
func runRegistryRefreshLoop(ctx context.Context, holder *registryHolder, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := holder.refresh(ctx); err != nil {
				log.Printf("tari-p2p-exporter: registry refresh from Postgres failed, keeping previous node list: %v", err)
			}
		}
	}
}

// runPollLoop probes every node's P2P layer once immediately, then again on every tick
// of interval, until ctx is cancelled. It fetches the current node list from holder on
// every cycle so registry changes are picked up without restarting the exporter.
func runPollLoop(ctx context.Context, holder *registryHolder, interval time.Duration) {
	poll := func() {
		nodes := holder.get()
		results := p2pcollector.PollAll(ctx, nodes)
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

// recordMetrics updates every Prometheus metric from one completed poll cycle. An
// unreachable/unresponsive peer is recorded as tari_p2p_reachable=0, never a reason to
// skip that node's series or crash the exporter.
func recordMetrics(results []p2pcollector.Result) {
	for _, r := range results {
		labels := prometheus.Labels{"node_name": r.Node.Name, "tier": r.Node.Tier, "ip": r.Node.IP}

		if r.Reachable {
			p2pReachable.With(labels).Set(1)
			p2pHandshakeLatency.With(labels).Set(r.HandshakeLatency.Seconds())
		} else {
			p2pReachable.With(labels).Set(0)
			if r.Err != nil {
				log.Printf("tari-p2p-exporter: node %s (%s:%d) P2P probe failed: %v", r.Node.Name, r.Node.IP, r.Node.P2PPort, r.Err)
			}
		}

		if r.Reachable {
			pubkeyPrefix := r.PubKeyHex
			if len(pubkeyPrefix) > 8 {
				pubkeyPrefix = pubkeyPrefix[:8]
			}
			p2pInfo.With(prometheus.Labels{
				"node_name":     r.Node.Name,
				"tier":          r.Node.Tier,
				"ip":            r.Node.IP,
				"user_agent":    r.UserAgent,
				"pubkey_prefix": pubkeyPrefix,
			}).Set(1)

			p2pPeerFeatures.With(labels).Set(float64(r.Features))
			p2pAdvertisedAddressCount.With(labels).Set(float64(r.AddressCount))

			sigPresent := 0.0
			if r.HasIdentitySignature {
				sigPresent = 1.0
			}
			p2pIdentitySignaturePresent.With(labels).Set(sigPresent)
		}
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
