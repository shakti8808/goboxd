package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ExecutionResult is the structured outcome of a single SandboxRunner.Run
// call. It maps onto architecture spec §"Data Models" Execution_Result
// modulo wire-shape concerns: the run handler in Wave D translates this
// struct to JSON and applies any presentation-layer rules (e.g. emitting
// a sentinel exit code when TerminatedBySignal is true).
type ExecutionResult struct {
	Status          Status
	ExitCode        int
	TerminatedBySig bool
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	DurationMS      int64
}

// SandboxObserver is the runner-local hook the run handler in Wave D
// wires to internal/metrics and internal/log. The runner does not
// import either package directly so the architecture §5 dependency
// graph stays acyclic; tests inject a recording observer to assert the
// surface.
type SandboxObserver interface {
	OnCleanupFailure(requestID uuid.UUID, path string, err error)
	OnNsJailFailure(requestID uuid.UUID, category string, err error)
}

// nopSandboxObserver is the default SandboxObserver. It silently
// discards every event; production wires a real observer at construction
// time, tests assert via a recording stub.
type nopSandboxObserver struct{}

func (nopSandboxObserver) OnCleanupFailure(uuid.UUID, string, error) {}
func (nopSandboxObserver) OnNsJailFailure(uuid.UUID, string, error)  {}

// SandboxConfig is the immutable configuration the SandboxRunner reads
// once at construction time. Fields beyond the architecture spec's
// required set are exposed so tests can pin a deterministic clock and a
// stub launcher.
type SandboxConfig struct {
	NsJailPath              string
	SandboxRoot             string
	SeccompPolicy           string
	SandboxGuestDir         string
	LaunchTimeout           time.Duration
	BindMounts              []BindMount
	EnvAllowlist            []EnvVar
	StdoutCaptureLimitBytes int
	StderrCaptureLimitBytes int

	// Now defaults to time.Now; tests pin a clock to make DurationMS
	// deterministic.
	Now func() time.Time

	// Exec defaults to NewDefaultExecLauncher(); tests inject a stub.
	Exec ExecLauncher

	// Observer defaults to nopSandboxObserver{}; the run handler in
	// Wave D wires real metrics/log hooks here.
	Observer SandboxObserver

	// WorkspaceObs defaults to a no-op; CleanupOnly threads it through
	// Workspace.Verify so isolation violations surface to the metrics
	// counter without internal/runner importing internal/metrics.
	WorkspaceObs WorkspaceObserver
}

// ErrInvalidConfig is returned by New when SandboxConfig fails
// validation.
var ErrInvalidConfig = errors.New("runner: invalid sandbox config")

// ErrInvalidJob is returned by Run when the supplied Job is internally
// inconsistent (e.g. stdin bytes supplied but Job.StdinFile is empty).
var ErrInvalidJob = errors.New("runner: invalid job")

// SandboxRunner orchestrates the per-request lifecycle of a sandboxed
// code execution: allocate workspace, materialise files, build argv,
// launch nsjail, capture output, classify outcome, cleanup.
//
// A SandboxRunner is constructed once at startup and shared across
// concurrent requests. Per-request state (the workspace, the captured
// output, the classification input) is local to Run; SandboxRunner
// itself is read-only after New returns.
type SandboxRunner struct {
	cfg SandboxConfig
}

// New builds a SandboxRunner from cfg, applying defaults for optional
// fields and validating required ones.
func New(cfg SandboxConfig) (*SandboxRunner, error) {
	if strings.TrimSpace(cfg.NsJailPath) == "" {
		return nil, fmt.Errorf("%w: NsJailPath is empty", ErrInvalidConfig)
	}
	if !filepath.IsAbs(cfg.NsJailPath) {
		return nil, fmt.Errorf("%w: NsJailPath %q is not absolute", ErrInvalidConfig, cfg.NsJailPath)
	}
	if strings.TrimSpace(cfg.SandboxRoot) == "" {
		return nil, fmt.Errorf("%w: SandboxRoot is empty", ErrInvalidConfig)
	}
	if !filepath.IsAbs(cfg.SandboxRoot) {
		return nil, fmt.Errorf("%w: SandboxRoot %q is not absolute", ErrInvalidConfig, cfg.SandboxRoot)
	}
	if strings.TrimSpace(cfg.SeccompPolicy) == "" {
		return nil, fmt.Errorf("%w: SeccompPolicy is empty", ErrInvalidConfig)
	}
	if cfg.StdoutCaptureLimitBytes <= 0 {
		return nil, fmt.Errorf("%w: StdoutCaptureLimitBytes must be > 0", ErrInvalidConfig)
	}
	if cfg.StderrCaptureLimitBytes <= 0 {
		return nil, fmt.Errorf("%w: StderrCaptureLimitBytes must be > 0", ErrInvalidConfig)
	}
	if cfg.LaunchTimeout <= 0 {
		cfg.LaunchTimeout = 5 * time.Second
	}
	if cfg.SandboxGuestDir == "" {
		cfg.SandboxGuestDir = "/sandbox"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Exec == nil {
		cfg.Exec = NewDefaultExecLauncher()
	}
	if cfg.Observer == nil {
		cfg.Observer = nopSandboxObserver{}
	}
	if cfg.WorkspaceObs == nil {
		cfg.WorkspaceObs = nopObserver{}
	}
	return &SandboxRunner{cfg: cfg}, nil
}

// Run executes the per-request lifecycle for job and returns its
// ExecutionResult. Stdin (when non-empty) is written to a file inside
// the workspace named by job.StdinFile and made available to the child
// via the {{STDIN_FILE}} placeholder.
//
// Cleanup of the workspace is guaranteed on every termination mode:
// success, classified failure, panic, ctx cancellation, shutdown. The
// deferred path is the only place that calls Workspace.Cleanup so the
// invariant is enforced at one site.
func (r *SandboxRunner) Run(ctx context.Context, job Job, stdin []byte) (result ExecutionResult, runErr error) {
	if err := validateJob(job, stdin); err != nil {
		return ExecutionResult{Status: StatusInternalError}, err
	}
	requestID, err := uuid.NewRandom()
	if err != nil {
		return ExecutionResult{Status: StatusInternalError}, fmt.Errorf("runner: generate request id: %w", err)
	}
	ws, err := AllocateWorkspace(r.cfg.SandboxRoot, requestID)
	if err != nil {
		return ExecutionResult{Status: StatusInternalError}, err
	}
	// Cleanup runs on every termination mode (success, classified
	// failure, panic, ctx cancellation). The closure observes any
	// cleanup error via the observer interface and never overwrites
	// the result the caller is about to receive.
	defer func() {
		if rec := recover(); rec != nil {
			r.observeCleanup(ws, ws.Cleanup())
			panic(rec)
		}
		r.observeCleanup(ws, ws.Cleanup())
	}()

	job.JobDir = ws.Path()

	// Materialise the source file inside the workspace. The argv
	// builder enforces basename safety on every filename it sees, so
	// we don't re-validate here.
	if err := os.WriteFile(filepath.Join(ws.Path(), job.SourceFilename), []byte(job.Source), 0o600); err != nil {
		return ExecutionResult{Status: StatusInternalError}, fmt.Errorf("runner: write source: %w", err)
	}

	// Materialise stdin when supplied. The orchestrator picks the
	// stdin filename from job.StdinFile; the {{STDIN_FILE}} placeholder
	// in argv references it.
	if len(stdin) > 0 {
		if err := os.WriteFile(filepath.Join(ws.Path(), job.StdinFile), stdin, 0o600); err != nil {
			return ExecutionResult{Status: StatusInternalError}, fmt.Errorf("runner: write stdin: %w", err)
		}
	}

	// Compile step (if defined) — the run handler in Wave D will fill
	// CompileTemplate when the language carries a compile block;
	// orchestrator skips it transparently when the template is empty.
	start := r.cfg.Now()
	if job.CompileTemplate.Command != "" {
		compileResult, compileErr := r.runStep(ctx, StepCompile, job.CompileTemplate, job, stdin, ws.RequestID())
		if compileErr != nil {
			return ExecutionResult{Status: StatusInternalError, DurationMS: r.elapsedMS(start)}, compileErr
		}
		if compileResult.Status != StatusOK {
			compileResult.DurationMS = r.elapsedMS(start)
			return compileResult, nil
		}
	}

	// Run step is mandatory. The runStep helper composes argv builder,
	// launcher, capture, and classifier.
	runResult, runStepErr := r.runStep(ctx, StepRun, job.RunTemplate, job, stdin, ws.RequestID())
	if runStepErr != nil {
		return ExecutionResult{Status: StatusInternalError, DurationMS: r.elapsedMS(start)}, runStepErr
	}
	runResult.DurationMS = r.elapsedMS(start)
	return runResult, nil
}

// runStep is the inner orchestrator that drives a single nsjail
// invocation. It is shared between the compile and run steps so the
// argv-builder + launcher + capture + classifier composition is
// asserted at one site.
func (r *SandboxRunner) runStep(ctx context.Context, step StepKind, tpl CommandTemplate, job Job, _ []byte, reqID uuid.UUID) (ExecutionResult, error) {
	argvIn := ArgvInput{
		NsJailPath:      r.cfg.NsJailPath,
		Step:            step,
		Template:        tpl,
		Job:             job,
		EnvAllowlist:    r.cfg.EnvAllowlist,
		BindMounts:      r.cfg.BindMounts,
		SeccompPolicy:   r.cfg.SeccompPolicy,
		SandboxGuestDir: r.cfg.SandboxGuestDir,
	}
	argv, err := BuildArgv(argvIn)
	if err != nil {
		return ExecutionResult{Status: StatusInternalError}, err
	}

	stdinBytes := []byte(nil)
	if step == StepRun && job.StdinFile != "" {
		// The launcher writes stdin to the child's stdin pipe; the
		// orchestrator passes the bytes through. The argv builder has
		// already substituted {{STDIN_FILE}} with the basename, so the
		// child can also read the file directly when the language
		// definition prefers that pattern.
		// We pass an empty slice to the launcher because the file is
		// the canonical channel; surfacing both would risk double-feed.
		stdinBytes = nil
	}

	outcome, err := r.cfg.Exec.Launch(ctx, ExecRequest{
		Argv:          argv,
		Stdin:         stdinBytes,
		LaunchTimeout: r.cfg.LaunchTimeout,
	})
	if err != nil {
		r.cfg.Observer.OnNsJailFailure(reqID, "launch_returned_error", err)
		return ExecutionResult{Status: StatusInternalError}, err
	}
	if outcome.LaunchFailed {
		r.cfg.Observer.OnNsJailFailure(reqID, "launch_failed", outcome.LaunchErr)
		return ExecutionResult{Status: StatusInternalError}, nil
	}

	// Capture stdout and stderr concurrently so a deadlocked child
	// (closes one pipe before the other) does not block the runner.
	var stdoutRes, stderrRes CaptureResult
	var stdoutErr, stderrErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stdoutRes, stdoutErr = Capture(outcome.Stdout, r.cfg.StdoutCaptureLimitBytes)
	}()
	go func() {
		defer wg.Done()
		stderrRes, stderrErr = Capture(outcome.Stderr, r.cfg.StderrCaptureLimitBytes)
	}()
	wg.Wait()

	exec := outcome.Wait()

	// Capture errors propagate to the launcher classification: an EOF
	// or closed-pipe error is normal, a real I/O error is treated as
	// invocation failure for safety.
	if (stdoutErr != nil && !isStreamClosed(stdoutErr)) ||
		(stderrErr != nil && !isStreamClosed(stderrErr)) {
		err := stdoutErr
		if err == nil {
			err = stderrErr
		}
		r.cfg.Observer.OnNsJailFailure(reqID, "capture_io_failed", err)
		return ExecutionResult{Status: StatusInternalError}, nil
	}

	status := Classify(ClassifierInput{
		Step:               step,
		ExitCode:           exec.ExitCode,
		TerminatedBySignal: exec.TerminatedBySignal,
		LimitIndicator:     exec.LimitIndicator,
		InvocationFailure:  false,
	})

	return ExecutionResult{
		Status:          status,
		ExitCode:        exec.ExitCode,
		TerminatedBySig: exec.TerminatedBySignal,
		Stdout:          stdoutRes.Retained,
		Stderr:          stderrRes.Retained,
		StdoutTruncated: stdoutRes.Truncated,
		StderrTruncated: stderrRes.Truncated,
	}, nil
}

// CleanupOnly removes a workspace from a deferred path that does not
// own the workspace's lifecycle (e.g. a parent that pre-allocated the
// workspace before handing it to a goroutine). The function enforces
// the architecture §11 ownership invariant: the caller MUST pass the
// same *Workspace it allocated; passing a foreign workspace is rejected
// via the configured WorkspaceObserver.
func (r *SandboxRunner) CleanupOnly(workspace *Workspace) error {
	if workspace == nil {
		return nil
	}
	if err := workspace.Verify(workspace.Path(), r.cfg.WorkspaceObs); err != nil {
		return err
	}
	if err := workspace.Cleanup(); err != nil {
		r.cfg.Observer.OnCleanupFailure(workspace.RequestID(), workspace.Path(), err)
		return err
	}
	return nil
}

// observeCleanup notifies the SandboxObserver when ws.Cleanup returns
// a non-nil error. Callers thread this through the deferred path so a
// cleanup failure surfaces to the metrics counter without altering the
// returned ExecutionResult.
func (r *SandboxRunner) observeCleanup(ws *Workspace, err error) {
	if err == nil {
		return
	}
	r.cfg.Observer.OnCleanupFailure(ws.RequestID(), ws.Path(), err)
}

// elapsedMS returns the milliseconds since start, clamped to a
// non-negative int64 for the ExecutionResult envelope.
func (r *SandboxRunner) elapsedMS(start time.Time) int64 {
	d := r.cfg.Now().Sub(start).Milliseconds()
	if d < 0 {
		return 0
	}
	return d
}

// isStreamClosed reports whether err is one of the benign
// stream-terminator errors Capture surfaces.
func isStreamClosed(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe)
}

// validateJob enforces structural preconditions on the orchestrator's
// inputs before any filesystem operation occurs. The argv builder will
// re-check filename safety; this function only catches caller bugs the
// argv builder couldn't see (e.g. stdin bytes without a stdin file).
func validateJob(job Job, stdin []byte) error {
	if strings.TrimSpace(job.SourceFilename) == "" {
		return fmt.Errorf("%w: SourceFilename is empty", ErrInvalidJob)
	}
	if strings.TrimSpace(job.RunTemplate.Command) == "" {
		return fmt.Errorf("%w: RunTemplate.Command is empty", ErrInvalidJob)
	}
	if len(stdin) > 0 && strings.TrimSpace(job.StdinFile) == "" {
		return fmt.Errorf("%w: stdin bytes supplied without StdinFile", ErrInvalidJob)
	}
	return nil
}
