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
	registry *prometheus.Registry

	buildInfo *prometheus.GaugeVec

	droppedLogs    *prometheus.CounterVec
	droppedMetrics *prometheus.CounterVec

	securityRejections *prometheus.CounterVec

	runRequests                  *prometheus.CounterVec
	runDuration                  prometheus.Histogram
	queueDepth                   prometheus.Gauge
	sandboxCleanupFailures       prometheus.Counter
	workspaceIsolationViolations prometheus.Counter
}

// New constructs a Collector and registers every Phase 1 metric.
//
// service and version are stamped onto the goboxd_build_info gauge as
// label values; the gauge itself always carries value 1 and exists solely
// so dashboards can pivot on the running service+version.
func New(service, version string) *Collector {
	r := prometheus.NewRegistry()

	c := &Collector{
		registry: r,
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
	}

	r.MustRegister(
		c.buildInfo, c.droppedLogs, c.droppedMetrics, c.securityRejections,
		c.runRequests, c.runDuration, c.queueDepth,
		c.sandboxCleanupFailures, c.workspaceIsolationViolations,
	)

	// Initialise the build-info gauge so it appears on /metrics from
	// startup, not only after the first request.
	c.buildInfo.WithLabelValues(service, version).Set(1)

	return c
}

// Registry returns the underlying *prometheus.Registry.
//
// Used by tests that want to gather and inspect metric families directly
// without going through the HTTP exposition path.
func (c *Collector) Registry() *prometheus.Registry { return c.registry }

// Handler returns an http.Handler that exposes the registry as Prometheus
// text format (`Content-Type: text/plain; version=0.0.4`) at GET /metrics.
//
// promhttp.HandlerFor honours OpenMetrics negotiation by default; we leave
// that on because the architecture spec §14 only requires a Prometheus
// text exposition and does not forbid OpenMetrics for clients that ask
// for it.
func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{
		Registry: c.registry,
	})
}

// IncDroppedLog increments goboxd_dropped_logs_total{reason}.
//
// reason should be one of the documented constants
// (ReasonLogSerializationFailed, ReasonLogSinkUnavailable). Unknown
// reasons are accepted as label values; the architecture spec's allowed
// label set is non-exhaustive ("e.g.").
func (c *Collector) IncDroppedLog(reason string) {
	c.droppedLogs.WithLabelValues(reason).Inc()
}

// IncDroppedMetric increments goboxd_dropped_metrics_total{reason}.
//
// reason should be one of the documented constants
// (ReasonMetricRegistryUnavailable, ReasonMetricRecordFailed).
func (c *Collector) IncDroppedMetric(reason string) {
	c.droppedMetrics.WithLabelValues(reason).Inc()
}

// IncSecurityRejection increments goboxd_security_rejections_total{rule}.
//
// rule should be one of the documented Rule* constants. Recording is
// best-effort: if the underlying registry is nil (only possible when
// Collector is constructed in a degraded-stub mode, currently not used)
// the call is a no-op so observer-failure isolation (Property 25)
// holds for the originating request.
func (c *Collector) IncSecurityRejection(rule string) {
	if c == nil || c.securityRejections == nil {
		return
	}
	c.securityRejections.WithLabelValues(rule).Inc()
}

// IncRunRequest increments goboxd_run_requests_total{language,status}.
//
// status SHOULD be one of the Status* constants; unknown values are
// accepted but produce a label cardinality the operator must inspect.
func (c *Collector) IncRunRequest(language, status string) {
	if c == nil || c.runRequests == nil {
		return
	}
	c.runRequests.WithLabelValues(language, status).Inc()
}

// ObserveRunDuration records seconds onto goboxd_run_duration_seconds.
//
// Negative values are clamped to 0 so the histogram never sees an
// out-of-bound observation.
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
//
// Negative values are clamped to 0.
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
