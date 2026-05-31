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
	}

	r.MustRegister(c.buildInfo, c.droppedLogs, c.droppedMetrics)

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
