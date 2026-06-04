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

func TestPy3Hello(t *testing.T) {
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
		"source":   "print('hi')",
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
		t.Errorf("expected status OK, got %v, res: %+v", res["status"], res)
	}
	if res["stdout"] != "hi\n" {
		t.Errorf("expected stdout 'hi\n', got %q", res["stdout"])
	}
}
