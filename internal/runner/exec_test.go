package runner_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesouldev/goboxd/internal/runner"
)

// TestDefaultExecLauncherZeroExit drives the production launcher
// against /bin/true (or true.exe-style equivalents) so the exit-code
// routing is exercised end-to-end without depending on nsjail.
func TestDefaultExecLauncherZeroExit(t *testing.T) {
	t.Parallel()
	binary, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("true not on PATH: %v", err)
	}
	out, err := runner.NewDefaultExecLauncher().Launch(context.Background(), runner.ExecRequest{
		Argv:          []string{binary},
		LaunchTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	// Drain stdout so Wait can return; Capture would be the production
	// path but io.Copy is enough here.
	_, _ = io.Copy(io.Discard, out.Stdout)
	_, _ = io.Copy(io.Discard, out.Stderr)
	res := out.Wait()
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if res.TerminatedBySignal {
		t.Error("TerminatedBySignal = true, want false")
	}
}


// TestDefaultExecLauncherNonZeroExit exercises the non-zero-exit
// classification path through the production launcher.
func TestDefaultExecLauncherNonZeroExit(t *testing.T) {
	t.Parallel()
	binary, err := exec.LookPath("false")
	if err != nil {
		t.Skipf("false not on PATH: %v", err)
	}
	out, err := runner.NewDefaultExecLauncher().Launch(context.Background(), runner.ExecRequest{
		Argv:          []string{binary},
		LaunchTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	_, _ = io.Copy(io.Discard, out.Stdout)
	_, _ = io.Copy(io.Discard, out.Stderr)
	res := out.Wait()
	if res.ExitCode == 0 {
		t.Errorf("ExitCode = 0, want non-zero")
	}
}

// TestDefaultExecLauncherEmptyArgv asserts the input-validation guard.
func TestDefaultExecLauncherEmptyArgv(t *testing.T) {
	t.Parallel()
	out, err := runner.NewDefaultExecLauncher().Launch(context.Background(), runner.ExecRequest{LaunchTimeout: time.Second})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !out.LaunchFailed {
		t.Error("LaunchFailed = false; want true on empty argv")
	}
}

// TestDefaultExecLauncherMissingBinary surfaces fork/exec failure as
// LaunchFailed.
func TestDefaultExecLauncherMissingBinary(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "no-such-binary")
	out, err := runner.NewDefaultExecLauncher().Launch(context.Background(), runner.ExecRequest{
		Argv:          []string{missing},
		LaunchTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if !out.LaunchFailed {
		t.Errorf("LaunchFailed = false; want true for missing binary")
	}
}

// TestDefaultExecLauncherStdinPipe writes bytes into the child's stdin
// and asserts cat echoes them back.
func TestDefaultExecLauncherStdinPipe(t *testing.T) {
	t.Parallel()
	binary, err := exec.LookPath("cat")
	if err != nil {
		t.Skipf("cat not on PATH: %v", err)
	}
	out, err := runner.NewDefaultExecLauncher().Launch(context.Background(), runner.ExecRequest{
		Argv:          []string{binary},
		Stdin:         []byte("hello\n"),
		LaunchTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	got, _ := io.ReadAll(out.Stdout)
	_, _ = io.Copy(io.Discard, out.Stderr)
	res := out.Wait()
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(string(got), "hello") {
		t.Errorf("stdout = %q, want it to contain 'hello'", got)
	}
}


// TestParseNsjailLimitClassification exercises the stderr scanner that
// maps nsjail log strings to LimitIndicator values. The function is
// intentionally tolerant — coarse substring matching rather than a
// regex pinned to a particular nsjail release — so the test asserts
// the matrix at architecture-spec granularity.
func TestParseNsjailLimitClassification(t *testing.T) {
	// We cannot call parseNsjailLimit directly because it is unexported.
	// Instead drive it through a synthetic ExecRequest whose stderr
	// pipe contains the indicator string and observe the Wait result.
	cases := []struct {
		name   string
		stderr string
		want   runner.LimitIndicator
	}{
		{"oom", "out of memory in cgroup", runner.LimitIndicatorMemory},
		{"rlimit_as", "rlimit_as exceeded", runner.LimitIndicatorMemory},
		{"cpu time", "cpu time exhausted", runner.LimitIndicatorCPU},
		{"wall", "time >= 10s", runner.LimitIndicatorWall},
		{"none", "child exited cleanly", runner.LimitIndicatorNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			script := filepath.Join(tmp, "fake")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf %s \""+tc.stderr+"\" 1>&2\nexit 0\n"), 0o700); err != nil {
				t.Fatalf("write fake: %v", err)
			}
			out, err := runner.NewDefaultExecLauncher().Launch(context.Background(), runner.ExecRequest{
				Argv:          []string{script},
				LaunchTimeout: 5 * time.Second,
			})
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			_, _ = io.Copy(io.Discard, out.Stdout)
			_, _ = io.Copy(io.Discard, out.Stderr)
			res := out.Wait()
			if res.LimitIndicator != tc.want {
				t.Errorf("LimitIndicator = %q, want %q (stderr=%q)", res.LimitIndicator, tc.want, tc.stderr)
			}
		})
	}
}
