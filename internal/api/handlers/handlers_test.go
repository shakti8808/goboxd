package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/thesouldev/goboxd/internal/api/handlers"
)

// TestHealthLive asserts /healthz always returns 200 with the documented
// JSON body, independent of the readiness flags.
func TestHealthLive(t *testing.T) {
	t.Parallel()
	var startup, registry, shutdown, pool, nsjail atomic.Bool
	h := handlers.NewHealthHandler(&startup, &registry, &shutdown, &pool, &nsjail, "/usr/local/bin/nsjail")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.Live(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", body, `{"status":"ok"}`)
	}
}

// TestHealthReadyMatrix walks every combination of the readiness
// flags. Property 5: the response enumerates every failing flag.
func TestHealthReadyMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		startup      bool
		registry     bool
		shutdown     bool
		pool         bool
		nsjail       bool
		wantStatus   int
		wantFailures []string
	}{
		{"all up", true, true, false, true, true, http.StatusOK, nil},
		{"startup not done", false, true, false, true, true, http.StatusServiceUnavailable, []string{"startup"}},
		{"registry missing", true, false, false, true, true, http.StatusServiceUnavailable, []string{"Language_Registry"}},
		{"pool not ready", true, true, false, false, true, http.StatusServiceUnavailable, []string{"Worker_Pool"}},
		{"nsjail missing", true, true, false, true, false, http.StatusServiceUnavailable, []string{"NsJail"}},
		{"shutdown only", true, true, true, true, true, http.StatusServiceUnavailable, []string{"shutdown"}},
		{"all failing", false, false, true, false, false, http.StatusServiceUnavailable, []string{"startup", "Language_Registry", "Worker_Pool", "NsJail", "shutdown"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var startup, registry, shutdown, pool, nsjail atomic.Bool
			startup.Store(tc.startup)
			registry.Store(tc.registry)
			shutdown.Store(tc.shutdown)
			pool.Store(tc.pool)
			nsjail.Store(tc.nsjail)
			h := handlers.NewHealthHandler(&startup, &registry, &shutdown, &pool, &nsjail, "/usr/local/bin/nsjail")

			rec := httptest.NewRecorder()
			h.Ready(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response JSON invalid: %v\nbody=%s", err, rec.Body.String())
			}

			if tc.wantStatus == http.StatusOK {
				if body["ready"] != true {
					t.Errorf("ready = %v, want true", body["ready"])
				}
				if _, ok := body["failed_components"]; ok {
					t.Errorf("ready response should not carry failed_components: %v", body)
				}
				return
			}

			if body["ready"] != false {
				t.Errorf("ready = %v, want false", body["ready"])
			}
			arr, ok := body["failed_components"].([]any)
			if !ok {
				t.Fatalf("failed_components missing or wrong type: %v", body)
			}
			if len(arr) != len(tc.wantFailures) {
				t.Fatalf("failed_components length = %d, want %d (%v)", len(arr), len(tc.wantFailures), arr)
			}
			for i, want := range tc.wantFailures {
				entry, ok := arr[i].(map[string]any)
				if !ok {
					t.Fatalf("failed_components[%d] not an object: %v", i, arr[i])
				}
				if entry["component"] != want {
					t.Errorf("failed_components[%d].component = %v, want %q", i, entry["component"], want)
				}
				if reason, ok := entry["reason"].(string); !ok || reason == "" {
					t.Errorf("failed_components[%d].reason missing", i)
				}
				if want == "NsJail" {
					if entry["path"] != "/usr/local/bin/nsjail" {
						t.Errorf("expected NsJail path override check but got = %v", entry["path"])
					}
				}
			}
		})
	}
}

// TestInfoBodyShape asserts /info responds with exactly the documented
// field set and never leaks extra fields. Property 6.
func TestInfoBodyShape(t *testing.T) {
	t.Parallel()
	h := handlers.NewInfoHandler("goboxd", "v0.1.0", 4, []string{"py3", "cpp"})
	rec := httptest.NewRecorder()
	h.Info(rec, httptest.NewRequest(http.MethodGet, "/info", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v\nbody=%s", err, rec.Body.String())
	}
	wantKeys := map[string]struct{}{
		"service_name":     {},
		"version":          {},
		"languages":        {},
		"worker_pool_size": {},
	}
	if len(body) != len(wantKeys) {
		t.Errorf("/info has %d keys (%v), want exactly %d", len(body), body, len(wantKeys))
	}
	for k := range body {
		if _, ok := wantKeys[k]; !ok {
			t.Errorf("/info exposed unexpected key %q", k)
		}
	}
	if body["service_name"] != "goboxd" || body["version"] != "v0.1.0" {
		t.Errorf("identity fields wrong: %v", body)
	}
	if body["worker_pool_size"].(float64) != 4 {
		t.Errorf("worker_pool_size = %v, want 4", body["worker_pool_size"])
	}
	langs, ok := body["languages"].([]any)
	if !ok || len(langs) != 2 {
		t.Errorf("languages = %v, want [py3 cpp]", body["languages"])
	}
}

// TestInfoNilLanguagesEmptyArray asserts the constructor's nil-coalesce
// behaviour: a nil slice MUST emit an empty JSON array, not "null".
func TestInfoNilLanguagesEmptyArray(t *testing.T) {
	t.Parallel()
	h := handlers.NewInfoHandler("goboxd", "dev", 0, nil)
	rec := httptest.NewRecorder()
	h.Info(rec, httptest.NewRequest(http.MethodGet, "/info", nil))

	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"languages":[]`) {
		t.Errorf("body missing empty languages array: %s", body)
	}
	if strings.Contains(string(body), `"languages":null`) {
		t.Error("body emitted null for languages; want []")
	}
}

// TestRunStub asserts the Phase 1 stub returns the documented body.
func TestRunStub(t *testing.T) {
	t.Parallel()
	h := handlers.NewRunHandler()
	rec := httptest.NewRecorder()
	h.Run(rec, httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(`{}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body != `{"error":"not_implemented"}` {
		t.Errorf("body = %q", body)
	}
}
