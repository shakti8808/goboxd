package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/thesouldev/goboxd/internal/metrics"
)

// gather collects every metric family from the given collector, indexed
// by metric name for cheap lookup in assertions.
func gather(t *testing.T, c *metrics.Collector) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := c.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		out[f.GetName()] = f
	}
	return out
}

// findMetric returns the *dto.Metric whose label values match labels in
// order. It is the user's responsibility to pass labels in the order
// they were registered (we sort them via the family's metric labels).
func findMetric(family *dto.MetricFamily, labels map[string]string) *dto.Metric {
	for _, m := range family.GetMetric() {
		match := true
		for _, lp := range m.GetLabel() {
			if want, ok := labels[lp.GetName()]; ok && lp.GetValue() != want {
				match = false
				break
			}
		}
		if match {
			return m
		}
	}
	return nil
}

// TestBuildInfoRegistered asserts goboxd_build_info{service,version} is
// observable from the moment the collector is created (REQ A-12.7,
// architecture §14 row goboxd_build_info).
func TestBuildInfoRegistered(t *testing.T) {
	c := metrics.New("goboxd", "v1.2.3")
	families := gather(t, c)

	bi, ok := families["goboxd_build_info"]
	if !ok {
		t.Fatal("goboxd_build_info not registered")
	}
	if bi.GetType() != dto.MetricType_GAUGE {
		t.Errorf("goboxd_build_info type = %v, want GAUGE", bi.GetType())
	}
	m := findMetric(bi, map[string]string{"service": "goboxd", "version": "v1.2.3"})
	if m == nil {
		t.Fatalf("goboxd_build_info missing labelled sample: %v", bi.GetMetric())
	}
	if m.GetGauge().GetValue() != 1 {
		t.Errorf("goboxd_build_info value = %v, want 1", m.GetGauge().GetValue())
	}
}

// TestDroppedCountersRegistered asserts the two counters are present and
// increment correctly.
func TestDroppedCountersRegistered(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	c.IncDroppedLog(metrics.ReasonLogSerializationFailed)
	c.IncDroppedLog(metrics.ReasonLogSerializationFailed)
	c.IncDroppedMetric(metrics.ReasonMetricRegistryUnavailable)

	families := gather(t, c)

	logs, ok := families["goboxd_dropped_logs_total"]
	if !ok {
		t.Fatal("goboxd_dropped_logs_total missing")
	}
	if m := findMetric(logs, map[string]string{"reason": metrics.ReasonLogSerializationFailed}); m == nil {
		t.Fatalf("dropped_logs counter for reason %q missing", metrics.ReasonLogSerializationFailed)
	} else if got := m.GetCounter().GetValue(); got != 2 {
		t.Errorf("dropped_logs counter = %v, want 2", got)
	}

	metricsFam, ok := families["goboxd_dropped_metrics_total"]
	if !ok {
		t.Fatal("goboxd_dropped_metrics_total missing")
	}
	if m := findMetric(metricsFam, map[string]string{"reason": metrics.ReasonMetricRegistryUnavailable}); m == nil {
		t.Fatal("dropped_metrics counter for registry_unavailable missing")
	} else if got := m.GetCounter().GetValue(); got != 1 {
		t.Errorf("dropped_metrics counter = %v, want 1", got)
	}
}

// TestExposeFormat verifies the /metrics endpoint returns Prometheus text
// (or OpenMetrics, when the client negotiates it) carrying the build
// info line. The architecture spec only mandates Prometheus text format;
// promhttp may upgrade when the Accept header asks for it, but the body
// always carries goboxd_build_info either way.
func TestExposeFormat(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	srv := httptest.NewServer(c.Handler())
	defer srv.Close()

	// No Accept header → promhttp returns plain Prometheus text.
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain*", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `goboxd_build_info{service="goboxd",version="dev"} 1`) {
		t.Errorf("/metrics body missing build_info line:\n%s", body)
	}
}
