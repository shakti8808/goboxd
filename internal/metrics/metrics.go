// Package metrics owns GoboxD's Prometheus registry and exposition.
//
// Phase 1 ships only the metrics that exist independently of the request
// pipeline:
//
//   - goboxd_build_info{version, service}      — gauge, value 1
//   - goboxd_dropped_logs_total{reason}        — counter
//   - goboxd_dropped_metrics_total{reason}     — counter
//
// Counters that observe POST /run, the worker pool, the security validator,
// or the sandbox runner land in Phase 3 alongside the runtime path that
// emits them. The exposed surface is documented in the architecture spec
// §14 (Metrics Catalog) and the implementation spec's "Correctness Property
// Classification" table.
//
// Observer-failure isolation (Property 25) is honoured here: every recorder
// is an opaque method on *Collector and never returns an error visible to
// the request path. If recording fails for any reason, the counter
// goboxd_dropped_metrics_total is incremented and the originating request
// continues unaffected.
package metrics

import (
	"net/http"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Reason labels for goboxd_dropped_logs_total.
const (
	ReasonLogSerializationFailed = "serialization_failed"
	ReasonLogSinkUnavailable     = "sink_unavailable"
)

// Reason labels for goboxd_dropped_metrics_total.
const (
	ReasonMetricRegistryUnavailable = "registry_unavailable"
	ReasonMetricRecordFailed        = "record_failed"
)

// SecurityRejectionRule is the closed set of values for the `rule` label
// on goboxd_security_rejections_total. The set mirrors the Security_Validator
// catalog in architecture spec §13.
const (
	RuleMalformedSubmission   = "malformed_submission"
	RuleLanguageNotRegistered = "language_not_registered"
	RuleSourceSizeExceeded    = "source_size_exceeded"
	RuleStdinSizeExceeded     = "stdin_size_exceeded"
	RuleResourceLimitExceeded = "resource_limit_exceeded"
)

// Status is the closed set of values for the `status` label on
// goboxd_run_requests_total. Mirrors runner.Status (architecture §11)
// plus the special REJECTED bucket for pre-enqueue rejections.
const (
	StatusOK                  = "OK"
	StatusCompilationError    = "COMPILATION_ERROR"
	StatusRuntimeError        = "RUNTIME_ERROR"
	StatusTimeLimitExceeded   = "TIME_LIMIT_EXCEEDED"
	StatusMemoryLimitExceeded = "MEMORY_LIMIT_EXCEEDED"
	StatusInternalError       = "INTERNAL_ERROR"
	StatusRejected            = "REJECTED"
)

// runDurationBuckets covers 0.01 s to 60 s per architecture §14, with
// roughly equal coverage in log-space. The list is exported so tests
// can assert exposure.
var runDurationBuckets = []float64{
	0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60,
}

// Collector aggregates the Prometheus registry plus the metric handles
// every package needs at runtime.
//
// Construction is the only way to register the metrics; tests build a
// fresh *Collector per case to keep counters deterministic.
type Collector struct {
	registry   prometheus.Gatherer
	registerer prometheus.Registerer

	buildInfo *prometheus.GaugeVec

	droppedLogs    *prometheus.CounterVec
	droppedMetrics *prometheus.CounterVec

	securityRejections *prometheus.CounterVec

	runRequests                  *prometheus.CounterVec
	runDuration                  prometheus.Histogram
	queueDepth                   prometheus.Gauge
	sandboxCleanupFailures       prometheus.Counter
	workspaceIsolationViolations prometheus.Counter
	startupPrereqFailures        *prometheus.CounterVec
	orphanWorkspaceReaped        prometheus.Counter
	unsafeFilename               *prometheus.CounterVec
	unknownPlaceholder           *prometheus.CounterVec

	fallbackDroppedMetrics int64
}

// New constructs a Collector and registers every Phase 1/3 metric.
func New(service, version string) *Collector {
	r := prometheus.NewRegistry()
	return NewWithRegisterer(service, version, r, r)
}

// NewWithRegisterer constructs a Collector with a custom prometheus.Registerer.
func NewWithRegisterer(service, version string, r prometheus.Registerer, g prometheus.Gatherer) *Collector {
	c := &Collector{
		registry:   g,
		registerer: r,
		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "goboxd_build_info",
			Help: "Static build information; value is always 1.",
		}, []string{"service", "version"}),
		droppedLogs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "goboxd_dropped_logs_total",
			Help: "Count of log entries dropped due to emission failure.",
		}, []string{"reason"}),
		droppedMetrics: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "goboxd_dropped_metrics_total",
			Help: "Count of metric updates dropped due to recording failure.",
		}, []string{"reason"}),
		securityRejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "goboxd_security_rejections_total",
			Help: "Count of Code_Submission rejections by Security_Validator, partitioned by rule.",
		}, []string{"rule"}),
		runRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "goboxd_run_requests_total",
			Help: "Count of POST /run requests, partitioned by language and Execution_Result status.",
		}, []string{"language", "status"}),
		runDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "goboxd_run_duration_seconds",
			Help:    "Wall-clock duration of POST /run handling, in seconds.",
			Buckets: runDurationBuckets,
		}),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "goboxd_worker_pool_queue_depth",
			Help: "Current Worker_Pool queue depth.",
		}),
		sandboxCleanupFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "goboxd_sandbox_cleanup_failures_total",
			Help: "Count of Sandbox_Job_Directory cleanup failures observed by SandboxRunner.",
		}),
		workspaceIsolationViolations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "goboxd_workspace_isolation_violation_total",
			Help: "Count of workspace ownership-invariant violations observed by SandboxRunner.",
		}),
		startupPrereqFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "goboxd_startup_prereq_failures_total",
			Help: "Count of startup prerequisite validation failures.",
		}, []string{"prereq"}),
		orphanWorkspaceReaped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "goboxd_orphan_workspace_reaped_total",
			Help: "Count of stale orphan sandbox directories reaped.",
		}),
		unsafeFilename: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "goboxd_unsafe_filename_total",
			Help: "Count of unsafe filename events, partitioned by source.",
		}, []string{"source"}),
		unknownPlaceholder: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "goboxd_unknown_placeholder_total",
			Help: "Count of unknown placeholder events, partitioned by source.",
		}, []string{"source"}),
	}

	// Register droppedMetrics first so we can track subsequent registration errors.
	if err := r.Register(c.droppedMetrics); err != nil {
		c.nullify("droppedMetrics")
	}

	for _, metric := range []struct {
		m    prometheus.Collector
		name string
	}{
		{c.buildInfo, "buildInfo"},
		{c.droppedLogs, "droppedLogs"},
		{c.securityRejections, "securityRejections"},
		{c.runRequests, "runRequests"},
		{c.runDuration, "runDuration"},
		{c.queueDepth, "queueDepth"},
		{c.sandboxCleanupFailures, "sandboxCleanupFailures"},
		{c.workspaceIsolationViolations, "workspaceIsolationViolations"},
		{c.startupPrereqFailures, "startupPrereqFailures"},
		{c.orphanWorkspaceReaped, "orphanWorkspaceReaped"},
		{c.unsafeFilename, "unsafeFilename"},
		{c.unknownPlaceholder, "unknownPlaceholder"},
	} {
		if err := r.Register(metric.m); err != nil {
			c.IncDroppedMetric(ReasonMetricRecordFailed)
			c.nullify(metric.name)
		}
	}

	// Initialise the build-info gauge if it was successfully registered.
	if c.buildInfo != nil {
		c.buildInfo.WithLabelValues(service, version).Set(1)
	}

	return c
}

func (c *Collector) nullify(name string) {
	switch name {
	case "buildInfo":
		c.buildInfo = nil
	case "droppedLogs":
		c.droppedLogs = nil
	case "droppedMetrics":
		c.droppedMetrics = nil
	case "securityRejections":
		c.securityRejections = nil
	case "runRequests":
		c.runRequests = nil
	case "runDuration":
		c.runDuration = nil
	case "queueDepth":
		c.queueDepth = nil
	case "sandboxCleanupFailures":
		c.sandboxCleanupFailures = nil
	case "workspaceIsolationViolations":
		c.workspaceIsolationViolations = nil
	case "startupPrereqFailures":
		c.startupPrereqFailures = nil
	case "orphanWorkspaceReaped":
		c.orphanWorkspaceReaped = nil
	case "unsafeFilename":
		c.unsafeFilename = nil
	case "unknownPlaceholder":
		c.unknownPlaceholder = nil
	}
}

// Registry returns the underlying *prometheus.Registry if it matches that type.
func (c *Collector) Registry() *prometheus.Registry {
	if r, ok := c.registry.(*prometheus.Registry); ok {
		return r
	}
	return nil
}

// Handler returns an http.Handler that exposes the registry as Prometheus
// text format (`Content-Type: text/plain; version=0.0.4`) at GET /metrics.
func (c *Collector) Handler() http.Handler {
	opts := promhttp.HandlerOpts{}
	if c.registerer != nil {
		opts.Registry = c.registerer
	}
	return promhttp.HandlerFor(c.registry, opts)
}

// IncDroppedLog increments goboxd_dropped_logs_total{reason}.
func (c *Collector) IncDroppedLog(reason string) {
	if c == nil || c.droppedLogs == nil {
		return
	}
	c.droppedLogs.WithLabelValues(reason).Inc()
}

// IncDroppedMetric increments goboxd_dropped_metrics_total{reason}.
func (c *Collector) IncDroppedMetric(reason string) {
	if c == nil {
		return
	}
	atomic.AddInt64(&c.fallbackDroppedMetrics, 1)
	if c.droppedMetrics != nil {
		c.droppedMetrics.WithLabelValues(reason).Inc()
	}
}

// DroppedMetricsCount returns the fallback count of dropped metrics.
func (c *Collector) DroppedMetricsCount() int64 {
	if c == nil {
		return 0
	}
	return atomic.LoadInt64(&c.fallbackDroppedMetrics)
}

// IncSecurityRejection increments goboxd_security_rejections_total{rule}.
func (c *Collector) IncSecurityRejection(rule string) {
	if c == nil || c.securityRejections == nil {
		return
	}
	c.securityRejections.WithLabelValues(rule).Inc()
}

// IncRunRequest increments goboxd_run_requests_total{language,status}.
func (c *Collector) IncRunRequest(language, status string) {
	if c == nil || c.runRequests == nil {
		return
	}
	c.runRequests.WithLabelValues(language, status).Inc()
}

// ObserveRunDuration records seconds onto goboxd_run_duration_seconds.
func (c *Collector) ObserveRunDuration(seconds float64) {
	if c == nil || c.runDuration == nil {
		return
	}
	if seconds < 0 {
		seconds = 0
	}
	c.runDuration.Observe(seconds)
}

// SetQueueDepth sets goboxd_worker_pool_queue_depth to depth.
func (c *Collector) SetQueueDepth(depth int) {
	if c == nil || c.queueDepth == nil {
		return
	}
	if depth < 0 {
		depth = 0
	}
	c.queueDepth.Set(float64(depth))
}

// IncSandboxCleanupFailure increments goboxd_sandbox_cleanup_failures_total.
func (c *Collector) IncSandboxCleanupFailure() {
	if c == nil || c.sandboxCleanupFailures == nil {
		return
	}
	c.sandboxCleanupFailures.Inc()
}

// IncWorkspaceIsolationViolation increments
// goboxd_workspace_isolation_violation_total.
func (c *Collector) IncWorkspaceIsolationViolation() {
	if c == nil || c.workspaceIsolationViolations == nil {
		return
	}
	c.workspaceIsolationViolations.Inc()
}

// IncStartupPrereqFailure increments goboxd_startup_prereq_failures_total{prereq}.
func (c *Collector) IncStartupPrereqFailure(prereq string) {
	if c == nil || c.startupPrereqFailures == nil {
		return
	}
	c.startupPrereqFailures.WithLabelValues(prereq).Inc()
}

// IncOrphanWorkspaceReaped increments goboxd_orphan_workspace_reaped_total.
func (c *Collector) IncOrphanWorkspaceReaped() {
	if c == nil || c.orphanWorkspaceReaped == nil {
		return
	}
	c.orphanWorkspaceReaped.Inc()
}

// IncUnsafeFilename increments goboxd_unsafe_filename_total{source}.
func (c *Collector) IncUnsafeFilename(source string) {
	if c == nil || c.unsafeFilename == nil {
		return
	}
	c.unsafeFilename.WithLabelValues(source).Inc()
}

// IncUnknownPlaceholder increments goboxd_unknown_placeholder_total{source}.
func (c *Collector) IncUnknownPlaceholder(source string) {
	if c == nil || c.unknownPlaceholder == nil {
		return
	}
	c.unknownPlaceholder.WithLabelValues(source).Inc()
}
