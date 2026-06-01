//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestWorkspaceIsolationConcurrent(t *testing.T) {
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

	var wg sync.WaitGroup
	var respA, respB map[string]any
	var errA, errB error

	wg.Add(2)

	// Goroutine A: Writes token_a.txt, sleeps 2s, checks token_b.txt
	go func() {
		defer wg.Done()
		payload := map[string]any{
			"language": "py3",
			"source": `import time, os
with open('token_a.txt', 'w') as f:
    f.write('SECRET_A')
time.sleep(2)
if os.path.exists('token_b.txt'):
    print('found_token_b')
else:
    print('isolated_a')
`,
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(srv.URL+"/run", "application/json", bytes.NewReader(body))
		if err != nil {
			errA = err
			return
		}
		defer resp.Body.Close()
		_ = json.NewDecoder(resp.Body).Decode(&respA)
	}()

	// Goroutine B: Sleeps 0.5s, writes token_b.txt, checks token_a.txt
	go func() {
		defer wg.Done()
		time.Sleep(500 * time.Millisecond)
		payload := map[string]any{
			"language": "py3",
			"source": `import time, os
with open('token_b.txt', 'w') as f:
    f.write('SECRET_B')
if os.path.exists('token_a.txt'):
    print('found_token_a')
else:
    print('isolated_b')
`,
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(srv.URL+"/run", "application/json", bytes.NewReader(body))
		if err != nil {
			errB = err
			return
		}
		defer resp.Body.Close()
		_ = json.NewDecoder(resp.Body).Decode(&respB)
	}()

	wg.Wait()

	if errA != nil {
		t.Fatalf("Request A failed: %v", errA)
	}
	if errB != nil {
		t.Fatalf("Request B failed: %v", errB)
	}

	if respA["status"] != "OK" {
		t.Errorf("Request A status not OK: %v", respA)
	}
	if respB["status"] != "OK" {
		t.Errorf("Request B status not OK: %v", respB)
	}

	if respA["stdout"] != "isolated_a\n" {
		t.Errorf("Request A was not isolated: stdout=%q", respA["stdout"])
	}
	if respB["stdout"] != "isolated_b\n" {
		t.Errorf("Request B was not isolated: stdout=%q", respB["stdout"])
	}
}
