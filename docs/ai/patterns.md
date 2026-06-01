# Implementation Patterns

Patterns recorded here are present in the repository code today. Each
pattern points to the file and (where useful) the line range where it is
applied.

---

## Pattern: Observer interface for cross-package side effects

**Context:**
The architecture spec mandates an acyclic dependency graph between
`internal/` packages (design.md §5, Property 32, enforced by
`tests/structure_test.go`). Components in `internal/runner` and
`internal/worker` need to emit metrics and log entries, but cannot
import `internal/metrics` or `internal/log` without breaking the graph.

**Pattern:**
The component declares a small interface naming each side-effect event
it produces, defaults to a no-op implementation, and accepts an injected
observer through its constructor. The single place that knows about both
the component and the metrics/log packages — `cmd/goboxd/observers.go` —
implements the interface and wires it in at startup.

**Where we used it:**
- `internal/runner/runner.go` — `type SandboxObserver interface`
  (`OnCleanupFailure`, `OnNsJailFailure`); default `nopSandboxObserver{}`
- `internal/runner/workspace.go` — `type WorkspaceObserver interface`
  (`OnIsolationViolation`)
- `internal/runner/reaper.go` — `type ReaperObserver interface`
  (`OnReap`, `OnReapFailure`)
- `internal/worker/pool.go` — `type Observer interface`
  (`OnQueueDepthChanged`, `OnSubmissionRejected`)
- `cmd/goboxd/observers.go` — concrete implementations bridging the four
  interfaces to `*metrics.Collector` and `*slog.Logger`

---

## Pattern: Nil-safe metric methods via early-return guard

**Context:**
Architecture Property 25 requires that observer (metrics) emission
failure never alters the originating request's `Execution_Result`.
Callers — including handlers in test setups that pass a nil collector —
must not panic.

**Pattern:**
Every method on `*metrics.Collector` begins with
`if c == nil || c.<vec> == nil { return }`. Callers can pass a nil
receiver or a receiver whose internal vector failed to register, and
the call becomes a no-op rather than a panic.

**Where we used it:**
`internal/metrics/metrics.go` lines 254–365 — applies to every
increment/observe/set method:
- `IncDroppedLog`, `IncDroppedMetric`, `IncSecurityRejection`,
  `IncRunRequest`, `ObserveRunDuration`, `SetQueueDepth`,
  `IncSandboxCleanupFailure`, `IncWorkspaceIsolationViolation`,
  `IncStartupPrereqFailure`, `IncOrphanWorkspaceReaped`,
  `IncUnsafeFilename`, `IncUnknownPlaceholder`

The pattern is exercised by dedicated tests in
`internal/metrics/metrics_test.go`:
- `TestSecurityRejectionsCounterNilSafe`
- `TestRunMetricsNilSafe`
- `TestStartupPrereqFailuresCounterNilSafe`
- `TestOrphanWorkspaceReapedCounterNilSafe`

---

## Pattern: Pure-function core, side effects at the orchestrator seam

**Context:**
Several runtime decisions need to be deterministic and unit-testable in
isolation: argv construction, status classification, output capture
size handling. Pulling I/O, time, and goroutines into these functions
would make them untestable as pure rules.

**Pattern:**
Decision logic lives in pure functions over plain structs. The
orchestrator (`internal/runner/runner.go`) is the only site that calls
both the pure functions and the side-effecting ones (filesystem,
`os/exec`, goroutines).

**Where we used it:**
- `internal/runner/argv.go` — `BuildArgv(in ArgvInput) ([]string, error)`
  is a pure function (Property 17 verified by
  `argv_property_test.go`)
- `internal/runner/classifier.go` — `Classify(in ClassifierInput) Status`
  is a pure deterministic total function (Property 19)
- `internal/runner/capture.go` — `Capture(r io.Reader, limit int)
  (CaptureResult, error)` is a streaming function with no global state
  (Property 18, 38)
- `internal/runner/runner.go` `(*SandboxRunner).Run` is the orchestrator
  that composes them with workspace allocation, exec launch, and
  cleanup

---

## Pattern: Substitutable seam via interface for test injection

**Context:**
Subprocess launch (`os/exec`) and filesystem traversal are not
deterministic at unit-test time. Forcing real subprocess invocation in
unit tests would couple them to nsjail availability.

**Pattern:**
The package declares a small interface; production code returns the
default implementation; tests inject a stub. Interfaces are minimal —
just the surface needed for the call site.

**Where we used it:**
- `internal/runner/exec.go` — `type ExecLauncher interface { Launch(...)
  (ExecOutcome, error) }`. Default is `defaultExecLauncher{}`; tests
  inject stubs through `SandboxConfig.Exec`. Applied in
  `internal/runner/runner_test.go` and `runner.go` line 191
- `internal/runner/reaper.go` — `type ReaperFS interface` (with
  `ReadDir`, `Stat`); tests substitute the in-memory `memFS` from
  `reaper_test.go`

---

## Pattern: Build-tag-based test segregation

**Context:**
Integration tests require nsjail and Linux namespaces, which are not
available in every developer environment. Mixing them with fast unit
tests would block local development.

**Pattern:**
Integration tests carry `//go:build integration` as the very first line.
`make test` selects unit + property tests; `make integration` adds
`-tags=integration` to pull in the integration set. Integration tests
themselves call `t.Skip()` when nsjail is absent.

**Where we used it:**
All 19 files under `tests/integration/` carry the tag, including the
helpers in `tests/integration/helper_test.go` and
`tests/integration/main_test.go`. Examples:
- `tests/integration/py3_hello_test.go`
- `tests/integration/cpp_hello_test.go`
- `tests/integration/metrics_scrape_test.go`

The Makefile defines:
- `make test` → `go test ./...` inside the `tools` compose service
- `make integration` → `go test -tags=integration ./tests/...` inside
  the `tools` compose service

---

## Pattern: Property-based tests paired with table-driven tests

**Context:**
Several correctness properties (17, 18, 19, 36, 37, 38, 39 per the
implementation-spec classification matrix) are universally quantified
("for any input …") and cannot be adequately validated by hand-picked
example tests.

**Pattern:**
Each component covered by a property has both a `*_test.go` (table-
driven examples + boundary cases) and a `*_property_test.go` (rapid
generators + the property statement). Both files share the package and
neither carries a build tag, so `make test` runs both.

**Where we used it:**
- `internal/runner/argv.go` ↔ `argv_test.go` + `argv_property_test.go`
- `internal/runner/capture.go` ↔ `capture_test.go` +
  `capture_property_test.go`
- `internal/runner/classifier.go` ↔ `classifier_test.go` +
  `classifier_property_test.go`
- `internal/runner/reaper.go` ↔ `reaper_test.go` +
  `reaper_property_test.go`
- `internal/runner/workspace.go` ↔ `workspace_test.go` +
  `workspace_property_test.go`
- `internal/worker/pool.go` ↔ `pool_test.go` + `pool_property_test.go`

---

## Pattern: Deferred cleanup with panic-safe re-raise

**Context:**
`Sandbox_Job_Directory` cleanup must run on every termination mode of
the request lifecycle: success, classified failure, panic, ctx
cancellation, shutdown signal (Architecture Req 24.4). Forgetting any
mode produces a host filesystem leak.

**Pattern:**
The orchestrator installs a single `defer` that captures
`recover()`. If a panic occurred, cleanup runs and the panic is
re-raised so the caller still observes it. If no panic occurred,
cleanup runs and the result is returned normally.

**Where we used it:**
`internal/runner/runner.go` lines 162–169 in `(*SandboxRunner).Run`:

    defer func() {
        if rec := recover(); rec != nil {
            r.observeCleanup(ws, ws.Cleanup())
            panic(rec)
        }
        r.observeCleanup(ws, ws.Cleanup())
    }()

---

## Pattern: Validate-once-at-load-and-again-at-request-time

**Context:**
Architecture Property 35: filename and placeholder safety must be
checked at both registry-load time (fail-fast, exit non-zero) and at
request time (return `INTERNAL_ERROR` for the single request). One-shot
validation at load time alone misses runtime corruption or registry
hot-reload; one-shot at request time alone delays a startup-detectable
fault to the first request.

**Pattern:**
The validator function lives once in `internal/security/`. Both
`internal/registry/` (load time) and `internal/runner/argv.go`
(request time) call the same function and route the result to their
appropriate failure mode (process exit vs `INTERNAL_ERROR`).

**Where we used it:**
- `internal/security/filename.go` — `ValidateBasename`,
  `IsAllowedPlaceholder`
- `internal/registry/registry.go` — calls both during YAML load
- `internal/runner/argv.go` — `validateInput()` re-checks
  `SourceFilename`, `BinaryFilename`, `StdinFile`;
  `expandPlaceholders()` re-checks every placeholder against
  `IsAllowedPlaceholder`

---

## Pattern: Structured error types with `Reason`, `Detail`, `Cause`

**Context:**
Failures need to be machine-routable (rule-name labels for metrics, log
fields for operators) and human-readable (error messages). A bare
`errors.New` loses the structure; a custom error per failure mode keeps
it.

**Pattern:**
A struct error with explicit fields, `Error()` formatting them into a
single message, `Is()` matching against a sentinel, and `Unwrap()`
exposing any underlying cause for `errors.Is` / `errors.As` traversal.

**Where we used it:**
- `internal/runner/argv.go` — `type ArgvError struct { Reason, Detail
  string; Cause error }` with `Error()`, `Is(target)`, `Unwrap()`.
  Sentinel: `ErrInvalidArgvInput`
- `internal/security/filename.go` — `FilenameError`, `PlaceholderError`
  with similar shape, used as `Cause` inside `ArgvError`
- `internal/runner/workspace.go` — `WorkspaceIsolationError`
  carrying `expected_path`, `received_path`

---

## Pattern: Configuration snapshot, immutable after startup

**Context:**
Architecture Req 13.6 requires startup-time validation of every config
key. Re-reading config per request would allow live drift and complicate
testing.

**Pattern:**
`internal/config` exposes a typed `*Config` struct populated once in
`Load()`. Every consumer (`cmd/goboxd/main.go` wires them) receives the
snapshot at construction time and treats it as read-only thereafter.

**Where we used it:**
- `internal/config/config.go` — `type Config struct { ... }`,
  `func Load(env, yamlPath) (*Config, error)`
- `cmd/goboxd/main.go` — calls `config.Load` once at startup and
  passes derived values into runner, worker, validator, and handler
  constructors

---

## Pattern: Test classification enforcement via package-layout test

**Context:**
Architecture Property 32 (acyclic dependency graph) and the test
classification rule (unit vs property vs integration files) must be
enforced mechanically rather than by reviewer attention.

**Pattern:**
A non-tagged test file at `tests/structure_test.go` parses
`go list -deps ./...` output and asserts that every observed edge
matches the declared adjacency table from
`.kiro/specs/goboxd-architecture/design.md` §5.

**Where we used it:**
`tests/structure_test.go` (198 lines, no build tag, runs under
`make test`).
