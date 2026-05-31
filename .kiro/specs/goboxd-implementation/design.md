# Design Document: goboxd-implementation

## Overview

This spec implements the Go service whose blueprint is fully specified in
`.kiro/specs/goboxd-architecture/design.md` (hereafter the **architecture
design**). The architecture design is a documentation-only deliverable; **this
implementation spec produces actual Go source code** under `cmd/goboxd/`,
`internal/`, and tests under `internal/<pkg>/` (unit + property) and
`tests/integration/` (integration, build-tag gated).

This document is intentionally short. It does not duplicate any architecture
content. It records only the decisions that are specific to *implementation* —
module path, third-party dependencies, build/test commands, the property
catalog split, and the package implementation order — and references the
architecture design by section number for everything else.

## Inheritance From the Architecture Spec

The following are **inherited verbatim** from the architecture design and MUST
NOT be re-specified here. The architecture design is the single source of
truth. Implementation tasks that contradict any of these sections are
incorrect by definition.

| Inherited concern | Source in `.kiro/specs/goboxd-architecture/design.md` |
| --- | --- |
| Folder structure (`cmd/`, `internal/`, `docs/`, `tests/`) | §4 Folder Structure |
| Per-package responsibility, exported surface, dependency graph | §5 Package Design |
| Component model (Responsibilities / Interfaces / Interactions per component) | §6 Components and Interfaces |
| HTTP API surface, request/response shapes, status-code partition | §7 HTTP API Reference |
| `POST /run` data flow and error paths | §8 Data Flow |
| `POST /run` lifecycle steps + cleanup guarantees | §9 Request Lifecycle |
| Worker_Pool concurrency model + drain semantics | §10 Worker Pool Design |
| NsJail invocation argument template + placeholders + status classifier | §11 NsJail Invocation Design |
| Filename and Path Safety (basename rule + call sites) | §11 sub-section |
| Command Template and Argument Safety (closed placeholder allowlist) | §11 sub-section |
| Streaming output protection (memory-bounded discarders) | §11 sub-section |
| Language registry YAML schema + `py3`/`cpp` worked examples | §12 Language Registry |
| Security_Validator rule catalog | §13 Security_Validator Rule Catalog |
| Metrics catalog (names, types, labels, units, buckets) | §14 Metrics Catalog |
| Configuration table (env > YAML > default precedence) | §15 Configuration Reference |
| Observability and logging (UUIDv4 ids, ISO 8601 UTC ms, redaction rules) | §16 Observability and Logging |
| Correctness Properties 1–42 | §"Correctness Properties" |
| Implementation Phases 1, 2, 3 | §"Implementation Phases" |
| Non-goals (Dockerfile, docker-compose.yml, Makefile must not change) | §"Non-Goals & Infrastructure Assumptions" |

If a constraint is not visible in the architecture design, it is not a
constraint on this implementation. If a constraint is visible there, it
applies here unchanged.

## Architecture

The runtime architecture diagram is in the architecture design, §"Architecture"
(Mermaid `flowchart LR`) and is NOT duplicated here. The dependency graph
across `internal/` packages — also mandatory for this implementation — is in
the architecture design, §5 ("Dependency graph"), with topological order
`version, config, metrics, log, registry, security, runner, worker, handlers,
api`. This implementation realises that architecture unchanged.

## Components and Interfaces

Each component named in the architecture design §6 (API_Server, Run_Handler,
Health_Handler, Info_Handler, Language_Registry, Worker_Pool, Sandbox_Runner,
Security_Validator, Metrics_Collector, Configuration, Logger) is implemented
in exactly the folder listed in architecture design §"Component → Folder
traceability". The Responsibilities / Interfaces / Interactions for each
component are inherited verbatim from §6 and not restated here. Concrete Go
type and function signatures are decided per-package during implementation
PRs and need not be enumerated in this design — the inherited interfaces are
behavioural contracts, not Go signatures.

## Data Models

Wire shapes (`Code_Submission`, `Execution_Result`), the `Language_Definition`
YAML shape, and the in-memory `Job` shape are inherited verbatim from
architecture design §"Data Models" and §12. The Go type representations
chosen during implementation are required to be JSON/YAML-isomorphic to
those tables — same field names (in `snake_case` over the wire,
`CamelCase` Go fields with explicit `json:"…"` / `yaml:"…"` tags), same
ranges, same enum values for `Execution_Result.status`. No additional
fields may be added to the public wire surface.

## Implementation-Specific Decisions

These are the decisions the architecture design deliberately did not make
because they are implementation choices. They apply to this spec only.

### 1. Go module path

The existing `go.mod` declares:

```
module github.com/thesouldev/goboxd
go 1.23
```

This module path and Go toolchain version are **fixed**. Implementation tasks
do not modify `go.mod`'s module line or change the Go version.

### 2. Third-party dependencies

The implementation introduces exactly the following Go module dependencies.
No others may be added without an amendment to this design.

| Purpose | Module | Version policy |
| --- | --- | --- |
| YAML parser (Language_Registry, optional config YAML) | `gopkg.in/yaml.v3` | latest minor at time of impl |
| UUIDv4 generation (request ids, sandbox dirs — Property 39) | `github.com/google/uuid` | latest minor at time of impl |
| Prometheus client + text exposition (Metrics_Collector) | `github.com/prometheus/client_golang` | latest minor at time of impl |
| Property-based testing (PBT) | `pgregory.net/rapid` | latest minor at time of impl |
| Structured logging (Logger) | `log/slog` (Go stdlib) | bundled with Go 1.23 |
| HTTP server, JSON, signal handling, context, exec | Go stdlib | bundled with Go 1.23 |

Pinned versions land in `go.sum` once `go mod tidy` runs. The list above is
exhaustive; transitive deps brought in by these modules are accepted as-is.

### 3. Build configuration

- **Build target:** `linux/amd64` only. NsJail relies on Linux user/PID/mount/
  network/IPC/UTS namespaces, cgroups, seccomp-bpf, and the `nsjail` binary
  built in the multi-stage `Dockerfile`. The binary is not expected to build
  or run on macOS or Windows.
- **CGO:** disabled (`CGO_ENABLED=0`), matching the existing `Dockerfile`
  build stage, which produces a static `goboxd` binary.
- **Linker flags:** `-trimpath -ldflags="-s -w"`, matching the existing
  `Dockerfile`.
- **Build version injection:** `internal/version` exposes a string constant
  whose default is `dev` and which can be overridden at link time (e.g. via
  `-ldflags="-X github.com/thesouldev/goboxd/internal/version.Version=..."`).
  The `BUILD_VERSION` env var falls back to this constant when absent
  (architecture design §15).
- **Infrastructure files MUST NOT be modified.** Per architecture design
  Req 2.1, the existing `Dockerfile`, `docker-compose.yml`, and `Makefile`
  are out of scope and untouched. The implementation is shaped to fit the
  existing build/test commands, not the other way round.

### 4. Test execution commands

Inherited from the existing `Makefile`:

| Command | What it runs | Selects |
| --- | --- | --- |
| `make test` | `go test ./...` inside the `tools` compose service | unit tests + property tests (no build tag) |
| `make integration` | `go test -tags=integration ./tests/...` inside the `tools` compose service | integration tests only |
| `make lint` | `golangci-lint run ./...` inside the `tools` compose service | static analysis |

`make integration` requires the `nsjail` binary to be available in the
container (it is, via the `nsjail-builder` stage of the existing `Dockerfile`).
Integration tests `t.Skip()` when `nsjail` is absent at the configured path,
per architecture design §"Testing Strategy" — they do not fail closed locally.

### 5. Package boundary constraints

Every `internal/<package>` listed in the architecture design §4 is
implemented in its own directory and does not import any package "above" it
in the dependency graph (architecture design §5). The graph is acyclic; a
test gate enforces this:

- `internal/version`, `internal/config`, `internal/metrics` import nothing
  from `internal/`.
- `internal/log` imports only `internal/metrics`.
- `internal/registry` imports `internal/config`, `internal/log`.
- `internal/security` imports `internal/config`, `internal/registry`,
  `internal/log`, `internal/metrics`.
- `internal/runner` imports `internal/config`, `internal/registry`,
  `internal/log`, `internal/metrics`.
- `internal/worker` imports `internal/config`, `internal/log`,
  `internal/metrics`, `internal/runner`.
- `internal/api/handlers` imports `internal/config`, `internal/log`,
  `internal/metrics`, `internal/registry`, `internal/security`,
  `internal/version`, `internal/worker`.
- `internal/api` imports `internal/api/handlers`, `internal/log`,
  `internal/metrics`.
- `cmd/goboxd` imports the leaves it needs to wire (`config`, `log`,
  `version`, `registry`, `metrics`, `worker`, `runner`, `api`,
  `api/handlers`).

A unit test in `internal/api` (or a small `tests/structure_test.go` with no
build tag) parses `go list -deps` output and fails if any forbidden edge
appears. This validates Property 32 (acyclic dependency graph) at CI time.

## Correctness Properties

The 42 universal correctness properties are inherited verbatim from the
architecture design §"Correctness Properties" — see the table in the
"Correctness Property Classification" section below for the test-mechanism
mapping (PBT vs Unit vs Integration vs Static).

### Property 1: Inherits all 42 architecture-spec correctness properties

*For all* properties P in the set {Property 1, Property 2, …, Property 42}
defined in `.kiro/specs/goboxd-architecture/design.md`
§"Correctness Properties", this implementation MUST satisfy P. The
architecture design is the canonical statement of each property; this
implementation spec adds only the test-mechanism classification (the table
in "Correctness Property Classification" below) and does not restate the
property text.

**Validates: Requirements 1.1, 2.1, 2.3** (architecture inheritance — the
architecture deliverable defines the requirement set this implementation
realises; see `.kiro/specs/goboxd-architecture/requirements.md` for the
full requirement catalog).

## Correctness Property Classification

Architecture design §"Correctness Properties" defines 42 properties. Each
property is realised by exactly one of three test mechanisms in this
implementation. The split below is mandatory; tasks must produce tests of
the indicated kind.

**Legend.** *PBT* = property-based test using `pgregory.net/rapid`, lives
under `internal/<pkg>/*_property_test.go` (no build tag). *Unit* = ordinary
table-driven Go test, lives under `internal/<pkg>/*_test.go` (no build tag).
*Integration* = end-to-end test under `tests/integration/*_test.go` with
`//go:build integration`. *Static* = enforced by file/path layout, build
tags, or CI tooling — no Go test code at all.

| # | Property (short) | Mechanism | Where |
| --- | --- | --- | --- |
| 1 | No Go source in deliverable artifacts | N/A here | applies to architecture spec only |
| 2 | Component model three-subsection contract | N/A here | applies to architecture spec only |
| 3 | 404 reserved for unrecognised paths | Unit | `internal/api` |
| 4 | Non-`/run` endpoints inert wrt execution state | Unit | `internal/api/handlers` |
| 5 | Readiness body enumerates every failed condition | Unit | `internal/api/handlers` |
| 6 | `/info` never leaks secrets/paths/submission-derived data | Unit | `internal/api/handlers` |
| 7 | `Language_Registry.Get` deterministic side-effect-free exact match | PBT | `internal/registry` |
| 8 | Loaded definitions expose all required fields | PBT | `internal/registry` |
| 9 | Invalid registry YAML aborts startup with structured log | Unit | `internal/registry` |
| 10 | Invalid configuration aborts startup naming offending key | Unit | `internal/config` |
| 11 | Effective config follows env > YAML > default precedence | PBT | `internal/config` |
| 12 | `Worker_Pool` honors capacity bounds | PBT | `internal/worker` |
| 13 | Capacity-exhausted submissions rejected immediately | Unit | `internal/worker` |
| 14 | `Worker_Pool` dequeue order is FIFO | PBT | `internal/worker` |
| 15 | Drain semantics (T>0 vs T=0) | Unit | `internal/worker` |
| 16 | Sandbox execution always goes through nsjail | Integration | `tests/integration` |
| 17 | NsJail argv enforces isolation, limits, mount policy | PBT | `internal/runner` (argv builder is a pure function) |
| 18 | I/O caps honored exactly | PBT | `internal/runner` |
| 19 | Sandbox status classifier deterministic total function | PBT | `internal/runner` |
| 20 | NsJail invocation failure produces `INTERNAL_ERROR` w/ no leaks | Integration | `tests/integration` |
| 21 | Compile-step failure halts lifecycle before run step | Integration | `tests/integration` |
| 22 | `Sandbox_Job_Directory` cleanup is total | Integration | `tests/integration` |
| 23 | Cleanup failure does not alter response | Unit | `internal/runner` |
| 24 | Rejected submissions are inert | Unit | `internal/api/handlers` |
| 25 | Observer failure never alters `Execution_Result` | Unit | `internal/api/handlers` |
| 26 | Per-request log entry shape (UUIDv4, ISO 8601, duration_ms) | PBT | `internal/log` |
| 27 | Logs never contain `Code_Submission` payload bytes | PBT | `internal/log` |
| 28 | Metrics counters reflect the request stream | Integration | `tests/integration` |
| 29 | Run-duration observation falls into a documented bucket | Unit | `internal/metrics` |
| 30 | Queue-depth gauge tracks pool queue size | Unit | `internal/metrics` + `internal/worker` |
| 31 | Component–folder mapping total and exclusive | Static | doc structure (architecture spec) |
| 32 | Internal-package dependency graph acyclic & matches per-package import lists | Static | `tests/structure_test.go` (no build tag) parsing `go list` |
| 33 | Test classification non-overlapping (unit vs integration) | Static | file paths + `//go:build integration` tag presence |
| 34 | Language_Definition filenames are safe basenames | PBT | `internal/security` (`ValidateBasename`) |
| 35 | Filename validation invoked at both load and request time | Unit | `internal/registry` + `internal/runner` |
| 36 | `Code_Submission` cannot influence argv, env, or shell | PBT | `internal/runner` (argv builder over arbitrary submissions) |
| 37 | Placeholder substitution uses closed allowlist | PBT | `internal/runner` |
| 38 | Streaming output capture memory-bounded | PBT | `internal/runner` (capture discarder) |
| 39 | `Sandbox_Job_Directory` uniqueness and exclusivity | PBT | `internal/runner` (UUIDv4 path generator) |
| 40 | Cleanup total across all termination modes | Integration | `tests/integration` |
| 41 | `Sandbox_Runner` refuses foreign workspace paths | Unit | `internal/runner` |
| 42 | Startup prerequisites enforced before HTTP listener | Integration | `tests/integration` |

Properties 1–2 do not have implementation-spec tests because they constrain
the architecture document itself, not the runtime. Properties 31 and 33 are
enforced by repository layout and build-tag presence, not by Go test code.
Property 32 is enforced by a parser of `go list -deps` output that fails on
any cycle or any edge not declared in the architecture design §5 table.

## Error Handling

Error handling is inherited from the architecture design §"Error Handling"
(five-layer model: Transport / Routing / Validation / Execution / Observer).
This implementation surfaces errors exactly as that section dictates: HTTP
400 for validation, 404 for unknown paths, 405 for wrong method on a known
path, 429 for capacity exhaustion, 500 for unexpected internal failure on a
recognised path, 503 for `/readyz` while not ready or shutting down, and
HTTP 200 carrying `Execution_Result.status ∈ {COMPILATION_ERROR,
RUNTIME_ERROR, TIME_LIMIT_EXCEEDED, MEMORY_LIMIT_EXCEEDED, INTERNAL_ERROR}`
for sandbox-classified outcomes. Observer (metrics/log) emission failure
never alters the request's response (Property 25).

## Testing Strategy

Testing Layers

Three test layers exist, distinguished only by file path and build tag —
matching architecture design §"Testing Strategy" §15.5 and §20.1–20.3.

| Layer | Path | File suffix | Build tag | Selected by | Requires nsjail? |
| --- | --- | --- | --- | --- | --- |
| Unit | `internal/<pkg>/` | `*_test.go` | none | `make test` | no |
| Property (PBT) | `internal/<pkg>/` | `*_property_test.go` | none | `make test` | no |
| Integration | `tests/integration/` | `*_test.go` | `//go:build integration` | `make integration` | yes (skipped when absent) |

Property tests use `pgregory.net/rapid`. They never spawn `nsjail`; they
exercise pure functions (argv builder, status classifier, basename
validator, placeholder substitutor, capture discarder, queue ordering, etc.)
or in-memory stubs. Any test that needs `nsjail` belongs under
`tests/integration/` with the build tag.

A unit-tagged file in `tests/integration/` or an integration-tagged file
under `internal/` is malformed (Property 33). The structure test in
`tests/structure_test.go` (no build tag) flags this at CI time.

## Package Implementation Order

The order matches the architecture design's three implementation phases.
Each phase must be demonstrable end-to-end before the next phase starts;
this avoids long-lived branches and keeps the dependency graph healthy.

### Phase 1 — HTTP skeleton

Implements the package set needed to serve `GET /healthz`, `GET /readyz`,
`GET /info`, and a `POST /run` stub returning HTTP 503 `not_implemented`,
with the YAML registry loaded and the config validated.

1. `cmd/goboxd` — entry point: load config, init logger, init metrics
   registry, load language registry, build HTTP server, install signal
   handlers, run until shutdown.
2. `internal/version` — compile-time `Version` and `ServiceName` constants.
3. `internal/config` — env+YAML loader with documented precedence and range
   validation; emits `goboxd_startup_prereq_failures_total` on
   misconfiguration before exit.
4. `internal/log` — `slog`-based JSON logger with UUIDv4 + ISO 8601 UTC ms
   helpers and the redaction contract.
5. `internal/metrics` — Prometheus registry, `goboxd_build_info`,
   `goboxd_dropped_metrics_total`, `goboxd_dropped_logs_total`, exposer at
   `GET /metrics`.
6. `internal/registry` — YAML load, basename validation at load
   (delegating to `internal/security`), placeholder allowlist check at
   load, `Get`/`List`, immutable snapshot.
7. `internal/security` (filename + placeholder validators only — Phase 1
   scope per architecture design Phase 1) — `ValidateBasename`,
   placeholder allowlist constants. The full `Validate(submission)` API
   lands in Phase 2.
8. `internal/api/handlers` — `Health_Handler`, `Info_Handler`, `Run_Handler`
   stub returning HTTP 503 `not_implemented`.
9. `internal/api` — `API_Server`: routing, method allow-lists, 1 MiB body
   cap, 404/405 partition, shutdown coordination, `/metrics` route.

### Phase 2 — `POST /run` end-to-end with `py3`

Adds the execution path. After this phase, `POST /run` with
`{"language":"py3", ...}` runs real Python inside `nsjail`.

10. `internal/security` — full `Validate(submission)` rule catalog
    (`source_size_exceeded`, `stdin_size_exceeded`, `language_not_registered`,
    `resource_limit_exceeded`, `malformed_submission`).
11. `internal/runner` — argv builder, status classifier, capture discarders,
    workspace allocator (UUIDv4 dirs under `SANDBOX_ROOT_DIR`), ownership
    invariant, cleanup, orphan reaper.
12. `internal/worker` — bounded worker pool with FIFO queue and drain
    semantics, queue-depth gauge updates.
13. `internal/api/handlers` (Run_Handler) — full implementation: validate →
    enqueue → await → respond, with deferred cleanup contract delegating to
    `Sandbox_Runner.CleanupOnly`.

### Phase 3 — Full Metrics_Collector and `cpp`

14. `internal/metrics` — full counter/gauge/histogram set per architecture
    design §14 (run requests by language+status, run duration histogram,
    queue depth gauge, security rejections by rule, sandbox cleanup
    failures, unsafe filename, unknown placeholder, workspace isolation
    violation, orphan workspace reaped, etc.).
15. Add `cpp` to the default `language_registry.yaml` and exercise
    `compile.command` + `binary_filename` plumbing in `internal/runner`.
16. Integration tests: `cpp-hello`, `compile-error`, metrics scrape
    consistency.

The list above is ten concrete `internal/` packages plus `cmd/goboxd`. Each
package is implemented in a single PR with its unit and property tests
landing alongside the production code.

## Out of Scope

- Modifying `Dockerfile`, `docker-compose.yml`, or `Makefile` (architecture
  design Req 2.1).
- Adding new top-level directories beyond `cmd/`, `internal/`, `docs/`,
  `tests/`.
- Running on non-Linux operating systems.
- Adding languages beyond `py3` and `cpp` (the architecture design's
  "add a third language" worked example documents the YAML-only path).
- Changes to the Go module path or major Go version.

