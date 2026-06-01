package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thesouldev/goboxd/internal/api/handlers"
	"github.com/thesouldev/goboxd/internal/config"
	goboxlog "github.com/thesouldev/goboxd/internal/log"
	"github.com/thesouldev/goboxd/internal/metrics"
	"github.com/thesouldev/goboxd/internal/registry"
	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/security"
	"github.com/thesouldev/goboxd/internal/worker"
)

// mockJobRunner stub that mimics nsjail behavior.
type mockJobRunner struct {
	runFunc func(ctx context.Context, job runner.Job, stdin []byte) (runner.ExecutionResult, error)
}

func (m mockJobRunner) Run(ctx context.Context, job runner.Job, stdin []byte) (runner.ExecutionResult, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, job, stdin)
	}
	return runner.ExecutionResult{
		Status: runner.StatusOK,
		Stdout: []byte("hi\n"),
	}, nil
}

func TestMainWiringSmoke(t *testing.T) {
	// Create lookup with clean defaults.
	mockLookup := func(k string) (string, bool) {
		switch k {
		case "LANGUAGE_REGISTRY_PATH":
			// Point to registry YAML.
			return "../../configs/language_registry.yaml", true
		case "LOG_LEVEL":
			return "ERROR", true
		default:
			return "", false
		}
	}

	cfg, err := config.Load(mockLookup)
	if err != nil {
		t.Fatalf("failed to load test config: %v", err)
	}

	log := goboxlog.New(nil, slog.LevelError)
	mc := metrics.New("goboxd-test", "dev")

	reg, err := registry.Load(cfg.LanguageRegistryPath)
	if err != nil {
		t.Fatalf("failed to load registry: %v", err)
	}

	stubRunner := mockJobRunner{
		runFunc: func(ctx context.Context, job runner.Job, stdin []byte) (runner.ExecutionResult, error) {
			if job.LanguageID == "py3" && strings.Contains(job.Source, "print('hi')") {
				return runner.ExecutionResult{
					Status: runner.StatusOK,
					Stdout: []byte("hi\n"),
				}, nil
			}
			return runner.ExecutionResult{Status: runner.StatusRuntimeError}, nil
		},
	}

	poolCfg := worker.Config{
		Workers:      cfg.WorkerPoolSize,
		QueueLen:     cfg.WorkerPoolQueueLen,
		DrainTimeout: time.Duration(cfg.WorkerPoolDrainTimeoutS) * time.Second,
		Runner:       stubRunner,
		Observer:     poolObserver{metrics: mc},
	}
	pool, err := worker.New(poolCfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Drain(0)

	runDeps := handlers.RunDeps{
		Logger:         log,
		Validator:      validatorAdapter{},
		Registry:       reg,
		Pool:           pool,
		Metrics:        mc,
		SizeBounds: security.SizeBounds{
			MaxSourceSizeBytes: cfg.MaxSourceSizeBytes,
			MaxStdinSizeBytes:  cfg.MaxStdinSizeBytes,
		},
		CeilingsLookup: validatorCeilingsAdapter{cfg: cfg},
	}
	runH, err := handlers.NewFullRunHandler(runDeps)
	if err != nil {
		t.Fatalf("failed to construct full run handler: %v", err)
	}

	// Create test server.
	mux := http.NewServeMux()
	mux.HandleFunc("/run", runH.ServeHTTP)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Issue request.
	reqBody := `{"language":"py3","source":"print('hi')"}`
	resp, err := http.Post(server.URL+"/run", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /run request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status code = %d, want 200", resp.StatusCode)
	}

	var res map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	if res["status"] != "OK" {
		t.Errorf("status = %v, want OK", res["status"])
	}
	if res["stdout"] != "hi\n" {
		t.Errorf("stdout = %v, want \"hi\\n\"", res["stdout"])
	}
}

func TestReadyzWiringSmoke(t *testing.T) {
	var (
		startupCompleted       atomic.Bool
		languageRegistryLoaded atomic.Bool
		shutdownInProgress     atomic.Bool
		workerPoolReady        atomic.Bool
		nsjailPresent          atomic.Bool
	)

	healthH := handlers.NewHealthHandler(
		&startupCompleted,
		&languageRegistryLoaded,
		&shutdownInProgress,
		&workerPoolReady,
		&nsjailPresent,
		"/usr/local/bin/nsjail",
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", healthH.Ready)
	server := httptest.NewServer(mux)
	defer server.Close()

	// Initial check: not ready (components fail)
	resp, err := http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want 503", resp.StatusCode)
	}

	// Set ready states.
	startupCompleted.Store(true)
	languageRegistryLoaded.Store(true)
	workerPoolReady.Store(true)
	nsjailPresent.Store(true)

	resp2, err := http.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status code = %d, want 200", resp2.StatusCode)
	}
}
