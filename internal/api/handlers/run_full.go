package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/thesouldev/goboxd/internal/registry"
	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/security"
	"github.com/thesouldev/goboxd/internal/worker"
)

// FullRunHandler is the production POST /run handler.
//
// It replaces the Phase 1 stub (which still ships under the same
// package as RunHandler) by composing the Wave A security validator,
// the registry lookup, the Wave D worker pool, and the Wave C
// SandboxRunner via the worker.Job channel.
//
// Wave E's main wiring picks one of {RunHandler, FullRunHandler} at
// startup; the handler types live side-by-side so the swap is a single
// route-binding change in cmd/goboxd/main.go.
type FullRunHandler struct {
	deps RunDeps
}

// RunDeps is the set of seams the run handler depends on. Each is an
// interface so unit tests can substitute a stub; production wires
// concrete types from internal/security, internal/registry, and
// internal/worker.
type RunDeps struct {
	// Logger emits one INFO completion or rejection log entry per
	// request. The handler never logs Code_Submission payload bytes
	// (Property 27).
	Logger *slog.Logger

	// Validator runs the architecture §13 rule catalog. Production
	// wires security.Validate via a small adapter; tests inject a stub.
	Validator RunValidator

	// Registry resolves language ids to Language_Definitions.
	Registry RunRegistry

	// Pool accepts validated submissions for asynchronous execution.
	Pool RunPool

	// Metrics records the architecture §14 catalog. Nil-safe.
	Metrics RunMetrics

	// SizeBounds carries the request-size caps the validator enforces.
	SizeBounds security.SizeBounds

	// CeilingsLookup resolves per-language Resource_Limits ceilings
	// for the validator.
	CeilingsLookup security.CeilingsLookup

	// Now returns the current time. Defaults to time.Now; tests pin a
	// deterministic clock.
	Now func() time.Time

	// MaxBodyBytes is the upper bound on the JSON request body. The
	// upstream API server already applies a body cap; this defends
	// the handler when invoked via httptest in isolation.
	MaxBodyBytes int64
}

// RunValidator is the security-validator surface the handler calls.
type RunValidator interface {
	Validate(sub security.Submission, sizes security.SizeBounds, lookup security.LanguageDefinitionLookup, ceilings security.CeilingsLookup) error
}

// RunRegistry is the registry surface the handler calls.
type RunRegistry interface {
	Get(id string) (registry.Definition, bool)
	List() []string
}

// RunPool is the worker-pool surface the handler calls.
type RunPool interface {
	Submit(ctx context.Context, job worker.Job) error
}

// RunMetrics is the metrics observer surface. Every method is best-effort
// and MUST NOT alter the originating request's outcome.
type RunMetrics interface {
	IncRunRequest(language, status string)
	ObserveRunDuration(seconds float64)
	IncSecurityRejection(rule string)
	IncUnsafeFilename(source string)
	IncUnknownPlaceholder(source string)
}

// errInvalidDeps is returned by NewFullRunHandler when the supplied
// deps are incomplete. Implementation detail; not exported.
var errInvalidDeps = errors.New("handlers: invalid RunDeps")

// NewFullRunHandler validates deps and returns the handler.
func NewFullRunHandler(deps RunDeps) (*FullRunHandler, error) {
	if deps.Validator == nil {
		return nil, fmt.Errorf("%w: Validator is nil", errInvalidDeps)
	}
	if deps.Registry == nil {
		return nil, fmt.Errorf("%w: Registry is nil", errInvalidDeps)
	}
	if deps.Pool == nil {
		return nil, fmt.Errorf("%w: Pool is nil", errInvalidDeps)
	}
	if deps.Metrics == nil {
		deps.Metrics = nopRunMetrics{}
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.MaxBodyBytes <= 0 {
		deps.MaxBodyBytes = 1 << 20 // 1 MiB
	}
	return &FullRunHandler{deps: deps}, nil
}

// nopRunMetrics is the default RunMetrics; all methods are no-ops so
// handlers don't need to check for nil.
type nopRunMetrics struct{}

func (nopRunMetrics) IncRunRequest(string, string)    {}
func (nopRunMetrics) ObserveRunDuration(float64)      {}
func (nopRunMetrics) IncSecurityRejection(string)     {}
func (nopRunMetrics) IncUnsafeFilename(string)        {}
func (nopRunMetrics) IncUnknownPlaceholder(string)    {}

// codeSubmission is the JSON wire shape from architecture §"Data Models".
//
// Optional fields use pointers so the handler can distinguish "not
// present in the body" from "present with the zero value" — a
// distinction the security validator depends on for the
// resource_limit_exceeded rule.
type codeSubmission struct {
	Language       string                 `json:"language"`
	Source         *string                `json:"source"`
	Stdin          *string                `json:"stdin,omitempty"`
	ResourceLimits *codeResourceLimits    `json:"resource_limits,omitempty"`
}

type codeResourceLimits struct {
	WallTimeS    *int `json:"wall_time_s,omitempty"`
	CPUTimeS     *int `json:"cpu_time_s,omitempty"`
	MemoryMB     *int `json:"memory_mb,omitempty"`
	ProcessCount *int `json:"process_count,omitempty"`
	OutputSizeMB *int `json:"output_size_mb,omitempty"`
}

// executionResultBody is the JSON wire shape from architecture
// §"Data Models" Execution_Result.
type executionResultBody struct {
	Status          string `json:"status"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	DurationMS      int64  `json:"duration_ms"`
}

// errorEnvelope is the canonical 4xx response shape (architecture §13).
type errorEnvelope struct {
	Error      string `json:"error"`
	Field      string `json:"field,omitempty"`
	Language   string `json:"language,omitempty"`
	Limit      string `json:"limit,omitempty"`
	LimitBytes int    `json:"limit_bytes,omitempty"`
	Requested  int    `json:"requested,omitempty"`
	Max        int    `json:"max,omitempty"`
	QueueMax   int    `json:"queue_max,omitempty"`
	WorkersMax int    `json:"workers_max,omitempty"`
}

// ServeHTTP implements http.Handler.
//
// Lifecycle (architecture §11 "Run Handler Lifecycle"):
//
//   1. Content-Type guard — non-JSON → 400.
//   2. Body decode (≤ MaxBodyBytes) — failure → 400 malformed_submission.
//   3. Adapt registry to security.LanguageDefinitionLookup.
//   4. Run security.Validate — failure → 400 with rule-specific envelope.
//   5. Build runner.Job from registry definition + submission overrides.
//   6. Submit to Pool — failure → 429 capacity_exhausted.
//   7. Wait for Outcome.
//   8. Translate Outcome to ExecutionResult JSON, write 200.
//
// Property 25 (observer-failure isolation) holds: every metrics/log
// call is best-effort.
func (h *FullRunHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := h.deps.Now()
	requestID := r.Header.Get("X-Request-Id")

	if !isJSONContentType(r) {
		h.writeError(w, http.StatusBadRequest, errorEnvelope{Error: "invalid_content_type"})
		h.deps.Metrics.IncRunRequest("", string(runner.StatusInternalError))
		h.logRejection(requestID, "invalid_content_type", start)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, h.deps.MaxBodyBytes)
	defer func() { _ = r.Body.Close() }()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		// MaxBytesReader returns *http.MaxBytesError when the body
		// exceeds MaxBodyBytes; we treat any read error as malformed.
		h.writeError(w, http.StatusBadRequest, errorEnvelope{Error: string(security.RuleMalformedSubmission), Field: "body"})
		h.deps.Metrics.IncSecurityRejection(string(security.RuleMalformedSubmission))
		h.logRejection(requestID, string(security.RuleMalformedSubmission), start)
		return
	}

	var cs codeSubmission
	if err := json.Unmarshal(body, &cs); err != nil {
		h.writeError(w, http.StatusBadRequest, errorEnvelope{Error: string(security.RuleMalformedSubmission), Field: "body"})
		h.deps.Metrics.IncSecurityRejection(string(security.RuleMalformedSubmission))
		h.logRejection(requestID, string(security.RuleMalformedSubmission), start)
		return
	}

	sub := submissionFromBody(cs)
	lookup := registryLookup{r: h.deps.Registry}
	if vErr := h.deps.Validator.Validate(sub, h.deps.SizeBounds, lookup, h.deps.CeilingsLookup); vErr != nil {
		h.writeRejection(w, vErr)
		var rj *security.Rejection
		if errors.As(vErr, &rj) {
			h.deps.Metrics.IncSecurityRejection(string(rj.Rule))
			h.logRejection(requestID, string(rj.Rule), start)
		}
		return
	}

	def, ok := h.deps.Registry.Get(sub.Language)
	if !ok {
		// Should never happen — the validator already checked language
		// membership — but defend the path so the handler stays total.
		h.writeError(w, http.StatusInternalServerError, errorEnvelope{Error: "registry_missing"})
		h.deps.Metrics.IncRunRequest(sub.Language, string(runner.StatusInternalError))
		return
	}

	job := buildRunnerJob(def, sub, cs)
	stdinBytes := []byte(nil)
	if sub.PresentStdin {
		stdinBytes = []byte(*cs.Stdin)
	}

	resCh := make(chan worker.Outcome, 1)
	wj := worker.Job{
		LanguageID: sub.Language,
		Run:        job,
		Stdin:      stdinBytes,
		Ctx:        r.Context(),
		Result:     resCh,
	}
	if err := h.deps.Pool.Submit(r.Context(), wj); err != nil {
		if errors.Is(err, worker.ErrCapacityExhausted) {
			h.writeError(w, http.StatusTooManyRequests, errorEnvelope{Error: "capacity_exhausted"})
			h.deps.Metrics.IncRunRequest(sub.Language, "REJECTED")
			h.logRejection(requestID, "capacity_exhausted", start)
			return
		}
		if errors.Is(err, worker.ErrPoolStopped) {
			h.writeError(w, http.StatusServiceUnavailable, errorEnvelope{Error: "shutting_down"})
			h.deps.Metrics.IncRunRequest(sub.Language, string(runner.StatusInternalError))
			return
		}
		h.writeError(w, http.StatusInternalServerError, errorEnvelope{Error: "submit_failed"})
		h.deps.Metrics.IncRunRequest(sub.Language, string(runner.StatusInternalError))
		return
	}

	select {
	case out := <-resCh:
		h.writeOutcome(w, sub.Language, out, start, requestID)
	case <-r.Context().Done():
		// Client disconnected; we still let the worker finish but
		// stop trying to write a response.
		h.deps.Metrics.IncRunRequest(sub.Language, string(runner.StatusInternalError))
		return
	}
}


// writeOutcome translates a worker.Outcome to the architecture-spec
// JSON response and updates metrics/log observers.
func (h *FullRunHandler) writeOutcome(w http.ResponseWriter, language string, out worker.Outcome, start time.Time, requestID string) {
	if out.Err != nil {
		// Inspect for runner validation errors to increment specific metrics and log details
		var argvErr *runner.ArgvError
		if errors.As(out.Err, &argvErr) {
			if argvErr.Reason == "unsafe_filename" {
				h.deps.Metrics.IncUnsafeFilename("runtime_validation")
				var fe *security.FilenameError
				if errors.As(argvErr.Cause, &fe) {
					val := fe.Value
					if security.HasControlBytes(val) {
						val = fmt.Sprintf("length=%d", len(fe.Value))
					}
					fieldName := "source_filename"
					if strings.Contains(argvErr.Detail, "BinaryFilename") {
						fieldName = "binary_filename"
					} else if strings.Contains(argvErr.Detail, "StdinFile") {
						fieldName = "stdin_file"
					}
					h.deps.Logger.Error("unsafe_filename",
						"event", "unsafe_filename",
						"field", fieldName,
						"language_id", language,
						"value", val,
						"reason", fe.Reason,
						"request_id", requestID,
					)
				}
			} else if argvErr.Reason == "unknown_placeholder" || argvErr.Reason == "unterminated_placeholder" {
				h.deps.Metrics.IncUnknownPlaceholder("runtime_validation")
				var pe *security.PlaceholderError
				if errors.As(argvErr.Cause, &pe) {
					h.deps.Logger.Error("unknown_placeholder",
						"event", "unknown_placeholder",
						"language_id", language,
						"args_entry", pe.ArgsEntry,
						"placeholder", pe.Placeholder,
						"reason", "unknown_placeholder",
						"request_id", requestID,
					)
				}
			}
		}

		// Err is reserved for "the runner could not produce a
		// classified result at all" (e.g. workspace allocation
		// failed). Surface as INTERNAL_ERROR with empty stdout/stderr.
		body := executionResultBody{
			Status:     string(runner.StatusInternalError),
			DurationMS: int64(h.deps.Now().Sub(start).Milliseconds()),
		}
		writeJSON(w, http.StatusOK, body)
		h.deps.Metrics.IncRunRequest(language, string(runner.StatusInternalError))
		h.deps.Metrics.ObserveRunDuration(h.deps.Now().Sub(start).Seconds())
		h.logCompletion(requestID, language, string(runner.StatusInternalError), start)
		return
	}
	res := out.Result
	body := executionResultBody{
		Status:          string(res.Status),
		ExitCode:        res.ExitCode,
		Stdout:          string(res.Stdout),
		Stderr:          string(res.Stderr),
		StdoutTruncated: res.StdoutTruncated,
		StderrTruncated: res.StderrTruncated,
		DurationMS:      res.DurationMS,
	}
	writeJSON(w, http.StatusOK, body)
	h.deps.Metrics.IncRunRequest(language, string(res.Status))
	h.deps.Metrics.ObserveRunDuration(h.deps.Now().Sub(start).Seconds())
	h.logCompletion(requestID, language, string(res.Status), start)
}

// writeRejection translates a security.Rejection to the architecture-spec
// 400 envelope.
func (h *FullRunHandler) writeRejection(w http.ResponseWriter, err error) {
	var rj *security.Rejection
	if !errors.As(err, &rj) {
		h.writeError(w, http.StatusBadRequest, errorEnvelope{Error: string(security.RuleMalformedSubmission)})
		return
	}
	env := errorEnvelope{Error: string(rj.Rule)}
	switch rj.Rule {
	case security.RuleMalformedSubmission:
		env.Field = rj.Field
	case security.RuleLanguageNotRegistered:
		env.Language = rj.Language
	case security.RuleSourceSizeExceeded, security.RuleStdinSizeExceeded:
		env.LimitBytes = rj.LimitBytes
	case security.RuleResourceLimitExceeded:
		env.Limit = string(rj.Limit)
		env.Requested = rj.Requested
		env.Max = rj.Max
	}
	h.writeError(w, http.StatusBadRequest, env)
}

// writeError emits a 4xx envelope.
func (h *FullRunHandler) writeError(w http.ResponseWriter, status int, env errorEnvelope) {
	writeJSON(w, status, env)
}

// logCompletion emits one INFO completion log entry per request.
//
// Property 27: never includes Code_Submission payload bytes.
func (h *FullRunHandler) logCompletion(requestID, language, status string, start time.Time) {
	dur := h.deps.Now().Sub(start)
	h.deps.Logger.Info("run_completed",
		"event", "run_completed",
		"request_id", requestID,
		"language", language,
		"status", status,
		"duration_ms", dur.Milliseconds(),
	)
}

// logRejection emits one INFO rejection log entry per pre-enqueue
// rejection.
func (h *FullRunHandler) logRejection(requestID, reason string, start time.Time) {
	dur := h.deps.Now().Sub(start)
	h.deps.Logger.Info("run_rejected",
		"event", "run_rejected",
		"request_id", requestID,
		"rejection_reason", reason,
		"duration_ms", dur.Milliseconds(),
	)
}

// isJSONContentType reports whether the request declares JSON.
func isJSONContentType(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	mime := ct
	if i := strings.Index(ct, ";"); i >= 0 {
		mime = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(mime), "application/json")
}

// submissionFromBody adapts the wire shape into the security.Submission
// the validator inspects.
func submissionFromBody(cs codeSubmission) security.Submission {
	sub := security.Submission{Language: cs.Language}
	if cs.Source != nil {
		sub.PresentSource = true
		sub.Source = *cs.Source
		sub.SourceLen = len(*cs.Source)
	}
	if cs.Stdin != nil {
		sub.PresentStdin = true
		sub.Stdin = *cs.Stdin
		sub.StdinLen = len(*cs.Stdin)
	}
	if cs.ResourceLimits != nil {
		rl := cs.ResourceLimits
		if rl.WallTimeS != nil {
			sub.Limits.PresentWallTimeS = true
			sub.Limits.WallTimeS = *rl.WallTimeS
		}
		if rl.CPUTimeS != nil {
			sub.Limits.PresentCPUTimeS = true
			sub.Limits.CPUTimeS = *rl.CPUTimeS
		}
		if rl.MemoryMB != nil {
			sub.Limits.PresentMemoryMB = true
			sub.Limits.MemoryMB = *rl.MemoryMB
		}
		if rl.ProcessCount != nil {
			sub.Limits.PresentProcCount = true
			sub.Limits.ProcessCount = *rl.ProcessCount
		}
		if rl.OutputSizeMB != nil {
			sub.Limits.PresentOutputSize = true
			sub.Limits.OutputSizeMB = *rl.OutputSizeMB
		}
	}
	return sub
}

// buildRunnerJob composes runner.Job from the registry definition and
// the submission. Resource_Limits overrides apply on top of the
// registry-default limits; the validator already capped them against
// the per-language ceilings, so the handler simply applies whichever
// the submission supplied.
func buildRunnerJob(def registry.Definition, sub security.Submission, cs codeSubmission) runner.Job {
	limits := runner.Limits{
		WallTimeS:    def.Run.Limits.WallTimeS,
		CPUTimeS:     def.Run.Limits.CPUTimeS,
		MemoryMB:     def.Run.Limits.MemoryMB,
		ProcessCount: def.Run.Limits.ProcessCount,
		OutputSizeMB: def.Run.Limits.OutputSizeMB,
	}
	if cs.ResourceLimits != nil {
		rl := cs.ResourceLimits
		if rl.WallTimeS != nil {
			limits.WallTimeS = *rl.WallTimeS
		}
		if rl.CPUTimeS != nil {
			limits.CPUTimeS = *rl.CPUTimeS
		}
		if rl.MemoryMB != nil {
			limits.MemoryMB = *rl.MemoryMB
		}
		if rl.ProcessCount != nil {
			limits.ProcessCount = *rl.ProcessCount
		}
		if rl.OutputSizeMB != nil {
			limits.OutputSizeMB = *rl.OutputSizeMB
		}
	}

	job := runner.Job{
		LanguageID:      def.ID,
		SourceFilename:  def.SourceFilename,
		BinaryFilename:  def.BinaryFilename,
		Source:          sub.Source,
		EffectiveLimits: limits,
		RunTemplate: runner.CommandTemplate{
			Command: def.Run.Command,
			Args:    def.Run.Args,
		},
	}
	if def.HasCompile() {
		job.CompileTemplate = runner.CommandTemplate{
			Command: def.Compile.Command,
			Args:    def.Compile.Args,
		}
		job.CompileLimits = runner.Limits{
			WallTimeS:    def.Compile.Limits.WallTimeS,
			CPUTimeS:     def.Compile.Limits.CPUTimeS,
			MemoryMB:     def.Compile.Limits.MemoryMB,
			ProcessCount: def.Compile.Limits.ProcessCount,
			OutputSizeMB: def.Compile.Limits.OutputSizeMB,
		}
	}
	if sub.PresentStdin {
		job.StdinFile = "stdin.bin"
	}
	return job
}

// registryLookup adapts RunRegistry to security.LanguageDefinitionLookup.
type registryLookup struct{ r RunRegistry }

func (l registryLookup) Lookup(id string) bool {
	_, ok := l.r.Get(id)
	return ok
}
