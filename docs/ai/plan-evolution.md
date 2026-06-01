# Plan Evolution

Only changes that are observable in commit history, spec files, or
repository state are recorded here. Motivations are stated only where the
evidence supports them; otherwise the entry is left as observation only.

---

## 2026-05-31 · Two-spec split (architecture docs-only / implementation code)

**What we thought we'd do:**
A single Kiro spec covering both design and implementation.

**What we actually did:**
Two separate Kiro specs were created in commit `eca4b19`:
- `.kiro/specs/goboxd-architecture/` — `workflowType: requirements-first`,
  `specType: feature`, deliverable is `docs/architecture.md` (Req 2.3
  forbids `.go` files)
- `.kiro/specs/goboxd-implementation/` — `workflowType: design-first`,
  `specType: feature`, inherits all 27 architecture requirements verbatim
  and adds 6 I-prefixed implementation requirements

**Why it changed:**
Recorded in `.kiro/specs/goboxd-implementation/requirements.md` "Inheritance"
section: the architecture spec's Req 2.3 forbids producing Go source code
under that spec, so a second spec was needed for the implementation
artefacts. The two `.config.kiro` files document distinct workflow types
chosen at spec creation time.

---

## 2026-06-01 · Phase 2 split into Waves A–D and Wave E

**What we thought we'd do:**
The implementation spec lists Phase 2 as a single phase covering tasks
13–24 (`.kiro/specs/goboxd-implementation/tasks.md` §"Phase 2 — POST /run
end-to-end with py3"). No "wave" terminology appears in tasks.md.

**What we actually did:**
Phase 2 was delivered in two commits:
- `44ac6a5` "Complete Phase 2 Waves A-D sandbox execution pipeline"
  (36 files, 7551 insertions; tasks 13–21)
- `7bd3ac8` "Complete Phase 2 Wave E (Tasks 22-24)"
  (28 files, 2465 insertions; tasks 22–24)

**Why it changed:**
The split is observable in the commit messages and the task ranges they
cover. The repository does not record an explicit motivation. Observation
only: the Wave A–D commit landed the pure-function and orchestrator
machinery in `internal/runner` plus `internal/worker` plus
`internal/security`; the Wave E commit landed the `cmd/goboxd` wiring,
startup prereq checks, and the integration test set that depends on
the previous wave. The order matches the dependency graph between tasks.

---

## 2026-06-01 · `tasks.md` checkboxes never updated as work progressed

**What we thought we'd do:**
Standard Kiro spec workflow keeps `- [ ]` task checkboxes synchronised
with completion state.

**What we actually did:**
`.kiro/specs/goboxd-implementation/tasks.md` was committed once in
`eca4b19` and never modified afterwards. All 33 task checkboxes still
read `- [ ]` despite tasks 1–31 having been delivered across commits
`eca4b19`, `44ac6a5`, `7bd3ac8`, `4451ac7`, `db4b14a`, `5141b1c`. Same is
true of `.kiro/specs/goboxd-architecture/tasks.md`.

**Why it changed:**
No motivation is recorded. Observation only.

---

## 2026-06-01 · Task 30 reclassified from "make integration must exit 0" to BLOCKED OUTSIDE CODEBASE

**What we thought we'd do:**
`.kiro/specs/goboxd-implementation/tasks.md` Task 30 acceptance criterion:
*"`make integration` (delegates to `go test -tags=integration ./tests/...`)
exits 0 with `nsjail` available on `NSJAIL_PATH`; every integration test
in `tests/integration/` either passes or `t.Skip()`s with a documented
skip reason."*

**What we actually did:**
Two artefacts were produced under Task 30:

1. Commit `db4b14a` "Task 30 arm64 integration compatibility fix" — added
   16 lines to `tests/integration/helper_test.go` overriding
   `SANDBOX_RO_MOUNTS` when `runtime.GOOS=="linux"` and `os.Stat("/lib64")`
   reports the path missing (Debian arm64 base images do not ship
   `/lib64`).

2. Investigation establishing that the `tools` service in
   `docker-compose.yml` is unprivileged, that
   `clone(CLONE_NEW…)` therefore returns `EPERM`, that the same image
   succeeds under `docker run --privileged`, and that the fix would
   require editing `docker-compose.yml` — forbidden by Architecture
   Req 2.1 and Implementation Req 6.1. Investigation captured in this
   `docs/ai/` tree; not committed elsewhere.

**Why it changed:**
The acceptance criterion implicitly assumed the `tools` container could
create namespaces. It cannot, given the frozen infrastructure constraint.
The task was reclassified rather than the constraint relaxed.

---

## 2026-06-01 · Task 31 delivered as pure deletion

**What we thought we'd do:**
Task 31 in `.kiro/specs/goboxd-implementation/tasks.md` is "make lint
passes" — primarily a verification task.

**What we actually did:**
Commit `5141b1c` "Final acceptance cleanup (Task 31)" — 4 files modified,
0 insertions, 31 deletions:
- `cmd/goboxd/main.go` (-9)
- `internal/api/handlers/run_full_test.go` (-7)
- `internal/runner/testhelpers_test.go` (-13)
- `internal/worker/queue.go` (-2)

**Why it changed:**
No explicit motivation in the commit body. Observation only: the diff is
entirely unreachable / unused code, consistent with `make lint` flagging
issues that the cleanup commit resolved by deletion rather than
modification.

---

## 2026-06-01 · `docs/architecture.md` deliverable identified as missing during final audit

**What we thought we'd do:**
Architecture Req 1.1 mandates `docs/architecture.md` as the deliverable
for the architecture spec. The expectation embedded in the spec
(design.md line 9) is that `docs/architecture.md` would be written by
"mechanical expansion of the sections below".

**What we actually did:**
The file was never produced during Phase 1, Phase 2, or Phase 3.
`docs/` contained only `.gitkeep` from the `ea5ab73` upstream
scaffolding commit. The gap was identified during the documentation
audit that produced this `docs/ai/` tree, and `docs/architecture.md` was
generated by transforming `.kiro/specs/goboxd-architecture/design.md`
into the architecture-document section ordering required by Req 1.2–1.7.

**Why it changed:**
No earlier task in `.kiro/specs/goboxd-implementation/tasks.md` claims
ownership of this deliverable. Observation only: the architecture-spec
deliverable was tracked as a documentation outcome rather than an
implementation task, and was missed when implementation tasks drove the
work plan.
