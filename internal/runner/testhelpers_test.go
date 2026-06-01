package runner_test

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/thesouldev/goboxd/internal/runner"
)

// stubLauncher is a deterministic ExecLauncher every runner_test uses
// in place of the real os/exec invocation. The launcher records every
// ExecRequest it sees and returns a pre-canned ExecOutcome.
type stubLauncher struct {
	mu       sync.Mutex
	requests []runner.ExecRequest

	// outcome is the canned outcome for every Launch call. Tests
	// override individual fields per case.
	outcome runner.ExecOutcome

	// launchErr forces Launch to return this error before constructing
	// an ExecOutcome.
	launchErr error

	// stdout / stderr override the readers the launcher returns.
	stdout io.Reader
	stderr io.Reader

	// waitFn override the Wait closure; if nil the default returns
	// outcome's ExecResult zero-value (StatusOK from Classify).
	waitFn func() runner.ExecResult

	// panicOnLaunch makes Launch panic; used for the cleanup-on-panic
	// test.
	panicOnLaunch bool
}

func (s *stubLauncher) Launch(ctx context.Context, req runner.ExecRequest) (runner.ExecOutcome, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()

	if s.panicOnLaunch {
		panic("stub: forced panic during launch")
	}
	if s.launchErr != nil {
		return runner.ExecOutcome{}, s.launchErr
	}
	out := s.outcome
	if s.stdout != nil {
		out.Stdout = s.stdout
	} else if out.Stdout == nil {
		out.Stdout = strings.NewReader("")
	}
	if s.stderr != nil {
		out.Stderr = s.stderr
	} else if out.Stderr == nil {
		out.Stderr = strings.NewReader("")
	}
	if s.waitFn != nil {
		out.Wait = s.waitFn
	} else if out.Wait == nil {
		out.Wait = func() runner.ExecResult { return runner.ExecResult{ExitCode: 0} }
	}
	return out, nil
}

// recordingSandboxObserver captures every observer event so tests can
// assert the metric/log surface without importing internal/metrics.
type recordingSandboxObserver struct {
	mu               sync.Mutex
	cleanupFailures  []sandboxObserverCleanup
	nsjailFailures   []sandboxObserverNsjail
}

type sandboxObserverCleanup struct {
	RequestID uuid.UUID
	Path      string
	Err       error
}

type sandboxObserverNsjail struct {
	RequestID uuid.UUID
	Category  string
	Err       error
}

func (o *recordingSandboxObserver) OnCleanupFailure(id uuid.UUID, path string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cleanupFailures = append(o.cleanupFailures, sandboxObserverCleanup{id, path, err})
}

func (o *recordingSandboxObserver) OnNsJailFailure(id uuid.UUID, category string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.nsjailFailures = append(o.nsjailFailures, sandboxObserverNsjail{id, category, err})
}

// validJobPy3 returns a Job that mirrors the architecture §12 py3
// worked example. Unit tests vary one field per case; the helper keeps
// case bodies short.
func validJobPy3() runner.Job {
	return runner.Job{
		LanguageID:     "py3",
		SourceFilename: "main.py",
		Source:         "print('hello')\n",
		EffectiveLimits: runner.Limits{
			WallTimeS:    10,
			CPUTimeS:     10,
			MemoryMB:     256,
			ProcessCount: 16,
			OutputSizeMB: 4,
		},
		RunTemplate: runner.CommandTemplate{
			Command: "/usr/bin/python3",
			Args:    []string{"{{SOURCE_FILENAME}}"},
		},
	}
}

// newStubRunner builds a SandboxRunner with sensible defaults for
// runner unit tests: a stub launcher, a deterministic clock, a
// per-test sandbox root.
func newStubRunner(t *testing.T, root string, launcher *stubLauncher, obs runner.SandboxObserver) *runner.SandboxRunner {
	t.Helper()
	if obs == nil {
		obs = &recordingSandboxObserver{}
	}
	clock := &fakeClock{now: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)}
	r, err := runner.New(runner.SandboxConfig{
		NsJailPath:              "/usr/local/bin/nsjail",
		SandboxRoot:             root,
		SeccompPolicy:           "DEFAULT KILL",
		StdoutCaptureLimitBytes: 1024,
		StderrCaptureLimitBytes: 1024,
		LaunchTimeout:           5 * time.Second,
		Now:                     clock.Now,
		Exec:                    launcher,
		Observer:                obs,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// fakeClock is a deterministic time.Now replacement. Each call advances
// by 1 ms so DurationMS values are observable but stable.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Millisecond)
	return c.now
}

// readerOf is a small helper that builds an io.Reader from a string
// while letting tests still treat the result as io.Reader (rather than
// *strings.Reader) where the launcher's interface needs the abstract
// type.
func readerOf(s string) io.Reader { return strings.NewReader(s) }

