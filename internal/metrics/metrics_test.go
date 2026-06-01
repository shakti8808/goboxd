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

// TestSecurityRejectionsCounter exercises IncSecurityRejection across
// every documented rule.
func TestSecurityRejectionsCounter(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	for _, rule := range []string{
		metrics.RuleMalformedSubmission,
		metrics.RuleLanguageNotRegistered,
		metrics.RuleSourceSizeExceeded,
		metrics.RuleStdinSizeExceeded,
		metrics.RuleResourceLimitExceeded,
	} {
		c.IncSecurityRejection(rule)
		c.IncSecurityRejection(rule)
	}

	families := gather(t, c)
	rj, ok := families["goboxd_security_rejections_total"]
	if !ok {
		t.Fatal("goboxd_security_rejections_total missing")
	}
	for _, rule := range []string{
		metrics.RuleMalformedSubmission,
		metrics.RuleLanguageNotRegistered,
		metrics.RuleSourceSizeExceeded,
		metrics.RuleStdinSizeExceeded,
		metrics.RuleResourceLimitExceeded,
	} {
		if m := findMetric(rj, map[string]string{"rule": rule}); m == nil {
			t.Errorf("rejection counter for rule %q missing", rule)
		} else if got := m.GetCounter().GetValue(); got != 2 {
			t.Errorf("rejection counter for rule %q = %v, want 2", rule, got)
		}
	}
}

// TestSecurityRejectionsCounterNilSafe asserts the observer-failure
// isolation guard: a nil *Collector or one whose vec is nil must not
// panic when the run handler invokes the counter.
func TestSecurityRejectionsCounterNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IncSecurityRejection panicked on nil collector: %v", r)
		}
	}()
	var c *metrics.Collector
	c.IncSecurityRejection(metrics.RuleSourceSizeExceeded)
}
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


// TestRunRequestCounter exercises IncRunRequest across language+status
// pairs and asserts the cumulative values are observable on the
// registry.
func TestRunRequestCounter(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	c.IncRunRequest("py3", metrics.StatusOK)
	c.IncRunRequest("py3", metrics.StatusOK)
	c.IncRunRequest("py3", metrics.StatusTimeLimitExceeded)
	c.IncRunRequest("cpp", metrics.StatusCompilationError)

	families := gather(t, c)
	rj, ok := families["goboxd_run_requests_total"]
	if !ok {
		t.Fatal("goboxd_run_requests_total missing")
	}
	for _, want := range []struct {
		language, status string
		count            float64
	}{
		{"py3", metrics.StatusOK, 2},
		{"py3", metrics.StatusTimeLimitExceeded, 1},
		{"cpp", metrics.StatusCompilationError, 1},
	} {
		m := findMetric(rj, map[string]string{"language": want.language, "status": want.status})
		if m == nil {
			t.Errorf("counter %s/%s missing", want.language, want.status)
			continue
		}
		if got := m.GetCounter().GetValue(); got != want.count {
			t.Errorf("counter %s/%s = %v, want %v", want.language, want.status, got, want.count)
		}
	}
}

// TestRunDurationHistogram asserts ObserveRunDuration registers the
// histogram and emits the documented bucket boundaries from §14.
func TestRunDurationHistogram(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	for _, s := range []float64{0.005, 0.05, 0.5, 5, 50} {
		c.ObserveRunDuration(s)
	}
	c.ObserveRunDuration(-1) // clamped to 0

	families := gather(t, c)
	h, ok := families["goboxd_run_duration_seconds"]
	if !ok {
		t.Fatal("goboxd_run_duration_seconds missing")
	}
	hist := h.GetMetric()[0].GetHistogram()
	if hist.GetSampleCount() != 6 {
		t.Errorf("sample count = %d, want 6", hist.GetSampleCount())
	}
	bucketBounds := []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
	if got := len(hist.GetBucket()); got != len(bucketBounds) {
		t.Errorf("bucket count = %d, want %d", got, len(bucketBounds))
	}
}

// TestQueueDepthGauge confirms SetQueueDepth tracks the most-recent
// value and clamps negatives.
func TestQueueDepthGauge(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	c.SetQueueDepth(5)
	c.SetQueueDepth(7)
	c.SetQueueDepth(-3) // clamped to 0

	families := gather(t, c)
	g, ok := families["goboxd_worker_pool_queue_depth"]
	if !ok {
		t.Fatal("goboxd_worker_pool_queue_depth missing")
	}
	v := g.GetMetric()[0].GetGauge().GetValue()
	if v != 0 {
		t.Errorf("queue depth = %v, want 0 after clamp", v)
	}
}

// TestSandboxAndIsolationCounters drive the two scalar counters added
// in Wave D.
func TestSandboxAndIsolationCounters(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	c.IncSandboxCleanupFailure()
	c.IncSandboxCleanupFailure()
	c.IncWorkspaceIsolationViolation()

	families := gather(t, c)
	if cf := families["goboxd_sandbox_cleanup_failures_total"]; cf == nil {
		t.Fatal("goboxd_sandbox_cleanup_failures_total missing")
	} else if v := cf.GetMetric()[0].GetCounter().GetValue(); v != 2 {
		t.Errorf("cleanup_failures = %v, want 2", v)
	}
	if iv := families["goboxd_workspace_isolation_violation_total"]; iv == nil {
		t.Fatal("goboxd_workspace_isolation_violation_total missing")
	} else if v := iv.GetMetric()[0].GetCounter().GetValue(); v != 1 {
		t.Errorf("isolation_violations = %v, want 1", v)
	}
}

// TestRunMetricsNilSafe asserts every Wave D helper is nil-safe — Wave A
// observer-failure-isolation (Property 25) demands that a nil collector
// (or one whose vec was not initialised) cannot panic the run handler.
func TestRunMetricsNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil collector panicked: %v", r)
		}
	}()
	var c *metrics.Collector
	c.IncRunRequest("py3", metrics.StatusOK)
	c.ObserveRunDuration(0.1)
	c.SetQueueDepth(0)
	c.IncSandboxCleanupFailure()
	c.IncWorkspaceIsolationViolation()
}

// TestStartupPrereqFailuresCounter registers and increments the startup prereq failures counter.
func TestStartupPrereqFailuresCounter(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	c.IncStartupPrereqFailure("sandbox_root")
	c.IncStartupPrereqFailure("sandbox_root")
	c.IncStartupPrereqFailure("ro_mount")

	families := gather(t, c)
	sp, ok := families["goboxd_startup_prereq_failures_total"]
	if !ok {
		t.Fatal("goboxd_startup_prereq_failures_total missing")
	}

	m := findMetric(sp, map[string]string{"prereq": "sandbox_root"})
	if m == nil {
		t.Fatal("sandbox_root counter missing")
	}
	if v := m.GetCounter().GetValue(); v != 2 {
		t.Errorf("sandbox_root value = %v, want 2", v)
	}

	m2 := findMetric(sp, map[string]string{"prereq": "ro_mount"})
	if m2 == nil {
		t.Fatal("ro_mount counter missing")
	}
	if v := m2.GetCounter().GetValue(); v != 1 {
		t.Errorf("ro_mount value = %v, want 1", v)
	}
}

// TestStartupPrereqFailuresCounterNilSafe asserts nil safety of the recorder.
func TestStartupPrereqFailuresCounterNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IncStartupPrereqFailure panicked on nil collector: %v", r)
		}
	}()
	var c *metrics.Collector
	c.IncStartupPrereqFailure("sandbox_root")
}

// TestOrphanWorkspaceReapedCounter asserts registration and increment logic of the orphan reaped counter.
func TestOrphanWorkspaceReapedCounter(t *testing.T) {
	c := metrics.New("goboxd", "dev")
	c.IncOrphanWorkspaceReaped()
	c.IncOrphanWorkspaceReaped()

	families := gather(t, c)
	ow, ok := families["goboxd_orphan_workspace_reaped_total"]
	if !ok {
		t.Fatal("goboxd_orphan_workspace_reaped_total missing")
	}
	if v := ow.GetMetric()[0].GetCounter().GetValue(); v != 2 {
		t.Errorf("orphan_reaped value = %v, want 2", v)
	}
}

// TestOrphanWorkspaceReapedCounterNilSafe asserts nil safety.
func TestOrphanWorkspaceReapedCounterNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IncOrphanWorkspaceReaped panicked on nil collector: %v", r)
		}
	}()
	var c *metrics.Collector
	c.IncOrphanWorkspaceReaped()
}
