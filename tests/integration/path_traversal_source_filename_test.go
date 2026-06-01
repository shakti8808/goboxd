//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathTraversalRegistryRejection(t *testing.T) {
	tmp := t.TempDir()
	sandboxDir := filepath.Join(tmp, "sandbox")
	if err := os.Mkdir(sandboxDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create custom registry containing path traversal
	registryContent := `
languages:
  - id: py3
    source_filename: ../etc/passwd
    run:
      command: /usr/bin/python3
      args: ["main.py"]
      limits:
        wall_time_s: 10
        cpu_time_s: 10
        memory_mb: 256
        process_count: 16
        output_size_mb: 4
`
	registryPath := filepath.Join(tmp, "language_registry.yaml")
	if err := os.WriteFile(registryPath, []byte(registryContent), 0644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"SANDBOX_ROOT_DIR":       sandboxDir,
		"LANGUAGE_REGISTRY_PATH": registryPath,
		"LOG_LEVEL":              "DEBUG",
	}

	exitCode, _, stderr := runServerExpectFailure(t, env)

	if exitCode == 0 {
		t.Errorf("expected server to exit with non-zero code, got 0")
	}

	if !strings.Contains(stderr, "unsafe_filename") {
		t.Errorf("expected error message to contain 'unsafe_filename', got: %s", stderr)
	}
}
