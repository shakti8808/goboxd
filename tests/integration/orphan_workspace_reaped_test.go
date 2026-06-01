//go:build integration

package integration_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOrphanWorkspaceReaped(t *testing.T) {
	sandboxDir := t.TempDir()

	// Pre-create a backdated orphan workspace
	orphanDir := filepath.Join(sandboxDir, "job-orphan-12345")
	if err := os.Mkdir(orphanDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Backdate the modification time to be older than SANDBOX_ORPHAN_TTL_S (e.g. older than 60s)
	backdatedTime := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(orphanDir, backdatedTime, backdatedTime); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"SANDBOX_ROOT_DIR":      sandboxDir,
		"SANDBOX_ORPHAN_TTL_S":  "60", // 1 minute TTL
		"LOG_LEVEL":             "DEBUG",
	}

	srv := startServer(t, env)
	defer srv.Close()

	// Wait up to 5 seconds for the orphan workspace to be reaped and metric incremented
	var metricsContent string
	success := false

	for start := time.Now(); time.Since(start) < 5*time.Second; {
		resp, getErr := http.Get(srv.URL + "/metrics")
		if getErr != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		metricsContent = string(body)
		if strings.Contains(metricsContent, "goboxd_orphan_workspace_reaped_total 1") {
			success = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !success {
		t.Errorf("expected goboxd_orphan_workspace_reaped_total to be 1, but metrics content was:\n%s", metricsContent)
	}

	// Verify that the orphan directory was actually deleted from disk
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Errorf("expected orphan directory %s to be deleted, but it still exists (err: %v)", orphanDir, err)
	}
}
