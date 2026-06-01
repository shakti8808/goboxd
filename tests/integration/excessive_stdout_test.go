//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"testing"
)

func TestExcessiveStdout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nsjail requires Linux; skipping on this platform")
	}
	if _, err := os.Stat("/usr/local/bin/nsjail"); err != nil {
		t.Skip("nsjail binary not present at /usr/local/bin/nsjail; skipping")
	}

	sandboxDir := t.TempDir()
	env := map[string]string{
		"SANDBOX_ROOT_DIR": sandboxDir,
		"LOG_LEVEL":        "DEBUG",
	}
	srv := startServer(t, env)
	defer srv.Close()

	// print 5 MB of data. Since the limit in configs/language_registry.yaml is 4 MB, it should be truncated.
	payload := map[string]any{
		"language": "py3",
		"source":   "print('A' * (5 * 1024 * 1024))",
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(srv.URL+"/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /run failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}

	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}

	if res["status"] != "OK" {
		t.Errorf("expected status OK, got %v", res["status"])
	}

	stdoutTrunc, ok := res["stdout_truncated"].(bool)
	if !ok || !stdoutTrunc {
		t.Errorf("expected stdout_truncated=true, got %v", res["stdout_truncated"])
	}

	stdout, ok := res["stdout"].(string)
	if !ok {
		t.Fatal("stdout missing in response")
	}
	if len(stdout) > 4*1024*1024 {
		t.Errorf("expected stdout to be <= 4MB, got %d bytes", len(stdout))
	}
}
