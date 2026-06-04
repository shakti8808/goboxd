//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestQueueFullRejection(t *testing.T) {
	sandboxDir := t.TempDir()
	env := map[string]string{
		"SANDBOX_ROOT_DIR":      sandboxDir,
		"LOG_LEVEL":             "DEBUG",
		"WORKER_POOL_SIZE":      "1", // 1 worker
		"WORKER_POOL_QUEUE_LEN": "1", // 1 queue slot
		"NSJAIL_SLEEP_S":        "2", // mock/real nsjail sleeps for 2s
	}
	srv := startServer(t, env)
	defer srv.Close()

	payload := map[string]any{
		"language": "py3",
		"source":   "import time\ntime.sleep(2)",
	}
	body, _ := json.Marshal(payload)

	var wg sync.WaitGroup
	var statuses []int
	var statusesMu sync.Mutex

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(srv.URL+"/run", "application/json", bytes.NewReader(body))
			if err != nil {
				return
			}
			defer resp.Body.Close()

			statusesMu.Lock()
			statuses = append(statuses, resp.StatusCode)
			statusesMu.Unlock()
		}()
		// Slight delay to ensure deterministic ordering of submissions
		time.Sleep(100 * time.Millisecond)
	}

	wg.Wait()

	var got200, got429 int
	for _, code := range statuses {
		if code == http.StatusOK {
			got200++
		} else if code == http.StatusTooManyRequests {
			got429++
		}
	}

	if got200 != 2 {
		t.Errorf("expected 2 successful requests, got %d. All statuses: %v", got200, statuses)
	}
	if got429 != 1 {
		t.Errorf("expected 1 rejected request (429), got %d. All statuses: %v", got429, statuses)
	}
}
