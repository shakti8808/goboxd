package metrics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/thesouldev/goboxd/internal/api/handlers"
	"github.com/thesouldev/goboxd/internal/metrics"
	"github.com/thesouldev/goboxd/internal/registry"
	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/security"
	"github.com/thesouldev/goboxd/internal/worker"
)

type failingRegisterer struct{}

func (failingRegisterer) Register(c prometheus.Collector) error {
	return errors.New("registration failed")
}

func (failingRegisterer) MustRegister(cs ...prometheus.Collector) {
	panic("registration failed")
}

func (failingRegisterer) Unregister(c prometheus.Collector) bool {
	return false
}

type mockPool struct {
	out worker.Outcome
}

func (p *mockPool) Submit(_ context.Context, job worker.Job) error {
	go func() {
		job.Result <- p.out
	}()
	return nil
}

type mockValidator struct{}

func (mockValidator) Validate(_ security.Submission, _ security.SizeBounds, _ security.LanguageDefinitionLookup, _ security.CeilingsLookup) error {
	return nil
}

type mockRegistry struct{}

func (mockRegistry) Get(id string) (registry.Definition, bool) {
	return registry.Definition{
		ID:             "py3",
		SourceFilename: "main.py",
		Run: registry.Step{
			Command: "/usr/bin/python3",
			Args:    []string{"main.py"},
		},
	}, true
}

func (mockRegistry) List() []string { return []string{"py3"} }

type mockCeilings struct{}

func (mockCeilings) CeilingsFor(string) security.ResourceCeilings {
	return security.ResourceCeilings{WallTimeS: 60, CPUTimeS: 60, MemoryMB: 1024, ProcessCount: 64, OutputSizeMB: 64}
}

func TestObserverIsolation(t *testing.T) {
	// 1. Construct Collector with a custom Registerer that fails registration
	c := metrics.NewWithRegisterer("goboxd", "dev", failingRegisterer{}, prometheus.NewRegistry())

	// Verify that failing registration registers fallback dropped metrics
	if c.DroppedMetricsCount() == 0 {
		t.Errorf("expected fallback dropped metrics to be > 0 due to registration failures, got 0")
	}

	// 2. Setup FullRunHandler with the failing metrics collector
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	pool := &mockPool{out: worker.Outcome{Result: runner.ExecutionResult{
		Status: runner.StatusOK, ExitCode: 0, Stdout: []byte("success"),
	}}}

	h, err := handlers.NewFullRunHandler(handlers.RunDeps{
		Logger:         logger,
		Validator:      mockValidator{},
		Registry:       mockRegistry{},
		Pool:           pool,
		Metrics:        c,
		SizeBounds:     security.SizeBounds{MaxSourceSizeBytes: 1024, MaxStdinSizeBytes: 1024},
		CeilingsLookup: mockCeilings{},
		MaxBodyBytes:   1024,
	})
	if err != nil {
		t.Fatalf("failed to build handler: %v", err)
	}

	// 3. Issue a POST /run request
	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(`{"language":"py3","source":"print('success')"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// Verify the request still completes successfully (Property 25)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}

	if body["status"] != "OK" {
		t.Errorf("expected status 'OK', got %v", body["status"])
	}
	if body["stdout"] != "success" {
		t.Errorf("expected stdout 'success', got %v", body["stdout"])
	}

	// Verify that the dropped metrics counter has incremented even more
	if c.DroppedMetricsCount() == 0 {
		t.Errorf("expected dropped metrics count to be non-zero")
	}
}
