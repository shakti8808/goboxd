//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestOversizeSourceRejection(t *testing.T) {
	sandboxDir := t.TempDir()
	env := map[string]string{
		"SANDBOX_ROOT_DIR":      sandboxDir,
		"LOG_LEVEL":             "DEBUG",
		"MAX_SOURCE_SIZE_BYTES": "100", // configured low limit to trigger rejection
	}
	srv := startServer(t, env)
	defer srv.Close()

	largeSource := strings.Repeat("A", 101)

	payload := map[string]any{
		"language": "py3",
		"source":   largeSource,
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(srv.URL+"/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /run failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400, got %d", resp.StatusCode)
	}

	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}

	if res["error"] != "source_size_exceeded" {
		t.Errorf("expected error 'source_size_exceeded', got %v", res["error"])
	}
}
