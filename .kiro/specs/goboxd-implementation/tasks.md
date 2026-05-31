# Implementation Plan: goboxd-implementation

## Overview

This plan converts the architecture spec at `.kiro/specs/goboxd-architecture/` into a working Go binary. Every task below is a coding task. The architecture document at `.kiro/specs/goboxd-architecture/design.md` is authoritative for requirement IDs (prefix `A-`), correctness properties (numbers 1–42), and the package set under `cmd/goboxd/` and `internal/`. Implementation-spec requirements `I-1` through `I-6` come from `.kiro/specs/goboxd-implementation/requirements.md`. The existing `Dockerfile`, `docker-compose.yml`, and `Makefile` are not modified by any task (Req I-6, Req A-2.1).

Test artifact conventions are inherited from architecture §"Testing Strategy":
- Unit tests: `internal/<pkg>/*_test.go` (no build tag), selected by `make test`.
- Property tests: `internal/<pkg>/*_property_test.go` (no build tag, `pgregory.net/rapid`), selected by `make test`. Minimum 100 iterations per invocation (Req I-4.2).
- Integration tests: `tests/integration/*_test.go` carrying `//go:build integration`, selected by `make integration`. `t.Skip()` when `nsjail` is absent at `NSJAIL_PATH` (Req I-3.5).

All commands run inside the `tools` compose service per the existing `Makefile`.

## Tasks

### Phase 1 — HTTP skeleton

- [ ] 1. Bootstrap module hygiene and pin third-party dependencies
  - Path: `go.mod`, `go.sum`
  - Reqs: I-2.1, I-2.2
  - Properties: —
  - Depends on: —
  - Acceptance: `go.mod` declares the four modules `gopkg.in/yaml.v3`, `github.com/google/uuid`, `github.com/prometheus/client_golang`, `pgregory.net/rapid` with pinned versions; `go.sum` is consistent; `go mod tidy` is a no-op; the module path remains `github.com/thesouldev/goboxd` and the toolchain remains `go 1.23`.
  - Tests: a unit test `internal/version/deps_test.go` that runs `go list -m all` via `os/exec` (skipped under `-short`) is acceptable but not required; this task is verified primarily by `go mod tidy` producing no diff.

- [ ] 2. Implement `internal/version` constants
  - Path: `internal/version/version.go`
  - Reqs: A-6.1, A-15 (`SERVICE_NAME`, `BUILD_VERSION`)
  - Properties: 6 (read-only inputs to `/info`)
  - Depends on: —
  - Acceptance: package exports `Version string` (default `"dev"`) and `ServiceName string` (default `"goboxd"`); both are overridable via `-ldflags "-X github.com/thesouldev/goboxd/internal/version.Version=..."`; no internal imports.
  - Tests: `internal/version/version_test.go` asserts non-empty defaults and that `Version` is settable at link time (compile-time test of the override symbol path).

- [ ] 3. Implement `internal/config` env+YAML loader with range validation
  - Path: `internal/config/config.go`, `internal/config/load.go`, `internal/config/validate.go`
  - Reqs: A-13.1 through A-13.7, A-25.1, I-2.2 (yaml.v3 only); covers every key in architecture §15 including `SANDBOX_ROOT_DIR_OWNER_UID` and `SANDBOX_ROOT_DIR_PERMS_MAX`
  - Properties: 10 (Unit), 11 (PBT)
  - Depends on: 1
  - Acceptance: `Load(env map[string]string, yamlPath string) (*Config, error)` resolves env > YAML > default for every key in architecture §15; missing-required, out-of-range, or unparseable values return an error naming the offending key; `0775` accepted for `SANDBOX_ROOT_DIR_PERMS_MAX`, world-writable bit (`o+w`) rejected; immutable snapshot returned to callers.
  - Tests: `internal/config/config_test.go` (Property 10: every required key, every range-violation case enumerated as table-driven unit tests; emits structured error naming offending key); `internal/config/config_property_test.go` (Property 11 PBT: for arbitrary `(env_present, env_value, yaml_present, yaml_value, default_value)` tuples the resolved effective value matches the env > YAML > default precedence rule, ≥100 rapid iterations).

- [ ] 4. Implement `internal/log` slog-based JSON logger with redaction and request-scoped helper
  - Path: `internal/log/log.go`, `internal/log/redact.go`, `internal/log/timestamp.go`
  - Reqs: A-14.1, A-14.4, A-14.5, A-14.6, A-14.7, A-26.x (UUIDv4, ISO 8601 UTC ms), A-16
  - Properties: 26 (PBT), 27 (PBT)
  - Depends on: 1, 5
  - Acceptance: exports `New(level slog.Level, w io.Writer) *Logger`, `WithRequest(id uuid.UUID) *slog.Logger`, `NewRequestID() uuid.UUID`; emitted records are JSON with `ts` matching `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$` and `request_id` matching the UUIDv4 regex; the redaction set drops `source`, `stdin`, `stdout`, `stderr` keys (and any nested occurrence) at INFO+; on emission failure the dropped-log counter is incremented via `internal/metrics`.
  - Tests: `internal/log/log_test.go` (timestamp/UUID format unit tests; redaction set unit tests at every level); `internal/log/log_property_test.go` (Property 26: arbitrary level/event/fields → emitted entry always contains a UUIDv4 `request_id` and an ISO 8601 UTC ms `ts` and a non-negative integer `duration_ms` for completion entries; Property 27: arbitrary `Code_Submission` payload bytes never appear in the emitted byte stream regardless of level; ≥100 rapid iterations each).

- [ ] 5. Implement `internal/metrics` Phase 1 skeleton
  - Path: `internal/metrics/metrics.go`, `internal/metrics/expose.go`
  - Reqs: A-12.5, A-12.6, A-14.6
  - Properties: 25 (observer-failure isolation, Phase 1 surface)
  - Depends on: 1
  - Acceptance: exports a `Collector` carrying a `prometheus.Registry`; registers `goboxd_build_info{version,service}` (gauge, value 1), `goboxd_dropped_logs_total{reason}` (counter), `goboxd_dropped_metrics_total{reason}` (counter); exposes the registry over `Expose(http.ResponseWriter, *http.Request)` returning Prometheus text format (`Content-Type: text/plain; version=0.0.4`); recording calls are no-ops when the registry is unavailable and increment `goboxd_dropped_metrics_total`.
  - Tests: `internal/metrics/metrics_test.go` covers exposition format (mime type, contains `goboxd_build_info`), no-op behavior, and dropped-counter increments (Property 25 surface).

- [ ] 6. Implement `internal/security` Phase 1 partial — `ValidateBasename` and placeholder allowlist
  - Path: `internal/security/filename.go`, `internal/security/placeholders.go`
  - Reqs: A-21.1, A-21.5, A-22.3, A-22.4, A-22.7
  - Properties: 34 (PBT), 37 (placeholder allowlist constants)
  - Depends on: 1
  - Acceptance: exports `ValidateBasename(name string) error` rejecting empty input, `/`, `\`, `..` substring (anywhere), absolute path prefix, leading `.`, any codepoint < 0x20, and NUL byte; exports `AllowedPlaceholders = []string{"SOURCE_FILENAME","BINARY_FILENAME","STDIN_FILE"}` and `IsAllowedPlaceholder(name string) bool`; errors are typed (`ErrUnsafeFilename`, `ErrUnknownPlaceholder`) with structured fields including `reason`.
  - Tests: `internal/security/filename_test.go` (table-driven unit tests for every rejection clause + happy-path `main.py`, `main.cpp`, `main`); `internal/security/filename_property_test.go` (Property 34 PBT: for any string drawn from rapid string generators, `ValidateBasename` returns nil iff and only if the string contains none of the forbidden bytes/sequences — generator covers ASCII, UTF-8, control bytes, and arbitrary length; ≥100 rapid iterations).

- [ ] 7. Implement `internal/registry` YAML loader with basename + placeholder validation
  - Path: `internal/registry/registry.go`, `internal/registry/load.go`, `internal/registry/types.go`
  - Reqs: A-7.1 through A-7.7, A-8.1 through A-8.4, A-13.7, A-21.1, A-21.2, A-22.4
  - Properties: 7 (PBT), 8 (PBT), 9 (Unit), 35 (Unit, load-time half)
  - Depends on: 3, 4, 6
  - Acceptance: exports `Load(path string, log *slog.Logger) (*Registry, error)`, `Get(id string) (Definition, bool)`, `List() []Definition`; `Get` is case-sensitive exact-match, immutable, side-effect-free; `Load` calls `security.ValidateBasename` on every `source_filename` and `binary_filename`; `Load` rejects unknown `{{NAME}}` placeholders in `compile.args`/`run.args` (closed allowlist); duplicate `id`, missing required field, malformed YAML, and unreadable file each produce a non-nil error and a single ERROR log entry with the offending field/id and `event="registry_load_failed"`; required-field set per architecture §12.
  - Tests: `internal/registry/registry_test.go` (Property 9 + Property 35 load-time: malformed YAML, missing `id`, missing `run.command`, missing `run.limits.*`, duplicate id, unsafe filename, unknown placeholder — each must abort load with a structured error; happy path loads `py3` and `cpp` worked examples from architecture §12); `internal/registry/registry_property_test.go` (Property 7 PBT: arbitrary id strings → `Get` returns non-nil iff and only if id matches an entry exactly; calling `Get(id)` n times yields identical results and `List()` order is preserved across calls; Property 8 PBT: every entry returned by `List()` exposes non-empty `id`, `source_filename`, `run.command`, complete `run.limits` block within documented ranges, and — when `compile` is present — non-empty `compile.command` and complete `compile.limits`; ≥100 rapid iterations).

- [ ] 8. Implement `internal/api/handlers` Phase 1 stubs — `Health_Handler`, `Info_Handler`, `Run_Handler` (503 stub)
  - Path: `internal/api/handlers/health.go`, `internal/api/handlers/info.go`, `internal/api/handlers/run.go`, `internal/api/handlers/types.go`
  - Reqs: A-3.1 through A-3.4, A-5.1 through A-5.8, A-6.1 through A-6.4
  - Properties: 4 (Unit), 5 (Unit), 6 (Unit)
  - Depends on: 2, 4, 5, 7
  - Acceptance: `Health_Handler.Live` returns `200 {"status":"ok"}` always while listener is up; `Health_Handler.Ready` returns `200 {"ready":true}` iff `language_registry_loaded ∧ worker_pool_ready ∧ nsjail_present_and_executable ∧ ¬shutdown_in_progress`, otherwise `503` with `failed_components` enumerating *every* failing condition by name (`Language_Registry`, `Worker_Pool`, `NsJail`, `shutdown`); `Info_Handler.Info` returns `200` with exact field set `{service_name, version, languages, worker_pool_size}` sourced only from `internal/version`, `*registry.Registry`, and the configured pool size — no submission-derived values, no filesystem paths; `Run_Handler` (Phase 1 stub) returns `503 {"error":"not_implemented"}`; none of the four handlers enqueue, allocate sandbox, or spawn `nsjail`.
  - Tests: `internal/api/handlers/health_test.go` (Property 5: enumerate all 16 readiness state vectors, assert the response body's `failed_components` array equals the set of false conditions); `internal/api/handlers/info_test.go` (Property 6: snapshot the response body field set; reject any path or submission-derived value); `internal/api/handlers/run_test.go` (stub returns 503; Property 4: handler invocation never enqueues or allocates — verified via injected stub `Worker_Pool` and `Sandbox_Runner` recorders).

- [ ] 9. Implement `internal/api` `API_Server` — routing, body cap, 404/405 partition, signal-driven shutdown
  - Path: `internal/api/server.go`, `internal/api/router.go`
  - Reqs: A-3.5 through A-3.11, A-5.7, A-5.8, A-9.7 through A-9.9
  - Properties: 3 (Unit)
  - Depends on: 4, 5, 8
  - Acceptance: binds `0.0.0.0:8080` (`API_BIND_ADDR`); exact-path routing for `/run` (POST), `/healthz` (GET), `/readyz` (GET), `/info` (GET), `/metrics` (GET); 1 MiB body cap on `POST /run` (returns 400 on overage); unknown path → 404; recognised path with wrong method → 405 with `Allow` header listing supported methods; non-JSON Content-Type on `POST /run` → 400; signal-driven graceful shutdown (`SIGINT`, `SIGTERM`) calls `Worker_Pool.Drain` placeholder hook then closes the listener; `/readyz` flips to 503 once shutdown begins; `/healthz` continues 200 until the listener closes.
  - Tests: `internal/api/server_test.go` (Property 3: enumerate `(method, path)` partition — every recognised path × supported methods → success/handler invocation; recognised path × unsupported method → 405 with correct `Allow` header; unrecognised path × any method → 404; oversize body on `/run` → 400; `404` never returned for a recognised path).

- [ ] 10. Implement `cmd/goboxd` Phase 1 wiring + httptest end-to-end smoke
  - Path: `cmd/goboxd/main.go`
  - Reqs: I-1.4, A-3.6, A-5.1, A-5.2, A-6.1
  - Properties: 4 (smoke covers it transitively), 32 (boundary respected by import set)
  - Depends on: 2, 3, 4, 5, 6, 7, 8, 9, 12
  - Acceptance: `main()` loads config from env + optional YAML, initializes the logger, instantiates the metrics collector, loads the language registry from `LANGUAGE_REGISTRY_PATH`, builds the API server with the Phase 1 handlers, installs `SIGINT`/`SIGTERM` handlers, runs until shutdown, and exits with a non-zero status on any startup error; the binary serves `GET /healthz`, `GET /readyz`, `GET /info`, and `GET /metrics` with HTTP 200 within 1 second of process start; `POST /run` returns HTTP 503 `not_implemented` (Phase 1 stub).
  - Tests: `cmd/goboxd/main_test.go` (uses `httptest.NewServer` against the wired router with the default `language_registry.yaml` from task 12; asserts `/healthz`, `/readyz`, `/info`, `/metrics` all return 200 within 1 s; asserts `POST /run` returns 503 with body `{"error":"not_implemented"}`).

- [ ] 11. Add `tests/structure_test.go` enforcing architecture §5 dependency graph (Property 32)
  - Path: `tests/structure_test.go`
  - Reqs: A-16.4, A-16.5, A-16.6, I-2.3
  - Properties: 32 (Static)
  - Depends on: 10
  - Acceptance: package `structure_test` (no build tag, so selected by `make test`) parses `go list -f '{{.ImportPath}} {{.Imports}}' ./internal/...` output and fails if (a) any package edge violates the architecture §5 declared dependency table, or (b) the resulting graph contains any cycle (DFS topological-order check); the architecture's expected adjacency table is encoded as a literal in the test file.
  - Tests: this task **is** the test artifact (`tests/structure_test.go`). A negative self-test is permitted as a sub-table that mutates a copy of the adjacency map and asserts the cycle detector fires.

- [ ] 12. Ship default `configs/language_registry.yaml` with `py3` and `cpp` entries
  - Path: `configs/language_registry.yaml`
  - Reqs: A-7.1, A-8.1, A-8.2, A-8.3, A-8.4
  - Properties: 8 (loaded definitions expose all required fields), 34 (basenames pass `ValidateBasename`)
  - Depends on: 6, 7
  - Acceptance: file matches the schema enforced by `internal/registry`; `py3` entry has `id: py3`, `source_filename: main.py`, omits `compile`, `run.command: /usr/bin/python3`, `run.args: ["main.py"]`, `run.limits.*` per architecture §12 worked example; `cpp` entry has `id: cpp`, `source_filename: main.cpp`, `binary_filename: main`, full `compile.*` and `run.*` blocks per architecture §12 worked example; loading via `internal/registry.Load` against this file succeeds.
  - Tests: `internal/registry/default_yaml_test.go` (no build tag) loads the shipped file and asserts both entries present with all required fields (Property 8); confirms `ValidateBasename` accepts every filename (Property 34 surface).

### Phase 2 — `POST /run` end-to-end with `py3`

- [ ] 13. Implement `internal/security` full `Validate(submission)` rule catalog
  - Path: `internal/security/validator.go`, `internal/security/rules.go`
  - Reqs: A-11.1 through A-11.7, A-13.5
  - Properties: 24 (Unit)
  - Depends on: 3, 5, 6, 7
  - Acceptance: exports `Validate(sub Submission, cfg *config.Config, reg *registry.Registry) error` returning typed rejections (`malformed_submission`, `language_not_registered`, `source_size_exceeded`, `stdin_size_exceeded`, `resource_limit_exceeded`) with a structured detail payload (rule name, offending field, requested value, configured limit where applicable); on every rejection `metrics.IncSecurityRejection(rule)` is invoked; validator never enqueues, never allocates sandbox; 99th-percentile decision time ≤ 50 ms (Req A-11.7) — no allocations on the hot path beyond a single error value.
  - Tests: `internal/security/validator_test.go` (Property 24 unit table: per-rule rejection produces exact rule name + detail, 0 enqueue side effects via stub `Worker_Pool`, 0 sandbox allocations via stub `Sandbox_Runner`, exactly one `metrics.IncSecurityRejection` call per rejection); benchmark `BenchmarkValidate` covering the 50 ms p99 envelope.

- [ ] 14. Implement `internal/runner` argv builder (pure function)
  - Path: `internal/runner/argv.go`, `internal/runner/types.go`
  - Reqs: A-10.1, A-10.2, A-10.3, A-10.4, A-22.1, A-22.2, A-22.3, A-22.6, A-22.7
  - Properties: 17 (PBT), 36 (PBT), 37 (PBT)
  - Depends on: 3, 5, 6, 7
  - Acceptance: exports `BuildArgv(in ArgvInput) ([]string, error)` taking `(NsJailPath, Job, EnvAllowlist, BindMounts, SeccompPolicy)` and returning a fully-resolved argv with (a) namespace flags `--mode o`, user/PID/mount/network/IPC/UTS isolation, network disabled; (b) `--time_limit`, `--rlimit_cpu`, `--rlimit_as`, `--rlimit_nproc`, `--rlimit_fsize` set to the effective limits; (c) exactly one rw bind mount (the `JOB_DIR` at `/sandbox`); (d) every other host-path bind mount marked read-only; (e) placeholder substitution restricted to `{SOURCE_FILENAME, BINARY_FILENAME, STDIN_FILE}` — unknown placeholders return `ErrUnknownPlaceholder`; (f) no shell interpreter is invoked anywhere.
  - Tests: `internal/runner/argv_test.go` (table-driven unit tests for `py3` and `cpp` worked examples — exact argv strings); `internal/runner/argv_property_test.go` (Property 17 PBT: arbitrary `Job` with effective limits in their documented ranges produces an argv whose flag values equal the limits and whose mount policy holds; Property 36 PBT: arbitrary `Code_Submission.source` and `.stdin` byte sequences never appear as argv elements, env names, or env values; Property 37 PBT: arbitrary placeholder names — `{{X}}`, `{{SOURCE_FILENAME}}`, `{{BINARY_FILENAME_x}}`, etc. — cause `BuildArgv` to fail iff the name is not in the closed allowlist; ≥100 rapid iterations each).

- [ ] 15. Implement `internal/runner` workspace allocator with ownership invariant
  - Path: `internal/runner/workspace.go`
  - Reqs: A-24.1 through A-24.5
  - Properties: 39 (PBT), 41 (Unit)
  - Depends on: 4, 14
  - Acceptance: exports `AllocateWorkspace(root string, requestID uuid.UUID) (*Workspace, error)` creating `<root>/job-<UUIDv4>` (mode `0700`) using a cryptographically secure UUIDv4 source (`github.com/google/uuid` v4); `Workspace.Path()` is recorded once and used by every subsequent operation; exports `RefuseForeignPath(ws *Workspace, target string) error` returning `ErrWorkspaceIsolationViolation` (with `expected_path` and `received_path`) when `target != ws.Path()`; on violation `metrics.IncWorkspaceIsolationViolation()` is invoked and an ERROR log entry is emitted with `event="workspace_isolation_violation"`, `request_id`, `expected_path`, `received_path`.
  - Tests: `internal/runner/workspace_test.go` (Property 41 unit: every operation passing a non-matching path triggers refusal + metric increment + log entry); `internal/runner/workspace_property_test.go` (Property 39 PBT: for arbitrary pairs of allocations the resulting paths are distinct, both contain a UUIDv4 segment, and the path entropy is ≥128 bits — verified by uniqueness over 100 generated pairs and regex-conformance of the trailing UUID segment; ≥100 rapid iterations).

- [ ] 16. Implement `internal/runner` capture discarders (memory-bounded stdout/stderr)
  - Path: `internal/runner/capture.go`
  - Reqs: A-10.5, A-23.1 through A-23.7
  - Properties: 18 (Unit), 38 (PBT)
  - Depends on: 3, 14
  - Acceptance: exports `Capture(r io.Reader, limit int) (retained []byte, total int64, truncated bool, err error)` reading in chunks ≤16 KiB; while `len(retained) < limit`, copies bytes up to remaining capacity; once `total ≥ limit`, sets `truncated = true` and discards subsequent bytes streamingly without growing `retained`; total memory bounded by `limit + 16KiB + O(1)`; exposes wired hooks `STDOUT_CAPTURE_LIMIT_BYTES` / `STDERR_CAPTURE_LIMIT_BYTES` from `*config.Config`.
  - Tests: `internal/runner/capture_test.go` (Property 18 unit: exact `min(actual, limit)` retention; truncation flag set iff `actual > limit`; chunk size ≤16 KiB observed via instrumented reader); `internal/runner/capture_property_test.go` (Property 38 PBT: arbitrary `(B_out, limit)` with `B_out ∈ [0, limit*4]` and arbitrary chunk-arrival schedules → memory bound holds and `truncated` flag matches `B_out > limit`; ≥100 rapid iterations).

- [ ] 17. Implement `internal/runner` status classifier (pure function)
  - Path: `internal/runner/classifier.go`
  - Reqs: A-4.6, A-4.7, A-4.8, A-10.6, A-10.7
  - Properties: 19 (PBT)
  - Depends on: 14
  - Acceptance: exports `Classify(in ClassifierInput) Status` taking `(step_kind, exit_code, signal, nsjail_log_indicator, invocation_failure)` and returning exactly one of `OK | COMPILATION_ERROR | RUNTIME_ERROR | TIME_LIMIT_EXCEEDED | MEMORY_LIMIT_EXCEEDED | INTERNAL_ERROR` per the architecture §11 mapping table; the function is deterministic and total — identical inputs always produce identical outputs; no side effects.
  - Tests: `internal/runner/classifier_test.go` (table-driven unit covering every row of the §11 mapping table); `internal/runner/classifier_property_test.go` (Property 19 PBT: for arbitrary tuples, calling `Classify` twice yields the same status and the status is always in the enumerated set; ≥100 rapid iterations).

- [ ] 18. Implement `internal/runner` orchestrator — wire allocator + argv builder + classifier + discarders + cleanup
  - Path: `internal/runner/runner.go`, `internal/runner/exec.go`
  - Reqs: A-10.1, A-10.7, A-4.9, A-4.10, A-24.4
  - Properties: 16 (Integration covers it), 22 (Integration), 23 (Unit), 40 (Integration)
  - Depends on: 14, 15, 16, 17
  - Acceptance: exports `Sandbox_Runner.Run(ctx context.Context, job Job) (Execution_Result, error)` which (a) allocates the workspace; (b) materialises the source file inside `<JOB_DIR>` using `Language_Definition.source_filename` after re-applying `ValidateBasename` (Property 35 request-time half); (c) runs compile step (if `compile.command` is present) via `os/exec` invoking `nsjail` with the full argv from task 14; (d) launches with a 5 s `NSJAIL_LAUNCH_TIMEOUT_S` deadline (Req A-10.7); (e) runs the run step; (f) classifies the outcome; (g) calls `CleanupOnly(jobDir)` on every termination mode including success, classified failure, panic, ctx-cancellation, shutdown signal; (h) cleanup failure increments `goboxd_sandbox_cleanup_failures_total` and emits an ERROR log entry but does not alter the returned `Execution_Result`.
  - Tests: `internal/runner/runner_test.go` (Property 23 unit: with cleanup failure injected, the returned `Execution_Result` is byte-for-byte identical to the success-cleanup case; metric and log are observed via injected stubs; uses a stub `nsjail` binary on `$PATH` written to a tempdir and a stub argv for unit-level coverage of the orchestrator).

- [ ] 19. Implement `internal/runner` orphan reaper (periodic goroutine)
  - Path: `internal/runner/reaper.go`
  - Reqs: A-26.8, A-18.6
  - Properties: 22 surface (recovers cleanup failures), 40 (cleanup totality)
  - Depends on: 4, 5, 15
  - Acceptance: exports `StartReaper(ctx context.Context, root string, ttl time.Duration) error` running on a fixed interval; lists `<root>/job-*` and removes entries whose mtime is older than `SANDBOX_ORPHAN_TTL_S`; each successful removal increments `goboxd_orphan_workspace_reaped_total` and emits an INFO log entry with `event="orphan_workspace_reaped"`, `path`, `age_seconds`; failure to remove is logged + counted but does not stop the goroutine.
  - Tests: `internal/runner/reaper_test.go` pre-creates a backdated directory, runs one reaper tick, asserts removal + counter increment + log entry.

- [ ] 20. Implement `internal/worker` bounded FIFO `Worker_Pool` with drain semantics
  - Path: `internal/worker/pool.go`, `internal/worker/queue.go`
  - Reqs: A-9.1 through A-9.9, A-12.3
  - Properties: 12 (PBT), 13 (Unit), 14 (PBT), 15 (Unit), 30 (Unit)
  - Depends on: 3, 4, 5, 18
  - Acceptance: exports `Worker_Pool.Submit(job) (resultChan, error)` (synchronous accept/reject; `ErrCapacityExhausted` when both worker capacity and queue capacity are full); `Drain(timeout time.Duration) (drained int, cancelled int)` (T>0: wait up to T then cancel remainder; T=0: immediately cancel in-flight + discard queued); `QueueDepth() int`; size and queue length validated at startup (1..1024 / 0..10000 / 0..3600); on enqueue/dequeue updates `goboxd_worker_pool_queue_depth` via `metrics.SetQueueDepth(n)`.
  - Tests: `internal/worker/pool_test.go` (Property 13 unit: capacity-full submission rejected immediately and never retroactively accepted even after a worker idles; Property 15 unit: drain timing for T>0 and T=0 — bounded epsilon, no new accepts after Drain begins; Property 30 unit: queue-depth gauge equals the queue size after every enqueue/dequeue event); `internal/worker/pool_property_test.go` (Property 12 PBT: arbitrary submission/completion schedules → in-flight count never exceeds size and queued count never exceeds queue len; Property 14 PBT: arbitrary accepted job sequences `j_1..j_n` → workers begin executing in order `j_1..j_n`; ≥100 rapid iterations each).

- [ ] 21. Implement `internal/api/handlers.Run_Handler` full
  - Path: `internal/api/handlers/run.go`
  - Reqs: A-4.1 through A-4.11, A-12.1, A-12.2, A-12.4, A-14.1, A-14.7, A-16
  - Properties: 24 (Unit), 25 (Unit)
  - Depends on: 8, 13, 18, 20
  - Acceptance: decodes JSON body (≤1 MiB enforced upstream) into `Code_Submission`; calls `security.Validate`; on accept calls `Worker_Pool.Submit`; awaits `Execution_Result` from the result channel or a context cancellation; deferred cleanup invokes `Sandbox_Runner.CleanupOnly`; emits exactly one INFO completion log entry with `request_id`, `language`, `status`, `duration_ms`, `ts` (architecture §16); emits one INFO rejection log entry on validator/capacity rejection with `request_id`, `rejection_reason`, `ts`; increments `goboxd_run_requests_total{language,status}` (or `status="REJECTED"`), records `goboxd_run_duration_seconds`; observer-failure isolation per Property 25 (metric/log emission failure never alters the response).
  - Tests: `internal/api/handlers/run_test.go` (happy-path unit test with stub registry/security/worker/runner; per-failure-mode tests covering each rejection rule from architecture §13 → 400 with structured envelope; capacity-exhausted → 429; INTERNAL_ERROR from runner → 200 with status `INTERNAL_ERROR` and empty stdout/stderr; Property 24 + Property 25 inline assertions).

- [ ] 22. Wire Phase 2 packages into `cmd/goboxd`
  - Path: `cmd/goboxd/main.go`
  - Reqs: I-1.4, A-4.x, A-9.x, A-10.x
  - Properties: 16 (transitively covered by integration), 22 (transitively)
  - Depends on: 10, 13, 18, 19, 20, 21
  - Acceptance: `main()` constructs the full security validator (task 13), `Worker_Pool` (task 20), `Sandbox_Runner` (task 18) including the orphan reaper goroutine (task 19), and the full `Run_Handler` (task 21); `make integration` scenarios `py3-hello`, `unknown-language`, `oversize-source`, `queue-full`, `time-limit` pass against this binary (verified by task 24); the wiring respects the architecture §5 dependency graph (verified by task 11).
  - Tests: extends `cmd/goboxd/main_test.go` with the Phase 2 wiring path — `POST /run` with `language=py3` and `print('hi')` returns `status=OK` and `stdout="hi\n"` against a stub `Sandbox_Runner` that mimics nsjail behaviour (real nsjail comes through `make integration`).

- [ ] 23. Phase 2 startup prerequisite checks (`cmd/goboxd`)
  - Path: `cmd/goboxd/prereq.go`
  - Reqs: A-25.1, A-25.2, A-25.3, A-25.4, A-25.5, A-25.6
  - Properties: 42 (Integration)
  - Depends on: 22
  - Acceptance: before the HTTP listener opens, validates (in order) (a) sandbox root exists, is owned by `SANDBOX_ROOT_DIR_OWNER_UID`, has permissions ≤ `SANDBOX_ROOT_DIR_PERMS_MAX`, and is not world-writable (Req A-25.1); (b) every `SANDBOX_RO_MOUNTS` host path exists and is readable (Req A-25.2); (c) `NSJAIL_PATH` exists and is executable by the GoboxD process user (Req A-25.3); on any failure exits non-zero, increments `goboxd_startup_prereq_failures_total{prereq=…}` (`prereq ∈ {sandbox_root, ro_mount, nsjail_binary}`), emits an ERROR log entry `event="startup_prereq_failed"` naming the failed check + path + reason, and never opens the HTTP listener.
  - Tests: `cmd/goboxd/prereq_test.go` (table-driven unit: each of the three checks with a passing fixture and a per-failure-mode fixture; asserts non-zero exit return value + counter + log fields). Integration coverage shipped under task 24 (`startup_prereq_test.go`).

- [ ] 24. Phase 2 integration tests under `tests/integration/`
  - Path: `tests/integration/py3_hello_test.go`, `tests/integration/unknown_language_test.go`, `tests/integration/oversize_source_test.go`, `tests/integration/queue_full_test.go`, `tests/integration/time_limit_test.go`, `tests/integration/path_traversal_source_filename_test.go`, `tests/integration/invalid_filename_control_bytes_test.go`, `tests/integration/command_injection_via_source_test.go`, `tests/integration/shell_metachar_stdin_test.go`, `tests/integration/excessive_stdout_test.go`, `tests/integration/excessive_stderr_test.go`, `tests/integration/workspace_isolation_concurrent_test.go`, `tests/integration/orphan_workspace_reaped_test.go`, `tests/integration/startup_prereq_test.go`
  - Reqs: A-20.4, A-20.5, A-20.6, A-26.1 through A-26.8, I-3.3, I-3.5, I-4.1
  - Properties: 16, 20, 21, 22, 28 (partial), 40, 41, 42
  - Depends on: 12, 22, 23
  - Acceptance: every file carries `//go:build integration` and lives under `tests/integration/`; each test starts a real GoboxD process (or the in-process wired server with a real `nsjail` from the `nsjail-builder` Dockerfile stage) with the shipped `configs/language_registry.yaml`; tests `t.Skip()` when `nsjail` is absent at `NSJAIL_PATH`; scenarios:
    - `py3_hello_test.go`: `POST /run` with `language=py3` and `print('hi')` returns `status=OK` and `stdout="hi\n"` (Property 16);
    - `unknown_language_test.go`: `language=rust` returns 400 with `rule=language_not_registered`;
    - `oversize_source_test.go`: source > 1 MiB returns 400 with `rule=source_size_exceeded`;
    - `queue_full_test.go`: saturate workers + queue → next submission returns 429 with `capacity_exhausted`;
    - `time_limit_test.go`: `while True: pass` returns `status=TIME_LIMIT_EXCEEDED` (Property 21 conjugate, Property 22 cleanup);
    - `path_traversal_source_filename_test.go`: registry yaml with `source_filename: "../etc/passwd"` causes startup non-zero exit (Property 34 load-time);
    - `invalid_filename_control_bytes_test.go`: registry yaml with NUL/control byte in filename causes startup non-zero exit;
    - `command_injection_via_source_test.go`: `source` containing `$(rm -rf /)`, backticks, and shell metacharacters runs cleanly inside `nsjail` and never reaches a shell — argv contains only the templated values (Property 36);
    - `shell_metachar_stdin_test.go`: `stdin` containing shell metacharacters reaches the child as bytes and never reaches a shell;
    - `excessive_stdout_test.go`: child emits >`STDOUT_CAPTURE_LIMIT_BYTES` → response includes `stdout_truncated=true` and runner memory stays bounded (Property 38 surface);
    - `excessive_stderr_test.go`: same for stderr;
    - `workspace_isolation_concurrent_test.go`: two concurrent requests A and B — B's path differs from A's; cross-workspace access by harness triggers `goboxd_workspace_isolation_violation_total` increment (Property 41);
    - `orphan_workspace_reaped_test.go`: pre-create backdated `<SANDBOX_ROOT_DIR>/job-<UUIDv4>` → reaper removes it and increments `goboxd_orphan_workspace_reaped_total`;
    - `startup_prereq_test.go`: each of `sandbox_root`, `ro_mount`, `nsjail_binary` failures causes non-zero exit before the HTTP listener opens (Property 42).
  - Tests: this task **is** the test artifact set.

### Phase 3 — Full metrics + `cpp`

- [ ] 25. Implement `internal/metrics` full metric catalog (architecture §14)
  - Path: `internal/metrics/metrics.go`, `internal/metrics/expose.go` (extended)
  - Reqs: A-12.1 through A-12.6, A-14.6
  - Properties: 25 (Unit), 28 (Integration), 29 (Unit), 30 (Unit)
  - Depends on: 5
  - Acceptance: registers and exposes every metric in architecture §14: `goboxd_run_requests_total{language,status}` (counter), `goboxd_run_duration_seconds` (histogram with buckets `0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60` covering 0.01..60 s), `goboxd_worker_pool_queue_depth` (gauge), `goboxd_security_rejections_total{rule}` (counter), `goboxd_sandbox_cleanup_failures_total` (counter), `goboxd_unsafe_filename_total{source}` (counter), `goboxd_unknown_placeholder_total{source}` (counter), `goboxd_workspace_isolation_violation_total` (counter), `goboxd_orphan_workspace_reaped_total` (counter), `goboxd_startup_prereq_failures_total{prereq}` (counter); maximum staleness ≤1 s between update and `/metrics` exposition; observer-failure isolation per Property 25 — emission failure increments `goboxd_dropped_metrics_total` and never alters the originating request.
  - Tests: `internal/metrics/metrics_test.go` (Property 29 unit: every recorded duration falls into exactly one of the documented buckets; Property 30 unit: enqueue/dequeue events update the gauge to the current queue size); `internal/metrics/observer_isolation_test.go` (Property 25 unit: with the registry replaced by a failing stub, the wrapping handler still returns the original `Execution_Result` and the dropped-metrics counter increments).

- [ ] 26. Add `cpp` to default registry + plumb compile-step in `internal/runner`
  - Path: `configs/language_registry.yaml` (extended), `internal/runner/exec.go` (compile-step wiring)
  - Reqs: A-8.2, A-8.4, A-4.8, A-10.6
  - Properties: 8 (loaded definitions expose all required fields), 21 (Integration)
  - Depends on: 12, 14, 18
  - Acceptance: `configs/language_registry.yaml` includes the architecture §12 `cpp` worked example verbatim; `internal/runner/exec.go` invokes the compile step with `compile.command`, `binary_filename` substitution via `{{BINARY_FILENAME}}`, and `compile.limits.*` mapped to nsjail flags; on non-zero compile exit the run step is skipped and `Execution_Result.status = COMPILATION_ERROR` with `stderr` carrying the compiler output (truncated to `STDERR_CAPTURE_LIMIT_BYTES`).
  - Tests: `tests/integration/cpp_hello_test.go` (`POST /run` with `language=cpp` and a hello-world `main.cpp` returns `status=OK` with the expected stdout); `tests/integration/compile_error_test.go` (broken C++ source returns 200 with `status=COMPILATION_ERROR` and non-empty `stderr`; run step is never executed — Property 21).

- [ ] 27. Phase 3 metrics scrape consistency integration test
  - Path: `tests/integration/metrics_scrape_test.go`
  - Reqs: A-12.1, A-12.2, A-12.3, A-12.4, A-12.6
  - Properties: 28 (Integration)
  - Depends on: 24, 25, 26
  - Acceptance: `//go:build integration` test issues a deterministic request stream (e.g. 3× `py3` OK, 2× `cpp` OK, 1× `py3` TIME_LIMIT_EXCEEDED, 1× rejection of each rule from architecture §13) then scrapes `GET /metrics`; asserts `goboxd_run_requests_total{language="py3",status="OK"}=3`, `goboxd_run_requests_total{language="cpp",status="OK"}=2`, `goboxd_run_requests_total{language="py3",status="TIME_LIMIT_EXCEEDED"}=1`, `goboxd_run_duration_seconds_count` reflects the request count, `goboxd_worker_pool_queue_depth` is observable, and `goboxd_security_rejections_total{rule=…}` reflects the rejection stream — all within the ≤1 s staleness bound (Property 28).
  - Tests: this task **is** the test artifact.

### Final Acceptance

- [ ] 28. `make build` produces a static `goboxd` linux/amd64 binary
  - Path: (verification only — no source change)
  - Reqs: I-1.1, I-1.2, A-2.1
  - Properties: —
  - Depends on: 22, 25, 26
  - Acceptance: `make build` (which delegates to `docker compose build goboxd` and the `Dockerfile` `runtime` target) succeeds; the resulting `goboxd:dev` image contains `/usr/local/bin/goboxd` built with `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w"`; the binary is a statically-linked ELF for `linux/amd64`.
  - Tests: shell command `make build` exits 0; an inline check (`docker run --rm goboxd:dev /usr/local/bin/goboxd --version` if a `--version` flag is added later, otherwise inspect via `docker create`) confirms the binary exists.

- [ ] 29. `make test` passes (unit + property tests)
  - Path: (verification only)
  - Reqs: I-1.2, I-3.4, I-4.1, I-4.2
  - Properties: every property numbered 3, 5, 6, 7, 8, 10, 11, 12, 13, 14, 15, 17, 18, 19, 23, 24, 25, 26, 27, 29, 30, 34, 36, 37, 38, 39, 41 (every PBT+Unit-classified property)
  - Depends on: 22, 23, 25, 26, 27
  - Acceptance: `make test` (delegates to `go test ./...` inside the `tools` compose service) exits 0; every `*_test.go` and `*_property_test.go` file under `internal/`, plus `cmd/goboxd/main_test.go` and `tests/structure_test.go`, runs and passes; rapid PBTs run ≥100 iterations per invocation per Req I-4.2.
  - Tests: shell command `make test` exits 0.

- [ ] 30. `make integration` passes (all integration scenarios)
  - Path: (verification only)
  - Reqs: I-1.2, I-3.5, A-20.4, A-20.5, A-20.6
  - Properties: 16, 20, 21, 22, 28, 40, 41, 42
  - Depends on: 24, 27
  - Acceptance: `make integration` (delegates to `go test -tags=integration ./tests/...`) exits 0 with `nsjail` available on `NSJAIL_PATH`; every integration test in `tests/integration/` either passes or `t.Skip()`s with a documented skip reason (Req I-3.5 only permits `t.Skip` when `nsjail` is absent — for CI runs with `nsjail` present the suite must pass).
  - Tests: shell command `make integration` exits 0.

- [ ] 31. `make lint` passes
  - Path: (verification only)
  - Reqs: I-1.2
  - Properties: —
  - Depends on: 22, 25, 26
  - Acceptance: `make lint` (delegates to `golangci-lint run ./...`) exits 0 across all packages; default linters from the `golangci-lint` install in the `builder` Dockerfile stage are used; no in-repo `.golangci.yml` is added unless the existing repo already has one (none does).
  - Tests: shell command `make lint` exits 0.

- [ ] 32. Confirm `Dockerfile`, `docker-compose.yml`, `Makefile` are byte-for-byte unchanged
  - Path: (verification only)
  - Reqs: A-2.1, I-6.1, I-6.2
  - Properties: —
  - Depends on: 28
  - Acceptance: `git diff --exit-code -- Dockerfile docker-compose.yml Makefile` exits 0 against the spec-start commit; no implementation task touches these files at any point in Phase 1, Phase 2, or Phase 3.
  - Tests: a CI-style shell assertion `git diff --exit-code -- Dockerfile docker-compose.yml Makefile` returns 0; the assertion is also encoded as a comment in `tests/structure_test.go` (architecture-spec invariance note) but enforcement is via the shell check.

- [ ] 33. Confirm `go list ./...` shows the architecture §5 package set only and `tests/structure_test.go` passes
  - Path: (verification only)
  - Reqs: A-16.4, A-16.5, A-16.6, I-2.3
  - Properties: 32 (Static)
  - Depends on: 11, 22, 25, 26
  - Acceptance: `go list ./...` output (excluding `tests/...`) equals exactly the set `{cmd/goboxd, internal/api, internal/api/handlers, internal/config, internal/log, internal/metrics, internal/registry, internal/runner, internal/security, internal/version, internal/worker}` (module path prefix `github.com/thesouldev/goboxd/`); `tests/structure_test.go` (run by `make test`) passes; no extra `internal/...` package exists; no edge in the parsed dependency graph violates the architecture §5 adjacency table.
  - Tests: `tests/structure_test.go` is the live gate (created in task 11); a shell assertion `diff <(go list ./... | sort) <(echo expected | sort)` is also acceptable as a CI step.

## Notes

- Tasks 1–12 deliver Phase 1 (HTTP skeleton; `POST /run` returns 503). Tasks 13–24 deliver Phase 2 (`POST /run` end-to-end with `py3`). Tasks 25–27 deliver Phase 3 (full metrics catalog + `cpp`). Tasks 28–33 are final-acceptance verifications.
- No top-level task is marked optional. Every test artifact ships with its production code per architecture §"Testing Strategy" — there is no separate "tests are optional" sub-task.
- Unit and property tests live under `internal/<pkg>/`. Integration tests live under `tests/integration/` with `//go:build integration`. The structure-test gate (task 11) enforces this layout (Property 33 surface).
- `pgregory.net/rapid` PBTs run ≥100 iterations per invocation (Req I-4.2).
- `internal/version` has no automated tests beyond the smoke test in task 2; the package is compile-time constants only (architecture §5 test coverage class).
- Tasks 24 and 26 explicitly create integration-test files; task 27 creates the metrics-scrape integration test. Together they cover Property 28 plus the per-failure-mode integration scenarios from architecture §"Testing Strategy".
- The architecture document at `.kiro/specs/goboxd-architecture/design.md` is authoritative — any task that appears to require modifying that document or `Dockerfile` / `docker-compose.yml` / `Makefile` is out of scope for this spec (Req I-6.2).

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1", "2"] },
    { "id": 1, "tasks": ["3", "5", "6"] },
    { "id": 2, "tasks": ["4"] },
    { "id": 3, "tasks": ["7", "25"] },
    { "id": 4, "tasks": ["8", "12", "13", "14"] },
    { "id": 5, "tasks": ["9", "15", "16", "17"] },
    { "id": 6, "tasks": ["10", "18"] },
    { "id": 7, "tasks": ["11", "19", "20", "26"] },
    { "id": 8, "tasks": ["21"] },
    { "id": 9, "tasks": ["22"] },
    { "id": 10, "tasks": ["23"] },
    { "id": 11, "tasks": ["24"] },
    { "id": 12, "tasks": ["27", "28", "31"] },
    { "id": 13, "tasks": ["29", "30", "32", "33"] }
  ]
}
```

## Summary

33 coding tasks deliver a working `goboxd` Go binary across three architecture phases (1: HTTP skeleton; 2: `POST /run` end-to-end with `py3`; 3: full metrics + `cpp`) plus a final-acceptance group, with property-based, unit, and `//go:build integration` tests covering every architecture correctness property numbered 3 through 42 and zero modifications to `Dockerfile`, `docker-compose.yml`, or `Makefile`.
