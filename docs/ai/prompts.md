# AI-Assisted Development — Prompt Log

This log records the major AI-assisted work blocks performed against this
repository. Original prompt text was not preserved during development;
entries below are paraphrased from the work products visible in git history,
the Kiro spec workflow records under `.kiro/specs/`, and the implementation
artefacts under `cmd/`, `internal/`, `configs/`, and `tests/`.

Where an entry says "Prompt paraphrased from development activity; original
prompt not preserved", the entry is grounded only in the resulting commit
and the documents it produced — not in any captured prompt text.

---

## 2026-05-31 · Architecture spec generation (Kiro requirements-first workflow)

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
The work was driven through the Kiro requirements-first spec workflow as
recorded in `.kiro/specs/goboxd-architecture/.config.kiro`
(`"workflowType": "requirements-first"`). The high-level intent visible from
the resulting documents was to produce a design-only deliverable for a Go
HTTP service that compiles and executes untrusted code inside NsJail
sandboxes, scoped to `cmd/goboxd/`, `internal/`, `docs/`, `tests/`.

**Response summary:**
Produced three spec files committed in `eca4b19`:
- `.kiro/specs/goboxd-architecture/requirements.md` (409 lines, 27 requirements in EARS form)
- `.kiro/specs/goboxd-architecture/design.md` (1391 lines, 42 correctness properties)
- `.kiro/specs/goboxd-architecture/tasks.md` (444 lines)

**What we used / didn't use:**
Used: the requirements as ground truth for every subsequent phase (REQ-A-1
through REQ-A-27 are referenced from implementation tasks). Used: the
correctness properties (1–42) as the basis for the test classification
matrix in the implementation spec. Did not use: the architecture
design.md was treated as planning material — it was never copied into
`docs/architecture.md`, which Req 1.1 mandates. That deliverable gap was
identified during the documentation audit and is the reason this `docs/`
tree now exists.

---

## 2026-05-31 · Implementation spec generation (Kiro design-first workflow)

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
Work was driven through Kiro's design-first workflow as recorded in
`.kiro/specs/goboxd-implementation/.config.kiro`
(`"workflowType": "design-first"`). The intent visible from the resulting
documents was to bridge from the documentation-only architecture spec to
working Go source code, while inheriting all 27 architecture requirements
verbatim and adding only implementation-specific constraints.

**Response summary:**
Produced three spec files committed in `eca4b19`:
- `.kiro/specs/goboxd-implementation/requirements.md` (80 lines, 6 I-prefixed requirements)
- `.kiro/specs/goboxd-implementation/design.md` (384 lines, including the 42-property classification matrix mapping each correctness property to PBT / Unit / Integration / Static)
- `.kiro/specs/goboxd-implementation/tasks.md` (337 lines, 33 tasks across 3 phases)

**What we used / didn't use:**
Used: the property classification matrix drove the location of every test
file. Six `*_property_test.go` files exist
(`internal/runner/{argv,capture,classifier,reaper,workspace}_property_test.go`,
`internal/worker/pool_property_test.go`); 19 integration tests exist under
`tests/integration/` carrying `//go:build integration`. Used: the closed
third-party dependency list (`yaml.v3`, `google/uuid`,
`prometheus/client_golang`, `pgregory.net/rapid`) — `go.mod` matches.
Did not use: tasks.md checkboxes were not maintained as work progressed;
all checkboxes remain `[ ]` in the file even though tasks 1–31 were
delivered. This is documented in `docs/ai/plan-evolution.md`.

---

## 2026-05-31 · Phase 1 implementation — HTTP skeleton

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
Implemented Phase 1 from the implementation spec
(`.kiro/specs/goboxd-implementation/design.md` §"Phase 1 — HTTP skeleton",
tasks 1–12).

**Response summary:**
Single commit `eca4b19` "Complete Phase 1 foundation implementation" — 42
files, 6918 insertions. Delivered:
- `cmd/goboxd/main.go` (entry point: load config, init logger/metrics, load
  registry, build HTTP server, signal handlers)
- `internal/version/`, `internal/config/`, `internal/log/`,
  `internal/metrics/` (Phase 1 skeleton: build_info, dropped counters)
- `internal/registry/` (YAML loader with basename + placeholder validation)
- `internal/security/` (Phase 1 partial: `ValidateBasename`, placeholder
  allowlist)
- `internal/api/` and `internal/api/handlers/` (Health_Handler, Info_Handler,
  Run_Handler stub returning 503 not_implemented)
- `tests/structure_test.go` (parses `go list` output to enforce the
  acyclic dependency graph from design.md §5)
- `configs/language_registry.yaml` (py3 + cpp entries)

**What we used / didn't use:**
Used: every Phase 1 deliverable named in design.md §"Phase 1". Used: the
`tests/structure_test.go` realises Property 32 (acyclic graph). Did not
use: no scope was reduced; Phase 1 was delivered as specified.

---

## 2026-06-01 · Phase 2 Waves A–D — sandbox execution pipeline

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
Implemented tasks 13–21 from
`.kiro/specs/goboxd-implementation/tasks.md` §"Phase 2".

**Response summary:**
Single commit `44ac6a5` "Complete Phase 2 Waves A-D sandbox execution
pipeline" — 36 files, 7551 insertions. Delivered:
- `internal/runner/argv.go`, `argv_test.go`, `argv_property_test.go` (pure
  argv builder; Properties 17, 36, 37)
- `internal/runner/classifier.go`, `classifier_test.go`,
  `classifier_property_test.go` (status classifier; Property 19)
- `internal/runner/capture.go`, `capture_test.go`,
  `capture_property_test.go` (memory-bounded discarders; Properties 18, 38)
- `internal/runner/workspace.go`, `workspace_test.go`,
  `workspace_property_test.go` (UUIDv4 paths; Properties 39, 41)
- `internal/runner/reaper.go`, `reaper_test.go`,
  `reaper_property_test.go` (orphan reaper goroutine)
- `internal/runner/runner.go`, `exec.go`, `runner_test.go` (orchestrator
  wiring)
- `internal/security/validator.go`, `validator_test.go` (full Validate rule
  catalog)
- `internal/worker/pool.go`, `queue.go`, `pool_test.go`,
  `pool_property_test.go` (bounded FIFO pool; Properties 12, 13, 14, 15)
- `internal/api/handlers/run_full.go`, `run_full_test.go` (full Run_Handler)

**What we used / didn't use:**
Used: every property listed in the design.md classification matrix for the
runner, security, and worker packages received its corresponding test file.
Did not use: no scope deletion observed in the commit.

---

## 2026-06-01 · Phase 2 Wave E — startup wiring + integration tests

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
Implemented tasks 22–24 from `.kiro/specs/goboxd-implementation/tasks.md`.

**Response summary:**
Single commit `7bd3ac8` "Complete Phase 2 Wave E (Tasks 22-24)" — 28 files,
2465 insertions. Delivered:
- `cmd/goboxd/main.go` extended with full Phase 2 wiring (validator, pool,
  runner, orphan reaper)
- `cmd/goboxd/observers.go` (concrete `SandboxObserver`, `WorkspaceObserver`,
  `ReaperObserver` implementations bridging `internal/runner` to
  `internal/metrics` + `internal/log`)
- `cmd/goboxd/prereq.go`, `prereq_test.go` (sandbox root, RO mounts, nsjail
  binary checks before listener opens; REQ-A-25)
- 14 integration tests under `tests/integration/` realising Properties 16,
  20, 21, 22, 28 (partial), 40, 41, 42 — including `py3_hello_test.go`,
  `unknown_language_test.go`, `oversize_source_test.go`,
  `queue_full_test.go`, `time_limit_test.go`,
  `path_traversal_source_filename_test.go`,
  `command_injection_via_source_test.go`,
  `shell_metachar_stdin_test.go`,
  `excessive_stdout_test.go`, `excessive_stderr_test.go`,
  `workspace_isolation_concurrent_test.go`,
  `orphan_workspace_reaped_test.go`, `startup_prereq_test.go`,
  `invalid_filename_control_bytes_test.go`
- `tests/integration/helper_test.go`, `main_test.go` (test harness)

**What we used / didn't use:**
Used: every integration scenario named in tasks.md §24 was committed.
Did not use: scope was unchanged.

---

## 2026-06-01 · Phase 3 — full metrics + cpp compile step

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
Implemented tasks 25–27 from `.kiro/specs/goboxd-implementation/tasks.md`.

**Response summary:**
Single commit `4451ac7` "Phase 3 complete (Tasks 25-27)" — 13 files, 851
insertions. Delivered:
- `internal/metrics/metrics.go` extended to the full architecture §14
  catalog (run_requests_total, run_duration_seconds histogram,
  worker_pool_queue_depth, security_rejections_total,
  sandbox_cleanup_failures_total, unsafe_filename_total,
  unknown_placeholder_total, workspace_isolation_violation_total,
  orphan_workspace_reaped_total, startup_prereq_failures_total)
- `internal/metrics/metrics_test.go` extended (Properties 29, 30 tests added)
- `internal/metrics/observer_isolation_test.go` (Property 25)
- `internal/runner/runner.go`, `argv.go`, `types.go`, `runner_test.go`
  extended with compile-step plumbing
- `tests/integration/cpp_hello_test.go`, `compile_error_test.go`,
  `metrics_scrape_test.go`

**What we used / didn't use:**
Used: every metric in design.md §14 is registered; cpp compile step works
end-to-end in tests under privileged Linux. Did not use: separate
`*NilSafe` unit tests were not added for `IncUnsafeFilename` and
`IncUnknownPlaceholder` despite the established pattern; this gap is
recorded in `docs/ai/issues.md`. The implementations themselves are
nil-guarded — only the explicit tests are missing.

---

## 2026-06-01 · Task 30 investigation — make integration acceptance

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
Investigated `make integration` failures observed during final acceptance,
classifying root cause without modifying frozen infrastructure files
(Task 32 / Architecture Req 2.1 / Implementation Req 6.1).

**Response summary:**
Two outcomes:

1. **arm64 `/lib64` workaround.** Commit `db4b14a` "Task 30 arm64
   integration compatibility fix" — 16 lines added in
   `tests/integration/helper_test.go`. The helper detects
   `runtime.GOOS=="linux"` plus `os.Stat("/lib64")` returning
   "not exist" and overrides `SANDBOX_RO_MOUNTS` to omit `/lib64`, since
   Debian arm64 base images do not ship `/lib64`.

2. **Unprivileged-`tools`-container EPERM.** Investigation produced in
   conversation, not committed. Reproduction:
   `docker compose --profile tools run --rm tools nsjail -Mo …` →
   `clone(CLONE_NEWNS|CLONE_NEWCGROUP|CLONE_NEWUTS|CLONE_NEWIPC|CLONE_NEWUSER|CLONE_NEWPID|CLONE_NEWNET)`
   `failed: Operation not permitted`. Same image + `docker run --privileged`
   succeeds. Root cause: the `tools` service in `docker-compose.yml` lacks
   `privileged: true` (the `goboxd` service has it, but
   `make integration` runs in `tools`). Resolution requires editing
   `docker-compose.yml`, which Task 32 forbids. Classified as BLOCKED
   OUTSIDE CODEBASE.

**What we used / didn't use:**
Used: the arm64 `/lib64` workaround as a test-helper-only environment
override. Did not use: any code-side workaround for the EPERM blocker —
all candidate code-side fixes either violated architecture §10.2
("every job runs inside nsjail with the seven namespaces") or hid a
genuine failure mode behind a softer status. The blocker remains
infrastructure-bound.

---

## 2026-06-01 · Task 31 — final acceptance cleanup

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
Cleanup pass to remove dead/unreachable code surface flagged during
acceptance review.

**Response summary:**
Single commit `5141b1c` "Final acceptance cleanup (Task 31)" — 4 files
modified, 0 insertions, 31 deletions:
- `cmd/goboxd/main.go` (9 lines removed)
- `internal/api/handlers/run_full_test.go` (7 lines removed)
- `internal/runner/testhelpers_test.go` (13 lines removed)
- `internal/worker/queue.go` (2 lines removed)

**What we used / didn't use:**
Used: pure deletion — net negative LOC. Did not use: no behaviour change;
no test added or removed; `make test` and `make lint` continued to pass.

---

## 2026-06-01 · Documentation audit (this docs/ tree)

**Prompt:**
Prompt paraphrased from development activity; original prompt not preserved.
The hackathon discussion was identified as requiring `docs/ai/prompts.md`
plus companion AI documentation. The architecture spec
(`.kiro/specs/goboxd-architecture/requirements.md` Req 1.1) was identified
as also requiring `docs/architecture.md`, which had never been produced.

**Response summary:**
Audit confirmed `docs/` contained only `.gitkeep`. The decision was
made to mechanically transform `.kiro/specs/goboxd-architecture/design.md`
into `docs/architecture.md` (rather than copy verbatim, since design.md's
section headers literally read "(plan for docs/architecture.md §N)" and
its section ordering does not match the architecture-document order
required by Req 1.2–1.7). Six companion AI docs were created from
repository evidence — git history, specs, code — without inventing
prompts, alternatives, or decisions that the repository does not record.

**What we used / didn't use:**
Used: commit log, spec files, code grep, and reproduction transcripts as
evidence. Did not use: any speculation about author intent; any
alternative-considered framing not present in the specs; any in-repo
audit-report artefacts (none exist — the Phase 2 Wave E audit, the
Phase 3 audit, and the Task 30 investigation were produced in
development conversations and not committed).
