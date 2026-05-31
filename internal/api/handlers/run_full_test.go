package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/thesouldev/goboxd/internal/api/handlers"
	"github.com/thesouldev/goboxd/internal/registry"
	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/security"
	"github.com/thesouldev/goboxd/internal/worker"
)

// stubValidator returns a configurable rejection.
type stubValidator struct{ rejection error }

func (s stubValidator) Validate(_ security.Submission, _ security.SizeBounds, _ security.LanguageDefinitionLookup, _ security.CeilingsLookup) error {
	return s.rejection
}

// realValidator wraps the production security.Validate so the
// happy-path test exercises real rule ordering.
type realValidator struct{}

func (realValidator) Validate(sub security.Submission, sizes security.SizeBounds, lookup security.LanguageDefinitionLookup, ceilings security.CeilingsLookup) error {
	return security.Validate(sub, sizes, lookup, ceilings)
}


// stubRegistry mirrors the architecture §12 worked example for `py3`.
type stubRegistry struct{}

func (stubRegistry) Get(id string) (registry.Definition, bool) {
	if id != "py3" {
		return registry.Definition{}, false
	}
	return registry.Definition{
		ID:             "py3",
		SourceFilename: "main.py",
		Run: registry.Step{
			Command: "/usr/bin/python3",
			Args:    []string{"main.py"},
			Limits: registry.ResourceLimits{
				WallTimeS: 10, CPUTimeS: 10, MemoryMB: 256, ProcessCount: 16, OutputSizeMB: 4,
			},
		},
	}, true
}
func (stubRegistry) List() []string { return []string{"py3"} }

// stubCeilings provides the architecture-spec defaults so the
// validator's resource_limit_exceeded rule has somewhere to compare.
type stubCeilings struct{}

func (stubCeilings) CeilingsFor(string) security.ResourceCeilings {
	return security.ResourceCeilings{WallTimeS: 60, CPUTimeS: 60, MemoryMB: 1024, ProcessCount: 64, OutputSizeMB: 64}
}

// stubPool implements RunPool with a configurable result and submit
// rejection.
type stubPool struct {
	mu        sync.Mutex
	submitted []worker.Job
	out       worker.Outcome
	submitErr error
}

func (p *stubPool) Submit(_ context.Context, job worker.Job) error {
	if p.submitErr != nil {
		return p.submitErr
	}
	p.mu.Lock()
	p.submitted = append(p.submitted, job)
	p.mu.Unlock()
	go func(j worker.Job) {
		j.Result <- p.out
	}(job)
	return nil
}

// recordingMetrics captures every observer call.
type recordingMetrics struct {
	mu              sync.Mutex
	runRequests     []runRequestEvent
	durations       []float64
	rejections      []string
}

type runRequestEvent struct{ Language, Status string }

func (m *recordingMetrics) IncRunRequest(language, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runRequests = append(m.runRequests, runRequestEvent{language, status})
}
func (m *recordingMetrics) ObserveRunDuration(seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.durations = append(m.durations, seconds)
}
func (m *recordingMetrics) IncSecurityRejection(rule string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejections = append(m.rejections, rule)
}

// newHandler builds a FullRunHandler with sensible Wave D defaults
// and the supplied stubs.
func newHandler(t *testing.T, val handlers.RunValidator, pool handlers.RunPool, out worker.Outcome) (*handlers.FullRunHandler, *recordingMetrics, *bytes.Buffer) {
	t.Helper()
	if val == nil {
		val = realValidator{}
	}
	logBuf := &bytes.Buffer{}
	metrics := &recordingMetrics{}
	if sp, ok := pool.(*stubPool); ok && out.Result.Status != "" {
		sp.out = out
	}
	h, err := handlers.NewFullRunHandler(handlers.RunDeps{
		Logger:         slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})),
		Validator:      val,
		Registry:       stubRegistry{},
		Pool:           pool,
		Metrics:        metrics,
		SizeBounds:     security.SizeBounds{MaxSourceSizeBytes: 1 << 20, MaxStdinSizeBytes: 1 << 20},
		CeilingsLookup: stubCeilings{},
		MaxBodyBytes:   1 << 20,
	})
	if err != nil {
		t.Fatalf("NewFullRunHandler: %v", err)
	}
	return h, metrics, logBuf
}

// post issues a POST against h with the given body and Content-Type.
func post(h http.Handler, body string, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}


// TestFullRunHandlerHappyPath exercises the success path end-to-end:
// real validator + stub registry + stub pool returning a successful
// Outcome → 200 with the documented JSON envelope.
func TestFullRunHandlerHappyPath(t *testing.T) {
	t.Parallel()
	pool := &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{
		Status: runner.StatusOK, ExitCode: 0, Stdout: []byte("hi"),
	}}}
	h, metrics, _ := newHandler(t, nil, pool, worker.Outcome{Result: runner.ExecutionResult{
		Status: runner.StatusOK, Stdout: []byte("hi"),
	}})

	rec := post(h, `{"language":"py3","source":"print('hi')"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["status"] != "OK" {
		t.Errorf("status = %v, want OK", body["status"])
	}
	if body["stdout"] != "hi" {
		t.Errorf("stdout = %v, want hi", body["stdout"])
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.runRequests) != 1 || metrics.runRequests[0].Status != "OK" {
		t.Errorf("metrics runRequests = %v", metrics.runRequests)
	}
	if len(metrics.durations) != 1 {
		t.Errorf("metrics durations = %v, want 1", metrics.durations)
	}
}

// TestFullRunHandlerRejectionMatrix covers every documented validator
// rejection rule (Property 24).
func TestFullRunHandlerRejectionMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		body      string
		ruleLabel string
		field     string
	}{
		{"missing source", `{"language":"py3"}`, string(security.RuleMalformedSubmission), "source"},
		{"unknown language", `{"language":"rust","source":"x"}`, string(security.RuleLanguageNotRegistered), ""},
		{"empty source", `{"language":"py3","source":""}`, string(security.RuleMalformedSubmission), "source"},
		{"resource limit", `{"language":"py3","source":"x","resource_limits":{"memory_mb":4096}}`, string(security.RuleResourceLimitExceeded), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{Status: runner.StatusOK}}}
			h, metrics, _ := newHandler(t, nil, pool, worker.Outcome{})

			rec := post(h, tc.body, "application/json")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if body["error"] != tc.ruleLabel {
				t.Errorf("error = %v, want %q", body["error"], tc.ruleLabel)
			}
			metrics.mu.Lock()
			rejections := append([]string{}, metrics.rejections...)
			metrics.mu.Unlock()
			if len(rejections) != 1 || rejections[0] != tc.ruleLabel {
				t.Errorf("rejections = %v, want exactly [%q]", rejections, tc.ruleLabel)
			}
		})
	}
}

// TestFullRunHandlerCapacityExhausted asserts the 429 path.
func TestFullRunHandlerCapacityExhausted(t *testing.T) {
	t.Parallel()
	pool := &stubPool{submitErr: worker.ErrCapacityExhausted}
	h, metrics, _ := newHandler(t, nil, pool, worker.Outcome{})

	rec := post(h, `{"language":"py3","source":"x"}`, "application/json")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "capacity_exhausted" {
		t.Errorf("error = %v, want capacity_exhausted", body["error"])
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.runRequests) != 1 || metrics.runRequests[0].Status != "REJECTED" {
		t.Errorf("metrics runRequests = %v", metrics.runRequests)
	}
}

// TestFullRunHandlerInternalErrorPassthrough confirms a runner that
// reports INTERNAL_ERROR still returns HTTP 200 with the status field
// set; the architecture spec maps sandbox-classified failures to 200.
func TestFullRunHandlerInternalErrorPassthrough(t *testing.T) {
	t.Parallel()
	pool := &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{
		Status: runner.StatusInternalError, ExitCode: -1,
	}}}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	rec := post(h, `{"language":"py3","source":"x"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "INTERNAL_ERROR" {
		t.Errorf("status = %v, want INTERNAL_ERROR", body["status"])
	}
}

// TestFullRunHandlerMalformedJSON asserts the 400 malformed_submission
// path for non-JSON bodies.
func TestFullRunHandlerMalformedJSON(t *testing.T) {
	t.Parallel()
	pool := &stubPool{}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	rec := post(h, `{not-json`, "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != string(security.RuleMalformedSubmission) {
		t.Errorf("error = %v, want %q", body["error"], security.RuleMalformedSubmission)
	}
}

// TestFullRunHandlerWrongContentType asserts the invalid_content_type
// guard.
func TestFullRunHandlerWrongContentType(t *testing.T) {
	t.Parallel()
	pool := &stubPool{}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	rec := post(h, `{}`, "text/plain")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "invalid_content_type" {
		t.Errorf("error = %v, want invalid_content_type", body["error"])
	}
}

// TestFullRunHandlerOversizeBody asserts the 1 MiB MaxBodyBytes guard.
func TestFullRunHandlerOversizeBody(t *testing.T) {
	t.Parallel()
	pool := &stubPool{}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	huge := strings.Repeat("a", (1<<20)+1024)
	rec := post(h, `{"language":"py3","source":"`+huge+`"}`, "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestFullRunHandlerLogsRedacted asserts Property 27: no source or
// stdin bytes leak into the log surface at INFO+.
func TestFullRunHandlerLogsRedacted(t *testing.T) {
	t.Parallel()
	pool := &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{Status: runner.StatusOK}}}
	h, _, logBuf := newHandler(t, nil, pool, worker.Outcome{})

	source := "secret_payload_xyz_42"
	body := `{"language":"py3","source":"` + source + `","stdin":"alpha_beta_stdin"}`
	rec := post(h, body, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(logBuf.String(), source) {
		t.Errorf("log surface leaked source bytes: %s", logBuf.String())
	}
	if strings.Contains(logBuf.String(), "alpha_beta_stdin") {
		t.Errorf("log surface leaked stdin bytes: %s", logBuf.String())
	}
}

// TestFullRunHandlerPoolStopped asserts the 503 path when the pool has
// been drained.
func TestFullRunHandlerPoolStopped(t *testing.T) {
	t.Parallel()
	pool := &stubPool{submitErr: worker.ErrPoolStopped}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	rec := post(h, `{"language":"py3","source":"x"}`, "application/json")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestFullRunHandlerSubmitGenericFailure asserts unexpected pool errors
// surface as 500.
func TestFullRunHandlerSubmitGenericFailure(t *testing.T) {
	t.Parallel()
	pool := &stubPool{submitErr: errors.New("disk on fire")}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	rec := post(h, `{"language":"py3","source":"x"}`, "application/json")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestFullRunHandlerOutcomeError covers Outcome.Err pass-through:
// a worker that reports a non-nil Err produces an INTERNAL_ERROR 200
// with empty stdout/stderr.
func TestFullRunHandlerOutcomeError(t *testing.T) {
	t.Parallel()
	pool := &stubPool{out: worker.Outcome{Err: errors.New("workspace fail")}}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	rec := post(h, `{"language":"py3","source":"x"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "INTERNAL_ERROR" {
		t.Errorf("status = %v, want INTERNAL_ERROR", body["status"])
	}
	if body["stdout"] != "" || body["stderr"] != "" {
		t.Errorf("stdout/stderr leaked: %v / %v", body["stdout"], body["stderr"])
	}
}


// TestNewFullRunHandlerValidatesDeps walks the required-field
// rejection cases.
func TestNewFullRunHandlerValidatesDeps(t *testing.T) {
	t.Parallel()
	full := func() handlers.RunDeps {
		return handlers.RunDeps{
			Validator:      realValidator{},
			Registry:       stubRegistry{},
			Pool:           &stubPool{},
			Metrics:        &recordingMetrics{},
			SizeBounds:     security.SizeBounds{MaxSourceSizeBytes: 1024, MaxStdinSizeBytes: 1024},
			CeilingsLookup: stubCeilings{},
		}
	}
	cases := []struct {
		name string
		mut  func(*handlers.RunDeps)
	}{
		{"nil validator", func(d *handlers.RunDeps) { d.Validator = nil }},
		{"nil registry", func(d *handlers.RunDeps) { d.Registry = nil }},
		{"nil pool", func(d *handlers.RunDeps) { d.Pool = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := full()
			tc.mut(&deps)
			_, err := handlers.NewFullRunHandler(deps)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// TestNewFullRunHandlerAppliesDefaults confirms nil Metrics / Logger /
// Now / MaxBodyBytes default sensibly.
func TestNewFullRunHandlerAppliesDefaults(t *testing.T) {
	t.Parallel()
	deps := handlers.RunDeps{
		Validator:      realValidator{},
		Registry:       stubRegistry{},
		Pool:           &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{Status: runner.StatusOK}}},
		SizeBounds:     security.SizeBounds{MaxSourceSizeBytes: 1024, MaxStdinSizeBytes: 1024},
		CeilingsLookup: stubCeilings{},
		// no Metrics, no Logger, no Now, no MaxBodyBytes
	}
	h, err := handlers.NewFullRunHandler(deps)
	if err != nil {
		t.Fatalf("NewFullRunHandler: %v", err)
	}
	rec := post(h, `{"language":"py3","source":"x"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestFullRunHandlerStdinPath exercises the stdin-present branch of
// submissionFromBody so coverage covers the optional-fields code.
func TestFullRunHandlerStdinPath(t *testing.T) {
	t.Parallel()
	pool := &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{Status: runner.StatusOK}}}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	rec := post(h, `{"language":"py3","source":"x","stdin":"hello"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.submitted) != 1 {
		t.Fatalf("submitted = %d, want 1", len(pool.submitted))
	}
	if string(pool.submitted[0].Stdin) != "hello" {
		t.Errorf("Stdin = %q, want hello", pool.submitted[0].Stdin)
	}
	if pool.submitted[0].Run.StdinFile == "" {
		t.Error("Run.StdinFile not set when stdin present")
	}
}

// TestFullRunHandlerResourceLimitOverrides exercises the resource-limit
// override branches in submissionFromBody and buildRunnerJob.
func TestFullRunHandlerResourceLimitOverrides(t *testing.T) {
	t.Parallel()
	pool := &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{Status: runner.StatusOK}}}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	body := `{"language":"py3","source":"x","resource_limits":{"wall_time_s":5,"cpu_time_s":5,"memory_mb":128,"process_count":8,"output_size_mb":2}}`
	rec := post(h, body, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.submitted) != 1 {
		t.Fatalf("submitted = %d, want 1", len(pool.submitted))
	}
	lim := pool.submitted[0].Run.EffectiveLimits
	if lim.WallTimeS != 5 || lim.CPUTimeS != 5 || lim.MemoryMB != 128 || lim.ProcessCount != 8 || lim.OutputSizeMB != 2 {
		t.Errorf("EffectiveLimits = %+v, want overrides applied", lim)
	}
}

// TestFullRunHandlerCompileTemplate exercises buildRunnerJob's
// HasCompile branch.
func TestFullRunHandlerCompileTemplate(t *testing.T) {
	t.Parallel()
	pool := &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{Status: runner.StatusOK}}}
	h, _, _ := newHandler(t, nil, pool, worker.Outcome{})

	// Override the registry to one with a compile block.
	cppDeps := handlers.RunDeps{
		Validator:      realValidator{},
		Registry:       stubCPPRegistry{},
		Pool:           pool,
		Metrics:        &recordingMetrics{},
		SizeBounds:     security.SizeBounds{MaxSourceSizeBytes: 1 << 20, MaxStdinSizeBytes: 1 << 20},
		CeilingsLookup: stubCeilings{},
	}
	hCpp, err := handlers.NewFullRunHandler(cppDeps)
	if err != nil {
		t.Fatalf("NewFullRunHandler: %v", err)
	}
	rec := post(hCpp, `{"language":"cpp","source":"int main(){}"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.submitted) != 1 {
		t.Fatalf("submitted = %d, want 1", len(pool.submitted))
	}
	if pool.submitted[0].Run.CompileTemplate.Command == "" {
		t.Error("CompileTemplate.Command empty; want populated for cpp")
	}
	_ = h
}

// stubCPPRegistry mirrors the architecture §12 cpp worked example.
type stubCPPRegistry struct{}

func (stubCPPRegistry) Get(id string) (registry.Definition, bool) {
	if id != "cpp" {
		return registry.Definition{}, false
	}
	return registry.Definition{
		ID:             "cpp",
		SourceFilename: "main.cpp",
		BinaryFilename: "main",
		Compile: &registry.Step{
			Command: "/usr/bin/g++",
			Args:    []string{"-O2", "-o", "main", "main.cpp"},
			Limits: registry.ResourceLimits{
				WallTimeS: 20, CPUTimeS: 20, MemoryMB: 512, ProcessCount: 32, OutputSizeMB: 16,
			},
		},
		Run: registry.Step{
			Command: "./main",
			Args:    []string{},
			Limits: registry.ResourceLimits{
				WallTimeS: 10, CPUTimeS: 10, MemoryMB: 256, ProcessCount: 16, OutputSizeMB: 4,
			},
		},
	}, true
}
func (stubCPPRegistry) List() []string { return []string{"cpp"} }

// TestFullRunHandlerNopMetricsObserver exercises the default no-op
// metrics observer methods so 0%-coverage methods become 100%.
func TestFullRunHandlerNopMetricsObserver(t *testing.T) {
	t.Parallel()
	pool := &stubPool{out: worker.Outcome{Result: runner.ExecutionResult{Status: runner.StatusOK}}}
	h, err := handlers.NewFullRunHandler(handlers.RunDeps{
		Validator:      realValidator{},
		Registry:       stubRegistry{},
		Pool:           pool,
		// no Metrics — falls back to nopRunMetrics.
		SizeBounds:     security.SizeBounds{MaxSourceSizeBytes: 1024, MaxStdinSizeBytes: 1024},
		CeilingsLookup: stubCeilings{},
	})
	if err != nil {
		t.Fatalf("NewFullRunHandler: %v", err)
	}
	rec := post(h, `{"language":"py3","source":"x"}`, "application/json")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	// Trigger a rejection so IncSecurityRejection on the no-op runs.
	rec = post(h, `{"language":"rust","source":"x"}`, "application/json")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
