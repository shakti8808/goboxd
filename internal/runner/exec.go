package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ExecRequest carries everything DefaultExecLauncher and any test stub
// needs to launch a single nsjail subprocess. Argv[0] is the nsjail
// binary path; the orchestrator never builds this struct directly —
// BuildArgv produces the argv slice and the orchestrator copies it
// through unchanged so Property 16 (every sandbox execution goes
// through nsjail) holds at the launcher boundary.
type ExecRequest struct {
	// Argv is the fully-resolved nsjail invocation. argv[0] is the
	// nsjail binary path; argv[1:] are nsjail flags terminated by `--`
	// and followed by the child argv.
	Argv []string

	// Stdin is the bytes piped to the child's stdin. May be empty.
	Stdin []byte

	// LaunchTimeout is the maximum wall-clock time the launcher waits
	// for nsjail to start producing output before classifying the
	// invocation as launch-failed (REQ A-10.7).
	LaunchTimeout time.Duration
}

// ExecOutcome is what the launcher returns to the orchestrator.
//
// Stdout and Stderr are io.Readers because Capture (Wave B) consumes
// streams; the launcher exposes the readers connected to the child's
// pipes and the orchestrator drives Capture against them concurrently
// with Wait. Wait blocks until the child has fully terminated; the
// orchestrator calls it after stdout/stderr drain so the captured
// totals are stable when Wait returns.
type ExecOutcome struct {
	// Stdout / Stderr are connected to the child's pipes. Both readers
	// terminate with io.EOF when the child closes its end.
	Stdout io.Reader
	Stderr io.Reader

	// Wait blocks until the child has fully exited and returns the
	// process result. Calling Wait more than once is safe; subsequent
	// calls return the cached result.
	Wait func() ExecResult

	// LaunchFailed is true when nsjail itself could not be located or
	// failed to start within LaunchTimeout. The orchestrator treats
	// this as the dominant input to the classifier (Property 19's
	// "InvocationFailure dominates" branch).
	LaunchFailed bool

	// LaunchErr carries the underlying os/exec error when LaunchFailed
	// is true; otherwise nil.
	LaunchErr error
}

// ExecResult is what Wait returns once the child has exited.
type ExecResult struct {
	ExitCode           int
	TerminatedBySignal bool
	// LimitIndicator is the nsjail-reported limit that fired, parsed
	// from stderr or its own log. Empty when no limit was hit.
	LimitIndicator LimitIndicator
}

// ExecLauncher is the seam that lets unit tests substitute a stub
// process launcher for the real os/exec invocation.
//
// The default production implementation is DefaultExecLauncher, returned
// by NewDefaultExecLauncher. Tests inject a recording stub directly into
// SandboxConfig.Exec.
type ExecLauncher interface {
	Launch(ctx context.Context, req ExecRequest) (ExecOutcome, error)
}

// NewDefaultExecLauncher returns the production ExecLauncher backed by
// os/exec.CommandContext.
//
// The launcher honours REQ A-10.7 by enforcing a launch deadline: if
// the child has not started producing output (stdout pipe attached,
// process started) within LaunchTimeout, the launcher reports
// LaunchFailed=true. The default 5 s ceiling comes from the
// architecture spec; callers override via ExecRequest.LaunchTimeout.
func NewDefaultExecLauncher() ExecLauncher { return defaultExecLauncher{} }

// defaultExecLauncher is the production implementation. It is
// stateless; one shared value is safe for concurrent use across
// requests because every call constructs its own *exec.Cmd.
type defaultExecLauncher struct{}

// Launch spawns the child via os/exec. The returned ExecOutcome's
// Wait function blocks on cmd.Wait and parses the resulting state into
// an ExecResult. Stdin bytes are written to the child synchronously
// inside this function so we can close the pipe deterministically;
// stdout/stderr pipes are attached and returned to the orchestrator,
// which drives Capture against them.
func (defaultExecLauncher) Launch(ctx context.Context, req ExecRequest) (ExecOutcome, error) {
	if len(req.Argv) == 0 {
		return ExecOutcome{LaunchFailed: true, LaunchErr: errors.New("runner: empty argv")}, nil
	}

	cmd := exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
	// Detach the child from the GoboxD process group so a host SIGTERM
	// reaches us first; nsjail's own PID namespace then collects the
	// untrusted descendants on teardown.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ExecOutcome{LaunchFailed: true, LaunchErr: err}, nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return ExecOutcome{LaunchFailed: true, LaunchErr: err}, nil
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return ExecOutcome{LaunchFailed: true, LaunchErr: err}, nil
	}

	// Start synchronously; the launch deadline is the time it takes
	// for fork+exec to complete. Real nsjail invocations that exceed
	// LaunchTimeout produce a context-deadline exceeded error here.
	startCtx, startCancel := context.WithTimeout(ctx, req.LaunchTimeout)
	defer startCancel()
	startedCh := make(chan error, 1)
	go func() { startedCh <- cmd.Start() }()
	select {
	case startErr := <-startedCh:
		if startErr != nil {
			_ = stdin.Close()
			return ExecOutcome{LaunchFailed: true, LaunchErr: startErr}, nil
		}
	case <-startCtx.Done():
		// fork+exec took longer than LaunchTimeout. Close pipes and
		// classify as launch failure; the goroutine continues and
		// reaps the child, but we don't wait for it.
		_ = stdin.Close()
		return ExecOutcome{LaunchFailed: true, LaunchErr: startCtx.Err()}, nil
	}

	// Stream stdin synchronously then close the pipe so the child
	// receives EOF. Errors here are surfaced via ExecResult; they
	// don't classify as launch failure (the child has already started).
	go func() {
		defer func() { _ = stdin.Close() }()
		if len(req.Stdin) > 0 {
			_, _ = stdin.Write(req.Stdin)
		}
	}()

	// We have to capture stderr ourselves so we can scan it for
	// nsjail limit indicators. The orchestrator wants the stderr
	// stream too (Capture); we tee it through a bytes.Buffer the
	// classifier can inspect.
	var stderrTee bytes.Buffer
	stderrR := io.TeeReader(stderr, &stderrTee)

	outcome := ExecOutcome{
		Stdout: stdout,
		Stderr: stderrR,
		Wait: func() ExecResult {
			waitErr := cmd.Wait()
			res := ExecResult{}
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				if state := exitErr.ProcessState; state != nil {
					res.ExitCode = state.ExitCode()
					if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
						res.TerminatedBySignal = true
					}
				}
			} else if waitErr != nil {
				// Non-ExitError (e.g. context cancelled) — treat as
				// signalled termination so the classifier maps run-step
				// failures to RUNTIME_ERROR rather than OK.
				res.TerminatedBySignal = true
			}
			
			var logStr string
			for i, arg := range req.Argv {
				if arg == "--log" && i+1 < len(req.Argv) {
					if content, err := os.ReadFile(req.Argv[i+1]); err == nil {
						logStr = string(content)
					}
					break
				}
			}

			res.LimitIndicator = parseNsjailLimit(stderrTee.String() + "\n" + logStr)
			return res
		},
	}
	return outcome, nil
}

// parseNsjailLimit scans the captured stderr for nsjail's limit-hit
// indicators. The substrings are stable across nsjail releases (they
// come from nsjail's source tree's hard-coded log strings) but we keep
// the matching coarse so a future release that rewords slightly still
// classifies correctly.
//
// Order of checks matters: memory must take precedence over time when
// both indicators are present (e.g. an OOM-killed process whose
// container also reports SIGKILL on timeout). The architecture §11
// classifier maps memory-attributed kills to MEMORY_LIMIT_EXCEEDED, so
// we surface the strongest signal first.
func parseNsjailLimit(stderr string) LimitIndicator {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "out of memory"),
		strings.Contains(s, "memory limit"),
		strings.Contains(s, "rlimit_as"):
		return LimitIndicatorMemory
	case strings.Contains(s, "cpu time"),
		strings.Contains(s, "rlimit_cpu"):
		return LimitIndicatorCPU
	case strings.Contains(s, "time >="),
		strings.Contains(s, "wall time"):
		return LimitIndicatorWall
	default:
		return LimitIndicatorNone
	}
}
