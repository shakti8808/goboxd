# Architecture Decision Records

Each ADR below is grounded in code, spec text, or commit evidence. Where
alternatives were not recorded during development, this is stated
explicitly and the ADR records only the final decision.

---

## ADR-1: Two-spec split — architecture (docs-only) and implementation (code)

**Context:**
The hackathon work needed both a design document and a working Go binary.
Architecture Req 2.3 forbids creating, modifying, or deleting any `.go`
file as part of the architecture spec — the deliverable is documentation
only. A separate spec was therefore needed for the runtime artefacts.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
Two Kiro specs were created:
- `.kiro/specs/goboxd-architecture/` — `workflowType: requirements-first`,
  produces `docs/architecture.md`, no Go files
- `.kiro/specs/goboxd-implementation/` — `workflowType: design-first`,
  produces Go source under `cmd/goboxd/` and `internal/`, inherits all
  27 architecture requirements verbatim

**Rationale:**
Recorded in `.kiro/specs/goboxd-implementation/requirements.md`
"Inheritance" section: the implementation spec inherits architecture
Reqs 1–27 unchanged but is "permitted and expected to produce Go source
code", whereas the architecture spec is documentation-only by Req 2.3.
The split keeps the two acceptance surfaces independent and traceable.

---

## ADR-2: Three-phase delivery — HTTP skeleton → py3 end-to-end → metrics + cpp

**Context:**
The implementation spec needed an order of work that produced a
demonstrable artefact at each phase boundary, while keeping the
dependency graph between packages healthy.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
Three phases as recorded in `.kiro/specs/goboxd-architecture/design.md`
§"Implementation Phases" and `.kiro/specs/goboxd-implementation/design.md`
§"Package Implementation Order":
1. **Phase 1 — HTTP skeleton.** Packages: version, config, log, metrics
   (skeleton), registry, security (filename + placeholder validators
   only), api, api/handlers (Run_Handler stub), cmd/goboxd. Demo gate:
   `/healthz`, `/readyz`, `/info`, `/metrics` return 200 within 1s;
   `POST /run` returns 503 not_implemented.
2. **Phase 2 — POST /run end-to-end with py3.** Adds full
   security.Validate, the runner package (argv, classifier, capture,
   workspace, reaper, exec, orchestrator), the worker package, and the
   full Run_Handler.
3. **Phase 3 — full metrics + cpp.** Extends `internal/metrics` to the
   full architecture §14 catalog, adds the cpp compile step, and adds
   `tests/integration/metrics_scrape_test.go`.

**Rationale:**
Each phase is demonstrable (Architecture Req 19.2). The order is the
topological order of the dependency graph (design.md §5). Commits
realise the phases: `eca4b19` (Phase 1), `44ac6a5` + `7bd3ac8` (Phase 2),
`4451ac7` (Phase 3).

---

## ADR-3: Closed third-party Go dependency set

**Context:**
The runtime needs YAML parsing, UUID generation, Prometheus metrics, and
property-based testing. A bounded dependency surface limits supply-chain
risk and keeps `go mod tidy` deterministic.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
Exactly four third-party modules, listed in
`.kiro/specs/goboxd-implementation/design.md` §"Third-party dependencies":
- `gopkg.in/yaml.v3` (Language_Registry YAML)
- `github.com/google/uuid` (UUIDv4 request IDs and sandbox dirs)
- `github.com/prometheus/client_golang` (Metrics_Collector + exposition)
- `pgregory.net/rapid` (property-based tests)

Everything else uses the Go 1.23 standard library.

**Rationale:**
Implementation Req I-2.2 mandates this exact list and forbids additions
without an amendment to design.md. `go.mod` declares these four modules
and no others as direct dependencies.

---

## ADR-4: Acyclic `internal/` dependency graph enforced at test time

**Context:**
The architecture spec declares an acyclic dependency graph between
`internal/` packages (design.md §5, Property 32). Drift would silently
violate the architecture.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
A package-layout test at `tests/structure_test.go` (no build tag, runs
under `make test`) parses `go list` output and fails if any edge appears
that is not declared in design.md §5, or if any cycle is detected.

**Rationale:**
Recorded in `.kiro/specs/goboxd-implementation/design.md` §5 "Package
boundary constraints": "A unit test in `internal/api` (or a small
`tests/structure_test.go` with no build tag) parses `go list -deps`
output and fails if any forbidden edge appears. This validates Property
32 (acyclic dependency graph) at CI time."

---

## ADR-5: Property-based testing via `pgregory.net/rapid`, ≥100 iterations

**Context:**
The architecture spec defines 42 correctness properties. Many are
universally quantified ("for any …"), so example-based unit tests cannot
adequately validate them.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
Properties classified as PBT in
`.kiro/specs/goboxd-implementation/design.md` §"Correctness Property
Classification" are implemented in `*_property_test.go` files using
`pgregory.net/rapid`, run for at least 100 iterations per invocation.

**Rationale:**
Implementation Req I-4.2 mandates ≥100 iterations. Six PBT files exist
in the repository:
- `internal/runner/argv_property_test.go`
- `internal/runner/capture_property_test.go`
- `internal/runner/classifier_property_test.go`
- `internal/runner/reaper_property_test.go`
- `internal/runner/workspace_property_test.go`
- `internal/worker/pool_property_test.go`

---

## ADR-6: Three-layer test split, integration tests selected by build tag

**Context:**
NsJail-dependent tests cannot run in environments without NsJail. Mixing
them with fast unit tests would block local development.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
Three layers, distinguished only by file path and build tag:
- Unit tests: `internal/<pkg>/*_test.go`, no build tag
- Property tests: `internal/<pkg>/*_property_test.go`, no build tag
- Integration tests: `tests/integration/*_test.go` carrying
  `//go:build integration`

`make test` runs the first two; `make integration` runs the third.
Integration tests `t.Skip()` when nsjail is unavailable.

**Rationale:**
Implementation Req I-3.1 through I-3.5; Architecture Req 20.1–20.3.
All 19 files under `tests/integration/` carry the build tag. The
`Makefile` `integration` target invokes
`go test -tags=integration ./tests/...`.

---

## ADR-7: Frozen infrastructure files (Task 32)

**Context:**
The upstream provided `Dockerfile`, `docker-compose.yml`, and `Makefile`
in commit `ea5ab73` ("Bootstrap project scaffolding"). Architecture Req
2.1 declares those files out of scope for modification.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
No commit on `team/glitchbox` modifies, renames, or deletes any of the
three files. Implementation Req I-6.1 restates the constraint, and
Implementation Req I-6.2 specifies that any feature appearing to require
such a change must be raised as a separate spec.

**Rationale:**
Architecture Req 2.1 is the source. Verified by `git log -- Dockerfile
docker-compose.yml Makefile` showing no modifications since `ea5ab73`.

**Consequence:**
Task 30 (`make integration` exits 0) is structurally unfixable from this
codebase because the fix lives in `docker-compose.yml`. See
`docs/ai/issues.md` 2026-06-01 nsjail clone() EPERM entry.

---

## ADR-8: Closed argv-placeholder allowlist; validate at registry load and request time

**Context:**
The architecture spec forbids any `Code_Submission` field from
influencing argv (Req 22). Placeholder substitution in
`compile.args` / `run.args` is the only template surface that resolves
per request.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
Closed allowlist `{SOURCE_FILENAME, BINARY_FILENAME, STDIN_FILE}` defined
in `internal/security/` and consulted from two call sites:
- `internal/registry/` at startup load (rejects unknown placeholders by
  exiting non-zero)
- `internal/runner/argv.go` `expandPlaceholders()` at request time
  (returns `INTERNAL_ERROR`)

**Rationale:**
Architecture Req 22.3 mandates the closed allowlist. Property 35
mandates that the same validator runs at both call sites — verified by
the `internal/security/filename.go` allowlist being imported by both
`internal/registry/` and `internal/runner/argv.go`.

---

## ADR-9: Workspace path = `<SANDBOX_ROOT_DIR>/job-<UUIDv4>` with ownership invariant

**Context:**
Concurrent requests must not share or observe each other's
Sandbox_Job_Directory contents (Architecture Req 24).

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
- Path scheme: `<SANDBOX_ROOT_DIR>/job-<UUIDv4>` produced from
  `github.com/google/uuid` (cryptographically secure)
- Ownership invariant: `Sandbox_Runner` records the path it created and
  refuses any operation on a path it did not create (returns
  `INTERNAL_ERROR`, increments
  `goboxd_workspace_isolation_violation_total`, emits ERROR log)
- Cleanup: deferred from Run_Handler, runs on every termination mode
- Orphan reaper: periodic goroutine that removes
  `<SANDBOX_ROOT_DIR>/job-*` entries older than `SANDBOX_ORPHAN_TTL_S`

**Rationale:**
Architecture Req 24.1, 24.3, 24.4, 24.5, 26.8. Implemented in
`internal/runner/workspace.go` (allocator + ownership check) and
`internal/runner/reaper.go` (orphan reaper).

---

## ADR-10: Observer interfaces invert dependencies between runner/worker and metrics/log

**Context:**
The architecture-mandated dependency graph (design.md §5) makes
`internal/runner` and `internal/worker` not depend on `internal/metrics`
or `internal/log`. But the runner and worker need to emit metrics and
logs.

**Options considered:**
Alternative options were not recorded during development; only the final
decision is evidenced.

**Decision:**
Each of the runner sub-components defines an observer interface that
its host package consumes. The interfaces are implemented in
`cmd/goboxd/observers.go` (added in commit `7bd3ac8`), which is the
single place where runner/worker components are wired to the real
metrics collector and logger.

Interfaces present in code:
- `runner.SandboxObserver` (`internal/runner/runner.go` line 38):
  `OnCleanupFailure`, `OnNsJailFailure`
- `runner.WorkspaceObserver` (`internal/runner/workspace.go` line 85):
  `OnIsolationViolation`
- `runner.ReaperObserver` (`internal/runner/reaper.go` line 33):
  `OnReap`, `OnReapFailure`
- `worker.Observer` (`internal/worker/pool.go` line 77):
  `OnQueueDepthChanged`, `OnSubmissionRejected`

**Rationale:**
Code comments at each interface declaration explicitly state the
rationale, e.g. `internal/runner/runner.go`: "The runner does not
import either package directly so the architecture §5 dependency graph
stays acyclic; tests inject a recording observer to assert the surface."
The pattern keeps `tests/structure_test.go` green while still letting
the runtime emit metrics and structured logs.
