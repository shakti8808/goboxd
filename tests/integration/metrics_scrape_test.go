//go:build integration

package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMetricsScrapeConsistency(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nsjail requires Linux; skipping on this platform")
	}
	if _, err := os.Stat("/usr/local/bin/nsjail"); err != nil {
		t.Skip("nsjail binary not present at /usr/local/bin/nsjail; skipping")
	}

	sandboxDir := t.TempDir()
	env := map[string]string{
		"SANDBOX_ROOT_DIR":      sandboxDir,
		"LOG_LEVEL":             "DEBUG",
		"MAX_SOURCE_SIZE_BYTES": "30",
	}

	srv := startServer(t, env)
	defer srv.Close()

	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Capture baseline metrics
	baseline := scrapeMetrics(t, client, srv.URL+"/metrics")

	// 2. Execute deterministic request sequence
	// A. 3x py3 OK
	for i := 0; i < 3; i++ {
		payload := map[string]any{
			"language": "py3",
			"source":   "print('ok')",
		}
		sendRunRequest(t, client, srv.URL, payload, http.StatusOK, "OK")
	}

	// B. 2x cpp OK
	for i := 0; i < 2; i++ {
		payload := map[string]any{
			"language": "cpp",
			"source":   "#include <iostream>\nint main() {\n    std::cout << \"ok\\n\";\n    return 0;\n}",
		}
		sendRunRequest(t, client, srv.URL, payload, http.StatusOK, "OK")
	}

	// C. 1x py3 TIME_LIMIT_EXCEEDED
	timeoutPayload := map[string]any{
		"language": "py3",
		"source":   "import time\ntime.sleep(5)",
		"resource_limits": map[string]any{
			"wall_time_s": 1,
		},
	}
	// We save the response body to show it in the completion report
	timeoutRespBody := sendRunRequest(t, client, srv.URL, timeoutPayload, http.StatusOK, "TIME_LIMIT_EXCEEDED")
	t.Logf("TIME_LIMIT_EXCEEDED response body: %s", timeoutRespBody)

	// D. 4 validator rejections
	// D1. malformed_submission (missing source)
	malformedPayload := map[string]any{
		"language": "py3",
	}
	sendRunRequestExpectRejection(t, client, srv.URL, malformedPayload, "malformed_submission")

	// D2. language_not_registered (rust)
	unregisteredPayload := map[string]any{
		"language": "rust",
		"source":   "print('hello')",
	}
	sendRunRequestExpectRejection(t, client, srv.URL, unregisteredPayload, "language_not_registered")

	// D3. source_size_exceeded (exceeding MAX_SOURCE_SIZE_BYTES=30)
	sourceSizePayload := map[string]any{
		"language": "py3",
		"source":   "print('this source code is 31 bytes!')",
	}
	sendRunRequestExpectRejection(t, client, srv.URL, sourceSizePayload, "source_size_exceeded")

	// D4. resource_limit_exceeded (exceeding memory ceiling 1024)
	resourceLimitPayload := map[string]any{
		"language": "py3",
		"source":   "print('ok')",
		"resource_limits": map[string]any{
			"memory_mb": 2048,
		},
	}
	sendRunRequestExpectRejection(t, client, srv.URL, resourceLimitPayload, "resource_limit_exceeded")

	// 3. Scrape metrics again
	post := scrapeMetrics(t, client, srv.URL+"/metrics")

	// 4. Assert deltas (post - baseline)
	// goboxd_run_requests_total
	assertDelta(t, baseline, post, `goboxd_run_requests_total{language="py3",status="OK"}`, 3)
	assertDelta(t, baseline, post, `goboxd_run_requests_total{language="cpp",status="OK"}`, 2)
	assertDelta(t, baseline, post, `goboxd_run_requests_total{language="py3",status="TIME_LIMIT_EXCEEDED"}`, 1)

	// goboxd_run_duration_seconds_count
	assertDelta(t, baseline, post, `goboxd_run_duration_seconds_count`, 6)

	// goboxd_security_rejections_total
	assertDelta(t, baseline, post, `goboxd_security_rejections_total{rule="malformed_submission"}`, 1)
	assertDelta(t, baseline, post, `goboxd_security_rejections_total{rule="language_not_registered"}`, 1)
	assertDelta(t, baseline, post, `goboxd_security_rejections_total{rule="source_size_exceeded"}`, 1)
	assertDelta(t, baseline, post, `goboxd_security_rejections_total{rule="resource_limit_exceeded"}`, 1)

	// goboxd_worker_pool_queue_depth must be 0 after completion
	queueDepth, ok := post[`goboxd_worker_pool_queue_depth`]
	if !ok {
		t.Errorf("goboxd_worker_pool_queue_depth is not present in /metrics output")
	} else if queueDepth != 0 {
		t.Errorf("expected final goboxd_worker_pool_queue_depth to be 0, got %f", queueDepth)
	}
}

func scrapeMetrics(t *testing.T, client *http.Client, url string) map[string]float64 {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("failed to GET metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 from /metrics, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read /metrics body: %v", err)
	}
	return parsePrometheusMetrics(string(body))
}

func parsePrometheusMetrics(content string) map[string]float64 {
	metrics := make(map[string]float64)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Find the last space separating the metric key and value
		idx := strings.LastIndex(line, " ")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		valStr := strings.TrimSpace(line[idx+1:])
		val, err := strconv.ParseFloat(valStr, 64)
		if err == nil {
			metrics[key] = val
		}
	}
	return metrics
}

func sendRunRequest(t *testing.T, client *http.Client, host string, payload map[string]any, expectedStatus int, expectedResultStatus string) string {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}
	resp, err := client.Post(host+"/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /run failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expectedStatus {
		t.Fatalf("expected HTTP %d, got %d", expectedStatus, resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err != nil {
		t.Fatalf("failed to unmarshal response: %v, raw: %s", err, string(respBytes))
	}

	if expectedResultStatus != "" {
		if res["status"] != expectedResultStatus {
			t.Errorf("expected execution status %q, got %v (response: %s)", expectedResultStatus, res["status"], string(respBytes))
		}
	}
	return string(respBytes)
}

func sendRunRequestExpectRejection(t *testing.T, client *http.Client, host string, payload map[string]any, expectedError string) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}
	resp, err := client.Post(host+"/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /run failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400 for rejection, got %d", resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var res map[string]any
	if err := json.Unmarshal(respBytes, &res); err != nil {
		t.Fatalf("failed to unmarshal rejection response: %v, raw: %s", err, string(respBytes))
	}

	if res["error"] != expectedError {
		t.Errorf("expected error %q, got %v (response: %s)", expectedError, res["error"], string(respBytes))
	}
}

func assertDelta(t *testing.T, baseline, post map[string]float64, key string, expectedDelta float64) {
	t.Helper()
	b := baseline[key] // zero if key is absent
	p := post[key]     // zero if key is absent
	delta := p - b
	if delta != expectedDelta {
		t.Errorf("metric delta mismatch for key %q: baseline=%f, post=%f, got delta=%f, want %f", key, b, p, delta, expectedDelta)
	}
}
