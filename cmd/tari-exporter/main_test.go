package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Snipa22/go-tari-observability/internal/collector"
	"github.com/Snipa22/go-tari-observability/internal/registry"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"net/http/httptest"
)

// TestRecordMetrics_NodeInfoVersionLabel verifies that a poll result with a known
// version is exposed via the tari_node_info info-gauge with the version as a label,
// and that a result where the version wasn't obtained does not emit a stale/zero
// version series.
func TestRecordMetrics_NodeInfoVersionLabel(t *testing.T) {
	node := registry.Node{Name: "seed-02", Tier: "seed", IP: "51.210.222.91", GRPCPort: 18102}

	results := []collector.Result{
		{
			Node:      node,
			Up:        true,
			Version:   "1.2.3-integration-test",
			VersionOK: true,
			PolledAt:  time.Now(),
		},
	}

	recordMetrics(results)

	srv := httptest.NewServer(promhttp.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 1<<20)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	want := `tari_node_info{ip="51.210.222.91",node_name="seed-02",tier="seed",version="1.2.3-integration-test"} 1`
	if !strings.Contains(body, want) {
		t.Fatalf("expected metrics to contain %q, got:\n%s", want, body)
	}
}
