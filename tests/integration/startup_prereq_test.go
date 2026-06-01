//go:build integration

package integration_test

import (
	"strings"
	"testing"
)

func TestStartupPrereqFailureNsjail(t *testing.T) {
	sandboxDir := t.TempDir()
	env := map[string]string{
		"SANDBOX_ROOT_DIR": sandboxDir,
		"NSJAIL_PATH":      "/does/not/exist/nsjail",
	}

	exitCode, stdout, stderr := runServerExpectFailure(t, env)

	if exitCode == 0 {
		t.Errorf("expected server to exit with non-zero code, got 0")
	}

	// The error log is written to stdout by default slog handler in main.go
	output := stdout + "\n" + stderr
	if !strings.Contains(output, `"event":"startup_prereq_failed"`) {
		t.Errorf("expected logs to contain 'startup_prereq_failed', got:\n%s", output)
	}
	if !strings.Contains(output, `"prereq":"nsjail_binary"`) {
		t.Errorf("expected logs to contain 'prereq\":\"nsjail_binary\"', got:\n%s", output)
	}
}

func TestStartupPrereqFailureSandboxRoot(t *testing.T) {
	env := map[string]string{
		"SANDBOX_ROOT_DIR": "/does/not/exist/sandbox_root_dir",
	}

	exitCode, stdout, stderr := runServerExpectFailure(t, env)

	if exitCode == 0 {
		t.Errorf("expected server to exit with non-zero code, got 0")
	}

	output := stdout + "\n" + stderr
	if !strings.Contains(output, `"event":"startup_prereq_failed"`) {
		t.Errorf("expected logs to contain 'startup_prereq_failed', got:\n%s", output)
	}
	if !strings.Contains(output, `"prereq":"sandbox_root"`) {
		t.Errorf("expected logs to contain 'prereq\":\"sandbox_root\"', got:\n%s", output)
	}
}
