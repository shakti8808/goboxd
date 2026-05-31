# Implementation Plan: GoboxD Architecture Document

## Overview

The deliverable for this feature is a single Markdown document at `docs/architecture.md`. Per Requirement 1.1 and Requirement 2.3, no `.go`, `.mod`, or `.sum` files are produced, and the infrastructure files `Dockerfile`, `docker-compose.yml`, and `Makefile` are not modified (Requirement 2.1).

The tasks below produce `docs/architecture.md` by mechanical expansion of the §1–§19 ordering map defined in `design.md` ("Document architecture (the deliverable)" table) and then validate the document against requirements 1–27 and Correctness Properties 1–42 from `design.md`.

Tasks are authoring/review tasks, not implementation tasks. No task in this plan writes Go source code.

## Tasks

### Scope and Non-Goals (§1, §2)

- [ ] 1. Author Scope and Non-Goals sections of `docs/architecture.md`
  - [ ] 1.1 Draft §1 "Scope" stating the design scope is limited to `cmd/goboxd`, `internal`, `docs`, and `tests` and that paths outside this list are out of scope
    - Section produced: §1 Scope
    - _Reqs: 1.7_
  - [ ] 1.2 Draft §2 "Non-Goals" listing `Dockerfile`, `docker-compose.yml`, and `Makefile` verbatim as files that SHALL NOT be created, modified, renamed, or deleted; enumerate the four pre-existing infrastructure prerequisites (container image build, container runtime, NsJail binary availability on the runtime path, network port exposure); state that no `.go`, `.mod`, or `.sum` file is created/modified/deleted and that the deliverable consists solely of `docs/architecture.md`
    - Section produced: §2 Non-Goals
    - _Reqs: 2.1, 2.2, 2.3, 2.5_
    - _Properties: 1_

### High-Level Overview (§3)

- [ ] 2. Author §3 High-Level Overview
  - [ ] 2.1 Reproduce the runtime architecture component diagram (Mermaid `flowchart` from design.md §"Runtime architecture") showing API_Server, Run_Handler, Health_Handler, Info_Handler, Security_Validator, Worker_Pool, Sandbox_Runner, Language_Registry, Metrics_Collector, Logger, Configuration, the nsjail subprocess, the Sandbox_Job_Directory, and the `/metrics` scrape, with solid request-driven arrows and dotted observer arrows
    - Section produced: §3 High-Level Overview
    - _Reqs: 1 (framing)_
    - _Properties: 25_

### Folder Structure (§4)

- [ ] 3. Author §4 Folder Structure
  - [ ] 3.1 Render the directory tree from design.md "Folder Structure (plan for `docs/architecture.md` §4)" verbatim, listing `cmd/goboxd/`, every immediate subdirectory of `internal/` (`api/`, `api/handlers/`, `config/`, `log/`, `metrics/`, `registry/`, `runner/`, `security/`, `version/`, `worker/`), the `docs/architecture.md` file, and `tests/integration/`
    - Section produced: §4 Folder Structure
    - _Reqs: 1.2, 15.1, 15.2, 15.3, 15.4_
  - [ ] 3.2 Provide a one-to-three sentence responsibility description for `cmd/goboxd/` and for every immediate subdirectory of `internal/`; provide a one-sentence purpose for `docs/architecture.md`
    - Section produced: §4 Folder Structure (annotations)
    - _Reqs: 15.2, 15.3, 15.4_
  - [ ] 3.3 Document the `tests/` test classification rule: unit tests live in `internal/<package>/*_test.go` with no build tag; integration tests live in `tests/integration/*_test.go` with `//go:build integration`; state that the build tag is what `make integration` selects
    - Section produced: §4 Folder Structure (test classification subsection)
    - _Reqs: 15.5, 20.1, 20.2, 20.3_
    - _Properties: 33_

### Package Design (§5)

- [ ] 4. Author §5 Package Design
  - [ ] 4.1 For every package under `internal/` (`version`, `log`, `config`, `metrics`, `registry`, `security`, `runner`, `worker`, `api/handlers`, `api`), write a 1–3 sentence responsibility statement naming inputs, outputs, and owned concern
    - Section produced: §5 Package Design (per-package responsibility)
    - _Reqs: 1.3, 16.1_
  - [ ] 4.2 For every package under `internal/`, list exported identifiers (types, interfaces, functions, methods, constants) with one-line descriptions; for any package with no exported surface, state explicitly that the package has no exported surface
    - Section produced: §5 Package Design (exported surface)
    - _Reqs: 16.2, 16.3_
  - [ ] 4.3 For every package under `internal/`, list the complete set of other `internal/` packages it imports, or state "no internal dependencies"
    - Section produced: §5 Package Design (import lists)
    - _Reqs: 16.4_
  - [ ] 4.4 Reproduce the package dependency graph (Mermaid diagram from design.md §"Dependency graph") and state that the graph contains zero cycles, supplying the topological order `version, config, metrics, log, registry, security, runner, worker, handlers, api`
    - Section produced: §5 Package Design (dependency graph)
    - _Reqs: 16.5, 16.6_
    - _Properties: 32_
  - [ ] 4.5 Classify every package under `internal/` as covered by unit tests, covered only by integration tests, or not covered, and name the test package or suite that provides coverage
    - Section produced: §5 Package Design (test coverage classification)
    - _Reqs: 16.7_

### Component Catalog (§6)

- [ ] 5. Author §6 Component Catalog
  - [ ] 5.1 Author Responsibilities / Interfaces / Interactions subsections for API_Server
    - Section produced: §6.1 API_Server
    - _Reqs: 2.4, 3.6, 3.7, 3.8, 3.9, 3.11, 5.8, 9.7, 9.8, 9.9, 12.6_
    - _Properties: 2, 3_
  - [ ] 5.2 Author Responsibilities / Interfaces / Interactions subsections for Run_Handler
    - Section produced: §6.2 Run_Handler
    - _Reqs: 2.4, 4, 18, 4.9, 4.10, 12.1, 12.2, 14.1, 14.7_
    - _Properties: 2, 22, 23, 26_
  - [ ] 5.3 Author Responsibilities / Interfaces / Interactions subsections for Health_Handler
    - Section produced: §6.3 Health_Handler
    - _Reqs: 2.4, 5.1, 5.2, 5.3, 5.4, 5.5, 5.6, 5.7, 5.8_
    - _Properties: 2, 4, 5_
  - [ ] 5.4 Author Responsibilities / Interfaces / Interactions subsections for Info_Handler
    - Section produced: §6.4 Info_Handler
    - _Reqs: 2.4, 6.1, 6.2, 6.3, 6.4_
    - _Properties: 2, 4, 6_
  - [ ] 5.5 Author Responsibilities / Interfaces / Interactions subsections for Language_Registry
    - Section produced: §6.5 Language_Registry
    - _Reqs: 2.4, 7.1, 7.3, 7.4, 7.5, 7.7, 8.3, 13.7_
    - _Properties: 2, 7, 8, 9_
  - [ ] 5.6 Author Responsibilities / Interfaces / Interactions subsections for Worker_Pool
    - Section produced: §6.6 Worker_Pool
    - _Reqs: 2.4, 9.1, 9.2, 9.3, 9.4, 9.5, 9.6, 9.7, 9.8, 9.9, 12.3, 13.6_
    - _Properties: 2, 12, 13, 14, 15, 30_
  - [ ] 5.7 Author Responsibilities / Interfaces / Interactions subsections for Sandbox_Runner, including the Workspace Isolation, Cleanup paths, and Orphan reaper subsections from design.md
    - Section produced: §6.7 Sandbox_Runner
    - _Reqs: 2.4, 4.9, 4.10, 10.1, 10.2, 10.3, 10.4, 10.5, 10.6, 10.7, 14.1, 14.5, 24.1, 24.2, 24.3, 24.4, 24.5_
    - _Properties: 2, 16, 17, 18, 19, 20, 21, 22, 39, 40, 41_
  - [ ] 5.8 Author Responsibilities / Interfaces / Interactions subsections for Security_Validator
    - Section produced: §6.8 Security_Validator
    - _Reqs: 2.4, 11.1, 11.2, 11.3, 11.4, 11.5, 11.6, 11.7, 12.4, 14.7_
    - _Properties: 2, 24_
  - [ ] 5.9 Author Responsibilities / Interfaces / Interactions subsections for Metrics_Collector
    - Section produced: §6.9 Metrics_Collector
    - _Reqs: 2.4, 12.1, 12.2, 12.3, 12.4, 12.5, 12.6, 14.6_
    - _Properties: 2, 25, 28, 29, 30_
  - [ ] 5.10 Author Responsibilities / Interfaces / Interactions subsections for Configuration
    - Section produced: §6.10 Configuration
    - _Reqs: 2.4, 13.1, 13.2, 13.6_
    - _Properties: 2, 10, 11_
  - [ ] 5.11 Author Responsibilities / Interfaces / Interactions subsections for Logger
    - Section produced: §6.11 Logger
    - _Reqs: 2.4, 14.1, 14.2, 14.3, 14.4, 14.5, 14.6, 14.7_
    - _Properties: 2, 25, 26, 27_

### HTTP API Reference (§7)

- [ ] 6. Author §7 HTTP API Reference
  - [ ] 6.1 Reproduce the per-endpoint table for `/run`, `/healthz`, `/readyz`, `/info`, `/metrics` covering method, required headers, request body shape, response body shape, response Content-Type, and the documented status codes (200, 400, 404, 405, 429, 500, 503)
    - Section produced: §7 HTTP API Reference
    - _Reqs: 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8, 3.9, 3.10, 3.11, 5.1, 5.2, 5.7, 6.1, 6.4, 12.6_
    - _Properties: 3, 5, 6_
  - [ ] 6.2 Document `Code_Submission` request body field set with types, required/optional flags, and ranges; document `Execution_Result` response body field set with types and ranges
    - Section produced: §7 HTTP API Reference (Data Models subsection)
    - _Reqs: 3.5, 4.2, 11.1, 11.2, 11.4, 13.5_

### Data Flow (§8)

- [ ] 7. Author §8 Data Flow for `POST /run`
  - [ ] 7.1 Reproduce the sequence diagram (Mermaid `sequenceDiagram` from design.md) showing Client, API_Server, Run_Handler, Security_Validator, Worker_Pool, Sandbox_Runner, nsjail, Sandbox_Job_Directory, Metrics_Collector, Logger
    - Section produced: §8 Data Flow (sequence diagram)
    - _Reqs: 1.4, 17.1, 17.2_
  - [ ] 7.2 Reproduce the numbered narrative table (steps 1–9) listing per-step component, input, output, state, and failure-mode-to-status mapping
    - Section produced: §8 Data Flow (numbered narrative)
    - _Reqs: 17.1, 17.2, 17.3, 17.4, 17.6_
    - _Properties: 25_
  - [ ] 7.3 Document the happy path and at least one error path (e.g. `language_not_registered`) using the same step structure
    - Section produced: §8 Data Flow (error path example)
    - _Reqs: 17.5_

### Request Lifecycle (§9)

- [ ] 8. Author §9 Request Lifecycle
  - [ ] 8.1 Reproduce the numbered lifecycle table for `POST /run` (steps 1–10) with M/C flag, responsible component, inputs, outputs, predecessor and successor steps; mark step 6 (compile) as the only conditional step with its execute/skip condition
    - Section produced: §9 Request Lifecycle (`POST /run`)
    - _Reqs: 1.5, 18.1, 18.2, 18.3, 18.4_
  - [ ] 8.2 Document cleanup guarantees for the three failure modes (execution timeout, process panic, shutdown signal): which steps execute, in what order, and the post-conditions on Sandbox_Job_Directory and spawned processes
    - Section produced: §9 Request Lifecycle (Cleanup guarantees)
    - _Reqs: 18.5_
    - _Properties: 22, 40_
  - [ ] 8.3 Document cleanup-failure recovery (cleanup_failures_total counter, ERROR log, orphan reaper using `SANDBOX_ORPHAN_TTL_S`)
    - Section produced: §9 Request Lifecycle (Cleanup failure recovery)
    - _Reqs: 4.10, 18.6_
    - _Properties: 23_
  - [ ] 8.4 Document the lifecycles for `GET /healthz`, `GET /readyz`, `GET /info` separately, stating that none create a Sandbox_Job_Directory, enqueue work, or run a compile/run step
    - Section produced: §9 Request Lifecycle (Non-`/run` lifecycles)
    - _Reqs: 18.7_
    - _Properties: 4_

### Worker Pool Design (§10)

- [ ] 9. Author §10 Worker Pool Design
  - [ ] 9.1 Reproduce the Worker_Pool aspect/specification table (max concurrent jobs, queue length, FIFO ordering, capacity rejection, drain timeout >0 and =0 semantics, configuration validation, signals)
    - Section produced: §10 Worker Pool Design
    - _Reqs: 9.1, 9.2, 9.3, 9.4, 9.5, 9.6, 9.7, 9.8, 9.9, 13.6_
    - _Properties: 12, 13, 14, 15_
  - [ ] 9.2 Document the HTTP 429 response body shape `{error: "capacity_exhausted", queue_max: N, workers_max: M}` for capacity-rejected submissions
    - Section produced: §10 Worker Pool Design (response shape)
    - _Reqs: 4.5_
    - _Properties: 24_

### NsJail Invocation Design (§11)

- [ ] 10. Author §11 NsJail Invocation Design
  - [ ] 10.1 Reproduce the complete NsJail argument template from design.md (with `{{ NSJAIL_PATH }}`, namespace flags, rlimit flags, bind-mount syntax, seccomp string, child argv following `--`)
    - Section produced: §11 NsJail Invocation Design (Argument template)
    - _Reqs: 10.2, 10.3, 10.4, 10.8_
    - _Properties: 16, 17_
  - [ ] 10.2 Reproduce the Placeholders table listing every per-request placeholder, its source field, and its validation rule
    - Section produced: §11 NsJail Invocation Design (Placeholders)
    - _Reqs: 10.8_
    - _Properties: 17, 37_
  - [ ] 10.3 Document namespace flags (user/PID/mount/network/IPC/UTS isolation; network disabled), resource-limit flags (`--time_limit`, `--rlimit_cpu`, `--rlimit_as`, `--rlimit_nproc`, `--rlimit_fsize`), bind-mount table (rw job dir, ro toolchain mounts, tmpfs `/tmp`), and I/O capture limits (1 MiB stdin/stdout/stderr with truncation flags)
    - Section produced: §11 NsJail Invocation Design (subsections)
    - _Reqs: 10.2, 10.3, 10.4, 10.5_
    - _Properties: 17, 18_
  - [ ] 10.4 Reproduce the deterministic status-classification table (compile/run × exit code/signal/nsjail-log indicator → status) covering OK, COMPILATION_ERROR, RUNTIME_ERROR, TIME_LIMIT_EXCEEDED, MEMORY_LIMIT_EXCEEDED, INTERNAL_ERROR
    - Section produced: §11 NsJail Invocation Design (Deterministic status classification)
    - _Reqs: 4.6, 4.7, 4.8, 10.6, 10.7_
    - _Properties: 19, 20, 21_
  - [ ] 10.5 Author the "Filename and Path Safety" subsection enumerating every basename validation rule (path separators, `..`, absolute prefix, leading `.`, codepoints below 0x20, NUL byte, empty string, POSIX scope) and naming the single canonical validator location `internal/security/filename.go::ValidateBasename`
    - Section produced: §11 NsJail Invocation Design (Filename and Path Safety)
    - _Reqs: 21.1, 21.2, 21.3, 21.4, 21.5_
    - _Properties: 34, 35_
  - [ ] 10.6 Author the "Command Template and Argument Safety" subsection: closed placeholder allowlist (`{{SOURCE_FILENAME}}`, `{{BINARY_FILENAME}}`, `{{STDIN_FILE}}`); deterministic single-pass non-recursive substitution algorithm; prohibition of shell-string concatenation; fixed environment allowlist (`PATH`, `LANG`, per-language env block from registry only)
    - Section produced: §11 NsJail Invocation Design (Command Template and Argument Safety)
    - _Reqs: 22.1, 22.2, 22.3, 22.4, 22.5, 22.6, 22.7_
    - _Properties: 36, 37_
  - [ ] 10.7 Author the "Streaming Output Protection" subsection: cumulative byte counter, retained-bytes buffer, fixed-size chunk reads (≤16 KiB), discard-after-limit semantics, `STDOUT_CAPTURE_LIMIT_BYTES + STDERR_CAPTURE_LIMIT_BYTES + O(1)` memory bound, truncation-flag relationship
    - Section produced: §11 NsJail Invocation Design (Streaming Output Protection)
    - _Reqs: 23.1, 23.2, 23.3, 23.4, 23.5, 23.6, 23.7_
    - _Properties: 38_

### Language Registry & YAML Schema (§12)

- [ ] 11. Author §12 Language Registry & YAML Schema
  - [ ] 11.1 Reproduce the YAML schema block listing every supported field of a Language_Definition (`id`, `source_filename`, `binary_filename`, `compile.command`, `compile.args`, `compile.limits.*`, `run.command`, `run.args`, `run.limits.*`) with required/optional flags and ranges
    - Section produced: §12 Language Registry & YAML Schema (Schema)
    - _Reqs: 7.2, 7.6_
    - _Properties: 8_
  - [ ] 11.2 Reproduce the worked `py3` YAML example (no compile block; run command `/usr/bin/python3` with `main.py`)
    - Section produced: §12 Language Registry & YAML Schema (`py3` example)
    - _Reqs: 8.1, 8.3_
  - [ ] 11.3 Reproduce the worked `cpp` YAML example (compile via `g++ -O2 -std=c++17 -o main main.cpp`; run `./main`)
    - Section produced: §12 Language Registry & YAML Schema (`cpp` example)
    - _Reqs: 8.2, 8.4_
  - [ ] 11.4 Reproduce the worked third-language example `node` and explicitly state that adding a language requires only a new YAML entry with no `internal/` Go file changes
    - Section produced: §12 Language Registry & YAML Schema (Add a third language)
    - _Reqs: 8.5_

### Security Validator Rule Catalog (§13)

- [ ] 12. Author §13 Security_Validator Rule Catalog
  - [ ] 12.1 Reproduce the rule catalog table for the five caller-facing rules (`malformed_submission`, `language_not_registered`, `source_size_exceeded`, `stdin_size_exceeded`, `resource_limit_exceeded`) with config key, default, range, trigger, and HTTP 400 error response shape
    - Section produced: §13 Security_Validator Rule Catalog (caller-facing rules)
    - _Reqs: 4.3, 4.4, 4.11, 11.1, 11.2, 11.3, 11.4, 11.5, 11.6, 11.7, 11.8, 13.5_
    - _Properties: 24_
  - [ ] 12.2 Reproduce the `unsafe_filename` rule row: registry-load fail-fast vs request-time INTERNAL_ERROR distinction; metric `goboxd_unsafe_filename_total{source}` with both `registry_load` and `runtime_validation` label values; ERROR log shape with `event`, `field`, `language_id`, `value`/`length`, `reason`, `request_id`
    - Section produced: §13 Security_Validator Rule Catalog (`unsafe_filename` row)
    - _Reqs: 21.2, 21.4, 27.1, 27.2, 27.3, 27.4, 27.5, 27.6_
    - _Properties: 34, 35_
  - [ ] 12.3 Reproduce the `unknown_placeholder` rule row: registry-load fail-fast vs request-time INTERNAL_ERROR distinction; metric `goboxd_unknown_placeholder_total{source}` with both label values; ERROR log shape with `event`, `language_id`, `args_entry`, `placeholder`, `reason`, `request_id`
    - Section produced: §13 Security_Validator Rule Catalog (`unknown_placeholder` row)
    - _Reqs: 22.4, 22.5, 27.1, 27.2, 27.3, 27.4, 27.5, 27.6_
    - _Properties: 36, 37_
  - [ ] 12.4 State that the 1 MiB body cap (Req 3.11) is enforced by API_Server upstream of the validator, so oversize bodies never reach Security_Validator
    - Section produced: §13 Security_Validator Rule Catalog (closing note)
    - _Reqs: 3.11_

### Metrics Catalog (§14)

- [ ] 13. Author §14 Metrics Catalog
  - [ ] 13.1 State exposition format (Prometheus text format with `Content-Type: text/plain; version=0.0.4`), endpoint (`GET /metrics`), and ≤1 s staleness bound
    - Section produced: §14 Metrics Catalog (header)
    - _Reqs: 12.6_
  - [ ] 13.2 Reproduce the metrics table covering every metric from design.md: `goboxd_run_requests_total`, `goboxd_run_duration_seconds`, `goboxd_worker_pool_queue_depth`, `goboxd_security_rejections_total`, `goboxd_sandbox_cleanup_failures_total`, `goboxd_dropped_logs_total`, `goboxd_dropped_metrics_total`, `goboxd_build_info`, `goboxd_unsafe_filename_total`, `goboxd_unknown_placeholder_total`, `goboxd_workspace_isolation_violation_total`, `goboxd_orphan_workspace_reaped_total`, `goboxd_startup_prereq_failures_total` — with type, unit, labels, and complete enumerated allowed label values
    - Section produced: §14 Metrics Catalog (table)
    - _Reqs: 12.1, 12.2, 12.3, 12.4, 12.7, 21.2, 21.4, 22.4, 22.5, 24.5, 25.4, 26.8_
    - _Properties: 28, 29, 30, 41_
  - [ ] 13.3 Document the histogram bucket boundaries `0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60` for `goboxd_run_duration_seconds`
    - Section produced: §14 Metrics Catalog (bucket subsection)
    - _Reqs: 12.2_
    - _Properties: 29_
  - [ ] 13.4 Document the unavailable-registry behaviour: recording calls are no-ops, `goboxd_dropped_metrics_total` increments, request continues, WARN log entry emitted
    - Section produced: §14 Metrics Catalog (failure subsection)
    - _Reqs: 12.5_
    - _Properties: 25_

### Configuration Reference (§15)

- [ ] 14. Author §15 Configuration Reference
  - [ ] 14.1 State the precedence rule (env > YAML > default) and that two operators reading the table will resolve identical effective values for any (key, source) combination
    - Section produced: §15 Configuration Reference (precedence)
    - _Reqs: 13.2_
    - _Properties: 11_
  - [ ] 14.2 Reproduce the configuration key table from design.md covering every key (API bind, max body, max source size, max stdin size, stdout/stderr capture limits, language registry path, worker pool size/queue/drain timeout, NsJail path/launch timeout, sandbox root, RO mounts, seccomp policy, orphan TTL, per-language ceilings, log level, service name, build version, `SANDBOX_ROOT_DIR_OWNER_UID`, `SANDBOX_ROOT_DIR_PERMS_MAX`) — with env var, YAML path, type, range, unit, and default
    - Section produced: §15 Configuration Reference (table)
    - _Reqs: 13.1, 13.3, 13.4, 13.5_
    - _Properties: 10, 11_
  - [ ] 14.3 Document the missing/out-of-range/unparseable startup-failure behaviour with non-zero exit and offending-key log
    - Section produced: §15 Configuration Reference (validation closing note)
    - _Reqs: 13.6, 13.7_
    - _Properties: 9, 10_

### Observability and Logging (§16)

- [ ] 15. Author §16 Observability and Logging
  - [ ] 15.1 Reproduce the lifecycle event/log-level/required-fields table for `POST /run` completion, pre-enqueue rejection, startup completion, shutdown begin, NsJail invocation failure, and sandbox cleanup failure
    - Section produced: §16 Observability and Logging (lifecycle table)
    - _Reqs: 14.1, 14.2, 14.3, 14.7_
    - _Properties: 26_
  - [ ] 15.2 Document redaction: at INFO and above, `Code_Submission.source`, `Code_Submission.stdin`, captured stdout, captured stderr are omitted; at DEBUG, sandbox argv MAY be logged but user payload remains redacted
    - Section produced: §16 Observability and Logging (Redaction)
    - _Reqs: 14.4, 14.5_
    - _Properties: 27_
  - [ ] 15.3 Document log-emission failure mode: originating request unaffected, `goboxd_dropped_logs_total{reason}` increments, processing continues
    - Section produced: §16 Observability and Logging (failure subsection)
    - _Reqs: 14.6_
    - _Properties: 25_

### Correctness Properties (§17 within Testing Strategy)

- [ ] 16. Author the Correctness Properties enumeration carried into the testing strategy
  - [ ] 16.1 Enumerate Properties 1–10 verbatim with their `Validates: Requirements` annotations (Property 1 → Reqs 2.3, 2.4; Property 2 → Req 2.4; Property 3 → Reqs 3.7, 3.8, 3.9, 3.10, 3.11, 4.11; Property 4 → Reqs 5.6, 6.3, 6.4, 18.7; Property 5 → Reqs 5.2, 5.3, 5.4, 5.5, 5.8; Property 6 → Reqs 6.1, 6.2; Property 7 → Req 7.5; Property 8 → Reqs 7.2, 8.1, 8.2; Property 9 → Reqs 7.3, 7.4, 7.7, 13.7; Property 10 → Reqs 9.6, 13.6)
    - Section produced: §17 Testing Strategy (Properties 1–10)
    - _Reqs: 2.3, 2.4, 3.7, 3.8, 3.9, 3.10, 3.11, 4.11, 5.2, 5.3, 5.4, 5.5, 5.6, 5.8, 6.1, 6.2, 6.3, 6.4, 7.2, 7.3, 7.4, 7.5, 7.7, 8.1, 8.2, 9.6, 13.6, 13.7, 18.7_
    - _Properties: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10_
  - [ ] 16.2 Enumerate Properties 11–20 verbatim with their `Validates: Requirements` annotations (Property 11 → Req 13.2; Property 12 → Reqs 9.1, 9.2; Property 13 → Reqs 4.5, 9.3; Property 14 → Req 9.4; Property 15 → Reqs 9.7, 9.8, 9.9; Property 16 → Req 10.1; Property 17 → Reqs 10.2, 10.3, 10.4; Property 18 → Req 10.5; Property 19 → Reqs 4.6, 4.7, 4.8, 10.6; Property 20 → Req 10.7)
    - Section produced: §17 Testing Strategy (Properties 11–20)
    - _Reqs: 4.5, 4.6, 4.7, 4.8, 9.1, 9.2, 9.3, 9.4, 9.7, 9.8, 9.9, 10.1, 10.2, 10.3, 10.4, 10.5, 10.6, 10.7, 13.2_
    - _Properties: 11, 12, 13, 14, 15, 16, 17, 18, 19, 20_
  - [ ] 16.3 Enumerate Properties 21–30 verbatim with their `Validates: Requirements` annotations (Property 21 → Req 4.8; Property 22 → Reqs 4.9, 18.5; Property 23 → Reqs 4.10, 18.6; Property 24 → Reqs 4.3, 4.4, 4.5, 11.1, 11.2, 11.3, 11.4, 11.5, 11.6; Property 25 → Reqs 12.5, 14.6, 17.6; Property 26 → Reqs 14.1, 14.7; Property 27 → Reqs 14.4, 14.5; Property 28 → Reqs 12.1, 12.4; Property 29 → Req 12.2; Property 30 → Req 12.3)
    - Section produced: §17 Testing Strategy (Properties 21–30)
    - _Reqs: 4.3, 4.4, 4.5, 4.8, 4.9, 4.10, 11.1, 11.2, 11.3, 11.4, 11.5, 11.6, 12.1, 12.2, 12.3, 12.4, 12.5, 14.1, 14.4, 14.5, 14.6, 14.7, 17.6, 18.5, 18.6_
    - _Properties: 21, 22, 23, 24, 25, 26, 27, 28, 29, 30_
  - [ ] 16.4 Enumerate Properties 31–42 verbatim with their `Validates: Requirements` annotations (Property 31 → Reqs 15.6, 15.7; Property 32 → Reqs 16.4, 16.5, 16.6; Property 33 → Reqs 15.5, 20.1, 20.2; Property 34 → Reqs 21.1, 21.2, 21.5; Property 35 → Reqs 21.3, 21.4; Property 36 → Reqs 22.1, 22.2, 22.6; Property 37 → Reqs 22.3, 22.4, 22.5, 22.7; Property 38 → Reqs 23.1, 23.2, 23.3, 23.4, 23.5, 23.6, 23.7; Property 39 → Reqs 24.1, 24.2, 24.3; Property 40 → Req 24.4; Property 41 → Req 24.5; Property 42 → Reqs 25.1, 25.2, 25.3, 25.4, 25.5, 25.6)
    - Section produced: §17 Testing Strategy (Properties 31–42)
    - _Reqs: 15.5, 15.6, 15.7, 16.4, 16.5, 16.6, 20.1, 20.2, 21.1, 21.2, 21.3, 21.4, 21.5, 22.1, 22.2, 22.3, 22.4, 22.5, 22.6, 22.7, 23.1, 23.2, 23.3, 23.4, 23.5, 23.6, 23.7, 24.1, 24.2, 24.3, 24.4, 24.5, 25.1, 25.2, 25.3, 25.4, 25.5, 25.6_
    - _Properties: 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42_

### Error Handling (§17 Error Handling subsection)

- [ ] 17. Author the Error Handling section
  - [ ] 17.1 Reproduce the five-layer error-handling table (Transport, Routing, Validation, Capacity, Execution) with triggers, handler, status code, and surfacing
    - Section produced: §17 Error Handling (layers table)
    - _Reqs: 3.7, 3.8, 3.9, 3.10, 3.11, 4.3, 4.4, 4.5, 4.6, 4.7, 4.8, 4.10, 4.11, 9.3, 10.6, 10.7, 11.1, 11.2, 11.3, 11.4, 11.5_
    - _Properties: 3, 19, 20, 21, 22, 23, 24_
  - [ ] 17.2 Document the two cross-cutting rules: observer-failure isolation and unconditional cleanup
    - Section produced: §17 Error Handling (cross-cutting rules)
    - _Reqs: 4.9, 4.10, 12.5, 14.6, 17.6, 18.5_
    - _Properties: 22, 23, 25_
  - [ ] 17.3 Author the Startup Prerequisite Validation subsection enumerating the five ordered checks (configuration load, Language_Registry load with filename + placeholder validation, sandbox root permissions/ownership, RO bind-mount existence/readability, NsJail binary executable check) with the structured ERROR log shape (`event="startup_prereq_failed"`, `prereq`, `path`, `reason`) and the `goboxd_startup_prereq_failures_total{prereq}` counter
    - Section produced: §17 Error Handling (Startup Prerequisite Validation)
    - _Reqs: 25.1, 25.2, 25.3, 25.4, 25.5, 25.6_
    - _Properties: 42_

### Testing Strategy (§17)

- [ ] 18. Author §17 Testing Strategy
  - [ ] 18.1 Reproduce the test taxonomy table (Unit / Property / Integration with location, selector, purpose) and state that the build tag is the single mechanism distinguishing integration tests
    - Section produced: §17 Testing Strategy (taxonomy)
    - _Reqs: 20.1, 20.2, 20.3_
    - _Properties: 33_
  - [ ] 18.2 Reproduce the per-package unit/property test focus table covering `internal/config`, `internal/registry`, `internal/security`, `internal/runner`, `internal/worker`, `internal/api/handlers`, `internal/api`, `internal/log`, `internal/metrics`
    - Section produced: §17 Testing Strategy (per-package focus)
    - _Reqs: 16.7, 20.1_
  - [ ] 18.3 Reproduce the per-language integration scenarios (`py3-hello`, `cpp-hello`) with trigger and assertion
    - Section produced: §17 Testing Strategy (per-language integration)
    - _Reqs: 20.4_
  - [ ] 18.4 Reproduce the per-failure-mode integration scenarios (`unknown-language`, `oversize-source`, `queue-full`, `time-limit`, `compile-error`) with trigger and observable indication
    - Section produced: §17 Testing Strategy (per-failure-mode integration)
    - _Reqs: 20.5_
  - [ ] 18.5 Author the Security scenarios sub-table covering `path-traversal-source-filename`, `path-traversal-binary-filename`, `invalid-filename-control-bytes`, `command-injection-via-source`, `shell-metachar-stdin`, `excessive-stdout`, `excessive-stderr`, `workspace-isolation-concurrent`, `orphan-workspace-reaped` with input condition, observable success criterion, expected metric increments, and expected log entries
    - Section produced: §17 Testing Strategy (Security scenarios)
    - _Reqs: 26.1, 26.2, 26.3, 26.4, 26.5, 26.6, 26.7, 26.8, 26.9_
    - _Properties: 34, 35, 36, 37, 38, 39, 40, 41_
  - [ ] 18.6 Document the NsJail-availability precondition: each scenario requiring nsjail declares the precondition and `t.Skip()`s when not met
    - Section produced: §17 Testing Strategy (precondition)
    - _Reqs: 20.6_

### Implementation Phases (§18)

- [ ] 19. Author §18 Implementation Phases
  - [ ] 19.1 Author Phase 1 (HTTP skeleton): scope (cmd/goboxd, config, log, version, api, api/handlers stubs, registry, minimal metrics); security scope bullets (filename validation, startup prerequisite checks, placeholder allowlist at registry load); demonstrable outcomes (build/run/healthz/readyz/info/metrics within 1 second); completion gate (≥70% statement coverage, named test suites pass under `make test`)
    - Section produced: §18 Implementation Phases (Phase 1)
    - _Reqs: 1.6, 19.1, 19.2, 19.3, 21.1, 22.1, 25.1, 25.2, 25.3_
    - _Properties: 42_
  - [ ] 19.2 Author Phase 2 (`POST /run` with NsJail and `py3`): scope (security, worker, runner, full Run_Handler); security scope bullets (closed placeholder allowlist enforced, runtime filename re-validation, streaming output discarders, workspace UUIDv4 + ownership invariant); demonstrable outcomes; completion gate (≥80% coverage, named integration scenarios pass); explicit dependency on Phase 1
    - Section produced: §18 Implementation Phases (Phase 2)
    - _Reqs: 19.1, 19.2, 19.4, 19.6, 22.2, 22.3, 23.1, 24.1, 24.3_
    - _Properties: 36, 37, 38, 39, 41_
  - [ ] 19.3 Author Phase 3 (Metrics_Collector and `cpp`): scope (full metrics, second language with compile step, binary_filename plumbing); demonstrable outcomes (cpp success and COMPILATION_ERROR; full metrics stream); completion gate (≥80% coverage, cpp-hello + compile-error pass, ≤1 s staleness); explicit dependency on Phase 2
    - Section produced: §18 Implementation Phases (Phase 3)
    - _Reqs: 19.1, 19.2, 19.5, 19.6_
    - _Properties: 28, 29, 30_
  - [ ] 19.4 Reproduce the phase-ordering dependency table (Phase 1 → none; Phase 2 → Phase 1 deliverables; Phase 3 → Phase 2 deliverables) and state that each phase's out-of-scope list excludes the next phase's scope
    - Section produced: §18 Implementation Phases (ordering)
    - _Reqs: 19.6_

### Component → Folder Traceability (§19)

- [ ] 20. Author §19 Component → Folder Traceability
  - [ ] 20.1 Reproduce the traceability table mapping every component named in §6 (API_Server, Run_Handler, Health_Handler, Info_Handler, Language_Registry, Worker_Pool, Sandbox_Runner, Security_Validator, Metrics_Collector, Configuration, Logger, build version constants, binary entry point) to exactly one folder path from §4
    - Section produced: §19 Component → Folder Traceability
    - _Reqs: 15.6, 15.7_
    - _Properties: 31_
  - [ ] 20.2 Note that Run_Handler, Health_Handler, and Info_Handler share `internal/api/handlers/` — one folder hosting multiple components — without violating the single-folder-per-component rule
    - Section produced: §19 Component → Folder Traceability (note)
    - _Reqs: 15.6_
    - _Properties: 31_

### Final Document Authoring

- [ ] 21. Create `docs/architecture.md`
  - [ ] 21.1 Create `docs/architecture.md` by mechanical expansion of design.md sections following the §1–§19 ordering map
    - Combine the outputs of tasks 1–20 into a single Markdown file at `docs/architecture.md` in the repository
    - Preserve the §1–§19 ordering exactly as defined in the "Document architecture (the deliverable)" table of design.md
    - File produced: `docs/architecture.md`
    - _Reqs: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8, 2.4_

### Document QA & Lint

- [ ] 22. Verify document QA and lint constraints
  - [ ] 22.1 Verify no `.go`, `.mod`, or `.sum` file is added, modified, or deleted by this feature; confirm the only file produced is `docs/architecture.md`
    - _Reqs: 1.1, 2.3_
    - _Properties: 1_
  - [ ] 22.2 Verify `Dockerfile`, `docker-compose.yml`, and `Makefile` are unchanged from their state before this feature began (byte-for-byte equality with their pre-feature content)
    - _Reqs: 2.1_
  - [ ] 22.3 Verify each component named in §6 (Component Catalog) maps to exactly one folder path in §19 (Component → Folder Traceability), and that every folder path in §4 has at least one component mapped to it (or is documented as a non-component folder such as `docs/` or `tests/`)
    - _Reqs: 15.6, 15.7_
    - _Properties: 31_
  - [ ] 22.4 Verify the package dependency graph in §5 is acyclic by checking that the topological order `version, config, metrics, log, registry, security, runner, worker, handlers, api` is a valid linear extension (every documented edge points strictly forward in the order)
    - _Reqs: 16.5, 16.6_
    - _Properties: 32_
  - [ ] 22.5 Run a structural lint over `docs/architecture.md` confirming the document contains exactly the 19 sections in the order defined by the design.md "Document architecture (the deliverable)" table (§1 Scope, §2 Non-Goals, §3 High-Level Overview, §4 Folder Structure, §5 Package Design, §6 Component Catalog, §7 HTTP API Reference, §8 Data Flow, §9 Request Lifecycle, §10 Worker Pool Design, §11 NsJail Invocation Design, §12 Language Registry & YAML Schema, §13 Security_Validator Rule Catalog, §14 Metrics Catalog, §15 Configuration Reference, §16 Observability and Logging, §17 Testing Strategy, §18 Implementation Phases, §19 Component → Folder Traceability)
    - _Reqs: 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8_
  - [ ] 22.6 Verify every Requirement 1–27 (including every numbered acceptance criterion such as 1.1, 1.2, …, 27.6) is referenced at least once in `docs/architecture.md`
    - _Reqs: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27_
  - [ ] 22.7 Verify every Correctness Property 1–42 is referenced at least once in `docs/architecture.md` (in §17 Testing Strategy)
    - _Reqs: 1.6, 20.1_
    - _Properties: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42_
  - [ ] 22.8 Verify every metric named in design.md body (e.g. `goboxd_run_requests_total`, `goboxd_run_duration_seconds`, `goboxd_worker_pool_queue_depth`, `goboxd_security_rejections_total`, `goboxd_sandbox_cleanup_failures_total`, `goboxd_dropped_logs_total`, `goboxd_dropped_metrics_total`, `goboxd_build_info`, `goboxd_unsafe_filename_total`, `goboxd_unknown_placeholder_total`, `goboxd_workspace_isolation_violation_total`, `goboxd_orphan_workspace_reaped_total`, `goboxd_startup_prereq_failures_total`) is present in §14 Metrics Catalog
    - _Reqs: 12.7_
  - [ ] 22.9 Verify every configuration key named in design.md body (every row of the Configuration Reference table including `SANDBOX_ROOT_DIR_OWNER_UID` and `SANDBOX_ROOT_DIR_PERMS_MAX`) is present in §15 Configuration Reference
    - _Reqs: 13.1, 13.3, 13.4, 13.5_
  - [ ] 22.10 Verify every Security_Validator rule referenced in the design body (`malformed_submission`, `language_not_registered`, `source_size_exceeded`, `stdin_size_exceeded`, `resource_limit_exceeded`, `unsafe_filename`, `unknown_placeholder`) is present in §13 Security_Validator Rule Catalog
    - _Reqs: 11.8, 27.1, 27.2_
  - [ ] 22.11 Run `getDiagnostics` on `docs/architecture.md` and confirm zero diagnostics; additionally grep the file content to confirm no Go fenced code blocks (no ` ```go ` opening fence) appear anywhere in the document
    - _Reqs: 2.4, 2.5_
    - _Properties: 1, 2_

## Notes

- This plan produces only documentation. No task creates, modifies, or deletes Go source code.
- Every task lists the specific section(s) of `docs/architecture.md` it produces or modifies, the requirement IDs it satisfies, and (where applicable) the Correctness Property numbers it traces back to.
- The single document-write task (Task 21) is kept atomic and explicit and is the only task that creates `docs/architecture.md`; preceding tasks (1–20) author the section content; following tasks (22.x) validate the document.
- The QA tasks (22.1 through 22.11) treat the document as the unit under test.

## Task Dependency Graph

```json
{
  "waves": [
    { "id": 0, "tasks": ["1.1", "1.2", "3.1", "6.2", "13.1", "14.1"] },
    { "id": 1, "tasks": ["2.1", "3.2", "3.3", "4.1", "4.2", "4.3", "6.1", "9.1", "9.2", "11.1", "11.2", "11.3", "11.4", "12.1", "12.2", "12.3", "12.4", "13.2", "13.3", "13.4", "14.2", "14.3", "15.1", "15.2", "15.3"] },
    { "id": 2, "tasks": ["4.4", "4.5", "5.1", "5.2", "5.3", "5.4", "5.5", "5.6", "5.7", "5.8", "5.9", "5.10", "5.11", "7.1", "7.2", "7.3", "8.1", "8.2", "8.3", "8.4", "10.1", "10.2", "10.3", "10.4", "10.5", "10.6", "10.7", "16.1", "16.2", "16.3", "16.4", "17.1", "17.2", "17.3", "18.1", "18.2", "18.3", "18.4", "18.5", "18.6", "19.1", "19.2", "19.3", "19.4"] },
    { "id": 3, "tasks": ["20.1", "20.2"] },
    { "id": 4, "tasks": ["21.1"] },
    { "id": 5, "tasks": ["22.1", "22.2", "22.3", "22.4", "22.5", "22.6", "22.7", "22.8", "22.9", "22.10", "22.11"] }
  ]
}
```

## Summary

Total tasks: 89 leaf sub-tasks across 22 top-level groups, organised under the architecture-section headings: Scope and Non-Goals; High-Level Overview; Folder Structure; Package Design; Component Catalog; HTTP API Reference; Data Flow; Request Lifecycle; Worker Pool Design; NsJail Invocation Design (with Filename Safety, Command Safety, and Streaming Output Protection sub-tasks); Language Registry & YAML Schema; Security Validator Rule Catalog; Metrics Catalog; Configuration Reference; Observability and Logging; Correctness Properties; Error Handling (with Startup Prerequisite Validation sub-task); Testing Strategy (with Security scenarios sub-task); Implementation Phases; Component → Folder Traceability — plus a Final Document Authoring group (Task 21, document-write) and a Document QA & Lint group (Task 22, validation).
