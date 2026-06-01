package main

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/thesouldev/goboxd/internal/config"
	"github.com/thesouldev/goboxd/internal/metrics"
	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/security"
)

// validatorAdapter wraps security.Validate function to satisfy handlers.RunValidator.
type validatorAdapter struct{}

func (validatorAdapter) Validate(
	sub security.Submission,
	sizes security.SizeBounds,
	lookup security.LanguageDefinitionLookup,
	ceilings security.CeilingsLookup,
) error {
	return security.Validate(sub, sizes, lookup, ceilings)
}

// validatorCeilingsAdapter adapts config.Config to security.CeilingsLookup.
type validatorCeilingsAdapter struct {
	cfg *config.Config
}

func (a validatorCeilingsAdapter) CeilingsFor(language string) security.ResourceCeilings {
	c := a.cfg.CeilingsFor(language)
	return security.ResourceCeilings{
		WallTimeS:    c.WallTimeS,
		CPUTimeS:     c.CPUTimeS,
		MemoryMB:     c.MemoryMB,
		ProcessCount: c.ProcessCount,
		OutputSizeMB: c.OutputSizeMB,
	}
}

// sandboxObserver implements runner.SandboxObserver.
type sandboxObserver struct {
	logger  *slog.Logger
	metrics *metrics.Collector
}

func (o sandboxObserver) OnCleanupFailure(requestID uuid.UUID, path string, err error) {
	o.metrics.IncSandboxCleanupFailure()
	o.logger.Error("sandbox_cleanup_failure",
		"event", "sandbox_cleanup_failure",
		"request_id", requestID.String(),
		"job_dir", path,
		"os_error", err.Error(),
	)
}

func (o sandboxObserver) OnNsJailFailure(requestID uuid.UUID, category string, err error) {
	o.logger.Error("nsjail_invocation_failure",
		"event", "nsjail_invocation_failure",
		"request_id", requestID.String(),
		"failure_category", category,
		"error", err.Error(),
	)
}

// reaperObserver implements runner.ReaperObserver.
type reaperObserver struct {
	logger  *slog.Logger
	metrics *metrics.Collector
}

func (o reaperObserver) OnReap(path string, age time.Duration) {
	o.metrics.IncOrphanWorkspaceReaped()
	o.logger.Info("orphan_workspace_reaped",
		"event", "orphan_workspace_reaped",
		"path", path,
		"age_seconds", int64(age.Seconds()),
	)
}

func (o reaperObserver) OnReapFailure(path string, err error) {
	o.logger.Error("orphan_workspace_reap_failed",
		"event", "orphan_workspace_reap_failed",
		"path", path,
		"error", err.Error(),
	)
}

// poolObserver implements worker.Observer.
type poolObserver struct {
	metrics *metrics.Collector
}

func (o poolObserver) OnQueueDepthChanged(depth int) {
	o.metrics.SetQueueDepth(depth)
}

func (o poolObserver) OnSubmissionRejected(reason string) {
	// The run handler already logs rejections at completion.
}

// workspaceObserver implements runner.WorkspaceObserver.
type workspaceObserver struct {
	logger  *slog.Logger
	metrics *metrics.Collector
}

func (o workspaceObserver) OnIsolationViolation(err runner.WorkspaceIsolationError) {
	o.metrics.IncWorkspaceIsolationViolation()
	o.logger.Error("workspace_isolation_violation",
		"event", "workspace_isolation_violation",
		"request_id", err.RequestID.String(),
		"expected", err.Expected,
		"received", err.Received,
	)
}
