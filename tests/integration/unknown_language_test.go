//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestUnknownLanguageRejection(t *testing.T) {
	sandboxDir := t.TempDir()
	env := map[string]string{
		"SANDBOX_ROOT_DIR": sandboxDir,
		"LOG_LEVEL":        "DEBUG",
	}
	srv := startServer(t, env)
	defer srv.Close()

	payload := map[string]any{
		"language": "rust", // not registered in configs/language_registry.yaml
		"source":   "fn main() {}",
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

	if res["error"] != "language_not_registered" {
		t.Errorf("expected error 'language_not_registered', got %v", res["error"])
	}
}
