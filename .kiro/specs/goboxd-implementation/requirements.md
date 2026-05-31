# Requirements Document

## Introduction

This spec produces the **working Go service** that implements the architecture defined in `.kiro/specs/goboxd-architecture/`. The architecture spec produced a documentation-only deliverable (`docs/architecture.md`); this implementation spec produces actual Go source code under `cmd/goboxd/`, `internal/`, and tests under `internal/<pkg>/` (unit + property) and `tests/integration/` (build-tag gated).

This document is intentionally short. It does not duplicate any requirement from the architecture spec — it inherits all of them — and adds only the implementation-specific requirements that the architecture spec deliberately did not encode. Cross-spec references in `design.md` and `tasks.md` use the prefix `I-` for these requirements (e.g. `I-1.1`, `I-3.5`) to disambiguate from architecture-spec requirements (prefixed `A-`).

## Inheritance

THE goboxd-implementation feature SHALL satisfy every Requirement defined in `.kiro/specs/goboxd-architecture/requirements.md` (Requirements 1 through 27) at runtime. Where the architecture requirement constrains the **document** (e.g. Req 1.1, 2.3 which forbid `.go` files in the architecture deliverable), the constraint applies to the architecture document only; this implementation spec is permitted and expected to produce Go source code.

## Glossary

All glossary entries from `.kiro/specs/goboxd-architecture/requirements.md` apply unchanged.

## Requirements

### Requirement 1: Working Go binary

**User Story:** As a maintainer, I want a working `goboxd` binary that satisfies every behavioural requirement of the architecture spec, so that GoboxD can run in production.

#### Acceptance Criteria

1. THE implementation SHALL produce a single static `goboxd` binary built for `linux/amd64` with `CGO_ENABLED=0`.
2. THE implementation SHALL pass `make build`, `make test`, `make lint`, and `make integration` against the existing `Makefile` without modifying `Dockerfile`, `docker-compose.yml`, or `Makefile`.
3. THE implementation SHALL realise the runtime behaviour required by Architecture Requirements 3 through 27.
4. WHEN the binary is started against a valid configuration and a valid `language_registry.yaml`, THE binary SHALL serve `POST /run`, `GET /healthz`, `GET /readyz`, `GET /info`, and `GET /metrics` per the Architecture HTTP API surface.

### Requirement 2: Module path and dependency boundaries

**User Story:** As a maintainer, I want a tight, declared dependency surface, so that supply-chain risk is minimal and the build is reproducible.

#### Acceptance Criteria

1. THE Go module path SHALL remain `github.com/thesouldev/goboxd` and the Go toolchain version SHALL remain `1.23` as declared in the existing `go.mod`.
2. THE implementation SHALL introduce only the third-party modules listed in `.kiro/specs/goboxd-implementation/design.md` "Third-party dependencies" (`gopkg.in/yaml.v3`, `github.com/google/uuid`, `github.com/prometheus/client_golang`, `pgregory.net/rapid`); all other functionality SHALL be implemented using the Go standard library.
3. THE `internal/` package import graph SHALL match the architecture spec §5 acyclic graph exactly; no edge SHALL exist that is not declared there.

### Requirement 3: Test layering

**User Story:** As a contributor, I want unit, property, and integration tests cleanly separated, so that fast feedback is preserved and `nsjail`-dependent tests are gated.

#### Acceptance Criteria

1. THE implementation SHALL place unit tests in `internal/<pkg>/*_test.go` with no build tag.
2. THE implementation SHALL place property tests in `internal/<pkg>/*_property_test.go` with no build tag, using `pgregory.net/rapid`.
3. THE implementation SHALL place integration tests in `tests/integration/*_test.go` carrying the `//go:build integration` build tag.
4. WHEN `make test` runs, only unit and property tests SHALL execute; integration tests SHALL NOT execute under that target.
5. WHEN `make integration` runs and the `nsjail` binary is unavailable at `NSJAIL_PATH`, integration tests SHALL `t.Skip()` rather than fail.

### Requirement 4: Property-test coverage of correctness properties

**User Story:** As a security engineer, I want every correctness property covered by an automated test of the appropriate kind, so that regressions are detected mechanically.

#### Acceptance Criteria

1. THE implementation SHALL provide automated test coverage for every Architecture Correctness Property numbered 3 through 42, using the test mechanism (PBT / Unit / Integration / Static) listed in `.kiro/specs/goboxd-implementation/design.md` "Correctness Property Classification".
2. WHERE the classification table assigns a property to PBT, the test SHALL be implemented with `pgregory.net/rapid` and SHALL run for at least 100 iterations per invocation.
3. WHERE the classification table assigns a property to "Static", a CI gate (file-layout check, build-tag check, or `go list -deps` parser) SHALL enforce the property without requiring Go test code.

### Requirement 5: Package implementation order

**User Story:** As a project owner, I want the implementation delivered in three phases that match the architecture spec phases, so that progress is demonstrable and merge risk is bounded.

#### Acceptance Criteria

1. THE implementation SHALL deliver Phase 1 (HTTP skeleton, no sandbox execution) before Phase 2.
2. THE implementation SHALL deliver Phase 2 (`POST /run` + NsJail + `py3`) before Phase 3.
3. THE implementation SHALL deliver Phase 3 (full Metrics_Collector + `cpp`) last.
4. The phase scope, demonstrable outcomes, and completion gates are inherited from `.kiro/specs/goboxd-architecture/design.md` "Implementation Phases" and SHALL NOT be redefined.

### Requirement 6: Infrastructure file invariance

**User Story:** As a project owner, I want the existing infrastructure files preserved byte-for-byte, so that the build environment remains stable across implementation work.

#### Acceptance Criteria

1. THE implementation SHALL NOT modify, rename, or delete `Dockerfile`, `docker-compose.yml`, or `Makefile` at any point during Phase 1, Phase 2, or Phase 3.
2. IF a feature appears to require a change to one of those files, THEN the change SHALL be raised as a separate spec and rejected from this implementation spec.
