package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// snapshotRow is one line of the human-readable table printed by `tari-netstat
// snapshot`.
type snapshotRow struct {
	Name   string
	Tier   string
	Up     bool
	HaveUp bool
	Height float64
	HaveH  bool
	Peers  float64
	HaveP  bool
	Lag    float64
	HaveL  bool
}

func runSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	exporterURL := fs.String("exporter-url", "http://localhost:9469", "base URL of a running tari-exporter instance")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout for fetching /metrics")
	if err := fs.Parse(args); err != nil {
		return err
	}

	metricsURL := *exporterURL + "/metrics"

	client := &http.Client{Timeout: *timeout}
	resp, err := client.Get(metricsURL)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", metricsURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fetching %s: unexpected status %s: %s", metricsURL, resp.Status, string(body))
	}

	families, err := parseMetrics(resp.Body)
	if err != nil {
		return fmt.Errorf("parsing metrics from %s: %w", metricsURL, err)
	}

	rows := buildSnapshotRows(families)
	printSnapshotTable(rows)

	return nil
}

// parseMetrics decodes the Prometheus text exposition format using the real
// prometheus/common/expfmt library, rather than hand-rolling a parser.
func parseMetrics(r io.Reader) (map[string]*dto.MetricFamily, error) {
	var parser expfmt.TextParser
	return parser.TextToMetricFamilies(r)
}

// labelValue returns the value of label name on m, or "" if absent.
func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// buildSnapshotRows walks the parsed metric families and reassembles one row per
// node_name/tier pair, keyed by the node_name label shared across every
// tari_node_* series.
func buildSnapshotRows(families map[string]*dto.MetricFamily) []snapshotRow {
	byName := make(map[string]*snapshotRow)

	getRow := func(name, tier string) *snapshotRow {
		r, ok := byName[name]
		if !ok {
			r = &snapshotRow{Name: name, Tier: tier}
			byName[name] = r
		}
		return r
	}

	if fam, ok := families["tari_node_up"]; ok {
		for _, m := range fam.GetMetric() {
			name := labelValue(m, "node_name")
			r := getRow(name, labelValue(m, "tier"))
			r.Up = m.GetGauge().GetValue() == 1
			r.HaveUp = true
		}
	}
	if fam, ok := families["tari_node_height"]; ok {
		for _, m := range fam.GetMetric() {
			name := labelValue(m, "node_name")
			r := getRow(name, labelValue(m, "tier"))
			r.Height = m.GetGauge().GetValue()
			r.HaveH = true
		}
	}
	if fam, ok := families["tari_node_peer_count"]; ok {
		for _, m := range fam.GetMetric() {
			name := labelValue(m, "node_name")
			r := getRow(name, labelValue(m, "tier"))
			r.Peers = m.GetGauge().GetValue()
			r.HaveP = true
		}
	}
	if fam, ok := families["tari_node_sync_lag"]; ok {
		for _, m := range fam.GetMetric() {
			name := labelValue(m, "node_name")
			r := getRow(name, labelValue(m, "tier"))
			r.Lag = m.GetGauge().GetValue()
			r.HaveL = true
		}
	}

	rows := make([]snapshotRow, 0, len(byName))
	for _, r := range byName {
		rows = append(rows, *r)
	}
	return rows
}

func printSnapshotTable(rows []snapshotRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := tierRank(rows[i].Tier), tierRank(rows[j].Tier)
		if ri != rj {
			return ri < rj
		}
		return rows[i].Name < rows[j].Name
	})

	fmt.Printf("%-14s %-9s %-6s %-10s %-6s %s\n", "NAME", "TIER", "UP", "HEIGHT", "PEERS", "SYNC_LAG")
	for _, r := range rows {
		up := "?"
		if r.HaveUp {
			if r.Up {
				up = "UP"
			} else {
				up = "DOWN"
			}
		}
		height := fmtOrUnknown(r.Height, r.HaveH)
		peers := fmtOrUnknown(r.Peers, r.HaveP)
		lag := fmtOrUnknown(r.Lag, r.HaveL)
		fmt.Printf("%-14s %-9s %-6s %-10s %-6s %s\n", r.Name, r.Tier, up, height, peers, lag)
	}

	if len(rows) == 0 {
		fmt.Println("(no tari_node_* series found in exporter output)")
	}
}

func fmtOrUnknown(v float64, have bool) string {
	if !have {
		return "?"
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}
