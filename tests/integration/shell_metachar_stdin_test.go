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

func TestShellMetacharactersStdin(t *testing.T) {
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

	payload := map[string]any{
		"language": "py3",
		"source":   "import sys\nprint(sys.stdin.read(), end='')",
		"stdin":    "hello; rm -rf /; echo $HOME",
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
	if res["stdout"] != "hello; rm -rf /; echo $HOME" {
		t.Errorf("expected stdout matching unmodified stdin, got %q", res["stdout"])
	}
}
