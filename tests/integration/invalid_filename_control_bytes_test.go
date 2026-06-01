//go:build integration

package integration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidFilenameControlBytesRegistryRejection(t *testing.T) {
	tmp := t.TempDir()
	sandboxDir := filepath.Join(tmp, "sandbox")
	if err := os.Mkdir(sandboxDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create custom registry containing a null character (\x00) in source_filename
	registryContent := "languages:\n  - id: py3\n    source_filename: \"main\\x00.py\"\n    run:\n      command: /usr/bin/python3\n      args: [\"main.py\"]\n      limits:\n        wall_time_s: 10\n        cpu_time_s: 10\n        memory_mb: 256\n        process_count: 16\n        output_size_mb: 4\n"
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
		t.Errorf("expected error to contain 'unsafe_filename', got: %s", stderr)
	}
}
