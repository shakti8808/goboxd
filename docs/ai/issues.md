# Issue Log

Confirmed issues — each grounded in a commit, code location, or
specification clause — appear under the first heading. Investigation-only
findings that surfaced during development but were not fixed by a
committed change appear under the second heading.

---

## Confirmed issues

### 2026-06-01 · arm64 `/lib64` absence broke integration test startup

**What we were trying to do:**
Run `make integration` on the macOS arm64 development host (Docker
Desktop 4.74.0, linux/arm64 LinuxKit VM, runc 1.3.5). The integration
test harness in `tests/integration/helper_test.go` starts a real
`goboxd` process which performs the startup prerequisite checks from
`cmd/goboxd/prereq.go`, including verifying every path in
`SANDBOX_RO_MOUNTS` is readable.

**What went wrong:**
The default `SANDBOX_RO_MOUNTS` value (configured in
`internal/config/config.go`) includes `/lib64:/lib64`. Debian
arm64 base images do not ship `/lib64` (only `/lib`). The startup
prereq check therefore failed before the HTTP listener opened, and
every integration test reported the harness timing out waiting for the
server.

**How we resolved it:**
Commit `db4b14a` "Task 30 arm64 integration compatibility fix" — added
16 lines to `tests/integration/helper_test.go` that detect
`runtime.GOOS=="linux"` plus `os.Stat("/lib64")` returning
"not exist", and override `SANDBOX_RO_MOUNTS` to omit `/lib64`. The
override is test-helper-only; production behaviour on amd64 Linux
hosts (where `/lib64` exists) is unchanged.

**What we learned:**
The architecture spec lists `[/usr:/usr,/lib:/lib,/lib64:/lib64,
/bin:/bin,/etc/alternatives:/etc/alternatives]` as the
`SANDBOX_RO_MOUNTS` default (see Architecture design §15
Configuration Reference table). That default is amd64-centric. arm64
Linux distributions deliberately do not ship `/lib64`. Future work
should either make the default platform-aware or document the
expectation that operators tune `SANDBOX_RO_MOUNTS` per architecture.

---

## Investigation-only findings

### 2026-06-01 · `make integration` blocked by unprivileged `tools` container — clone() returns EPERM

**What we were trying to do:**
After resolving the arm64 `/lib64` issue, run `make integration` to
satisfy Task 30 acceptance criterion: *"`make integration` exits 0 with
nsjail available on `NSJAIL_PATH`; every integration test passes or
documents a skip reason."*

**What went wrong:**
Reproduction inside the `tools` container:

    docker compose --profile tools run --rm tools \
      nsjail -Mo -R /bin:/bin -R /usr:/usr -R /lib:/lib -- /bin/echo hello

Output:

    [W] runChild():491
        clone(flags=CLONE_NEWNS|CLONE_NEWCGROUP|CLONE_NEWUTS|CLONE_NEWIPC|
                    CLONE_NEWUSER|CLONE_NEWPID|CLONE_NEWNET) failed:
        Operation not permitted
    [E] standaloneMode():275 Couldn't launch the child process

The runtime classifier in `internal/runner/runner.go` correctly maps
this nsjail exit to `RUNTIME_ERROR` per architecture §11; integration
tests then assert `OK` and fail. Verified the failure is not a code
defect:

    docker run --rm --privileged -v "$(pwd):/src" -w /src \
      goboxd-tools:dev sh -c "nsjail -Mo -R /bin:/bin -R /usr:/usr \
        -R /lib:/lib -- /bin/echo hello"
    [I] Executing '/bin/echo' for '[STANDALONE MODE]'
    hello
    [I] pid=8 ([STANDALONE MODE]) exited with status: 0

Same image, same nsjail binary, same arch — the only difference is
`--privileged`. The default Docker seccomp + AppArmor profile blocks
`clone(CLONE_NEW*)` for unprivileged containers.

**How we resolved it:**
Not resolved. The fix lives in `docker-compose.yml`: the `tools`
service needs `privileged: true` (or the equivalent `cap_add` +
`security_opt` bundle). The `goboxd` service in the same compose file
already has `privileged: true`, but `make integration` runs in
`tools`, not `goboxd`.

Editing `docker-compose.yml` is forbidden by Architecture Req 2.1 and
Implementation Req I-6.1 (Task 32). Code-side workarounds were
considered and rejected:
- Bypassing nsjail under EPERM would violate Architecture §10.2
  ("every job runs inside nsjail with the seven namespaces").
- Reclassifying EPERM as `INTERNAL_ERROR` rather than `RUNTIME_ERROR`
  would hide a real failure mode behind a softer status.

Task 30 was therefore reclassified as BLOCKED OUTSIDE CODEBASE.
Integration tests as artefacts are complete (Implementation Req I-3.5
satisfied — every test under `tests/integration/` exists and either
passes or `t.Skip()`s cleanly under a privileged Linux runner).

**What we learned:**
The acceptance criterion implicitly assumed the `tools` container
could create namespaces. Future iterations of the spec should either:
1. Run integration tests inside the privileged `goboxd` runtime
   service rather than the `tools` builder service, or
2. Grant the `tools` service the capabilities nsjail requires, or
3. Mark Task 30 as gated on Linux runners with privileged Docker.

The architecture spec's frozen-infrastructure constraint is correct
in intent (`Dockerfile`, `docker-compose.yml`, `Makefile` should not
drift during implementation) but did not anticipate that the
integration-test execution path requires privileges that the
infrastructure does not grant.

---

### 2026-06-01 · `docs/architecture.md` deliverable was missing despite Architecture Req 1.1

**What we were trying to do:**
Verify final acceptance against `.kiro/specs/goboxd-architecture/`.

**What went wrong:**
Architecture Req 1.1: *"THE Architecture_Document SHALL be produced as
a single Markdown file located at `docs/architecture.md`."*
`ls docs/` showed only `.gitkeep`, untouched since the upstream
scaffolding commit `ea5ab73`. Full git log scan confirmed the file
was never committed to any branch.

The architecture spec's own `design.md` line 9 framed the workflow
correctly: *"`docs/architecture.md` can be written by mechanical
expansion of the sections below."* Twelve top-level sections in
`design.md` literally carry the parenthetical "(plan for
`docs/architecture.md` §N)". A verbatim copy would have failed Req 1.8
(self-incoherent header text). The deliverable required a structural
transform that nobody had performed.

**How we resolved it:**
This documentation tree includes `docs/architecture.md`, produced by
mechanically transforming `.kiro/specs/goboxd-architecture/design.md`:
section reordering to match the §1–§19 list in design.md lines 49–62,
removal of "(plan for docs/architecture.md §N)" suffixes, promotion of
sub-sections that the requirements list as standalone (High-Level
Overview §3, Component → Folder Traceability §19), and removal of
spec-authoring meta sections (Overview, Data Models as standalone,
Correctness Properties, Error Handling — none of which appear in the
required architecture-document section list).

**What we learned:**
The architecture spec's deliverable was tracked as a documentation
outcome rather than as an implementation task in
`.kiro/specs/goboxd-implementation/tasks.md`, and was therefore missed
by the implementation-driven work plan. Future projects should either:
1. Include the documentation deliverable as an explicit task in the
   implementation tasks.md, or
2. Add a CI gate that fails if any spec-mandated artefact is missing.

---

### 2026-06-01 · Phase 3 nil-safety test pattern not extended to two new counters

**What we were trying to do:**
Phase 3 added two new metric counters: `goboxd_unsafe_filename_total`
and `goboxd_unknown_placeholder_total`
(`internal/metrics/metrics.go` lines 353–365).

**What went wrong:**
The established testing pattern in `internal/metrics/metrics_test.go`
pairs every counter with a `*NilSafe` test
(`TestSecurityRejectionsCounterNilSafe`, `TestRunMetricsNilSafe`,
`TestStartupPrereqFailuresCounterNilSafe`,
`TestOrphanWorkspaceReapedCounterNilSafe`). The two Phase 3 counters
have functional tests (`TestUnsafeFilenameCounter`,
`TestUnknownPlaceholderCounter`) but lack dedicated nil-safety tests.

The implementations themselves are nil-guarded — `IncUnsafeFilename`
and `IncUnknownPlaceholder` both begin with
`if c == nil || c.<vec> == nil { return }`. The behaviour is correct;
only the explicit test coverage of that behaviour is missing.

**How we resolved it:**
Not resolved by a code change. The gap was identified during the
Phase 3 audit. No commit adds the missing tests.

**What we learned:**
The audit cycle that surfaced this gap was conducted in development
conversations and never committed to the repository. A standing
checklist or a generated coverage report would have caught the gap as
part of the pull request, rather than during a final acceptance
review.

---

### 2026-06-01 · `tasks.md` checkboxes not maintained as work progressed

**What we were trying to do:**
Use `.kiro/specs/goboxd-implementation/tasks.md` as a canonical
progress tracker.

**What went wrong:**
Both `.kiro/specs/goboxd-architecture/tasks.md` and
`.kiro/specs/goboxd-implementation/tasks.md` were committed once in
`eca4b19` and never modified afterwards. Every checkbox still reads
`- [ ]` despite tasks 1–31 having been delivered.

**How we resolved it:**
Not resolved. The progress tracking now lives implicitly in commit
history (`eca4b19` Phase 1, `44ac6a5` + `7bd3ac8` Phase 2, `4451ac7`
Phase 3, `db4b14a` Task 30 partial, `5141b1c` Task 31).

**What we learned:**
Git commits are reliable progress evidence; tasks.md checkboxes are
not. If tasks.md is intended as the single source of truth, the
workflow needs a step that updates it as tasks complete. Otherwise,
treat commit history as authoritative and tasks.md as the original
plan rather than a progress tracker.
