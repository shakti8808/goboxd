// Package runner builds and executes the NsJail invocation that runs
// untrusted code inside the sandbox.
//
// Phase 2 lands the package incrementally:
//
//   - Wave A (Tasks 14, 17): pure functions — argv builder, status
//     classifier — plus their shared types. No file I/O, no os/exec,
//     no goroutines.
//   - Wave B (Tasks 15, 16): workspace allocator, capture discarders.
//   - Wave C (Tasks 18, 19): orchestrator wiring all of the above plus
//     `os/exec`, plus the orphan reaper goroutine.
//
// The types in this file are the minimum shared surface every wave
// needs. Each subsequent wave extends them additively rather than
// reworking the contract.
package runner

// StepKind distinguishes the compile and run stages of a job.
//
// The status classifier dispatches on StepKind because compile-step
// failures map to COMPILATION_ERROR while run-step failures map to
// RUNTIME_ERROR (architecture §11 mapping table).
type StepKind string

// StepKind values.
const (
	StepCompile StepKind = "compile"
	StepRun     StepKind = "run"
)

// Status is the closed enum of Execution_Result.status values from
// architecture §11. Wave A only consumes the values; Wave C emits them
// onto the wire.
type Status string

// Status values from architecture §11 mapping table.
const (
	StatusOK                  Status = "OK"
	StatusCompilationError    Status = "COMPILATION_ERROR"
	StatusRuntimeError        Status = "RUNTIME_ERROR"
	StatusTimeLimitExceeded   Status = "TIME_LIMIT_EXCEEDED"
	StatusMemoryLimitExceeded Status = "MEMORY_LIMIT_EXCEEDED"
	StatusInternalError       Status = "INTERNAL_ERROR"
)

// Limits carries the effective Resource_Limits the argv builder writes
// onto the nsjail flag set. All values are integers in the documented
// architecture §10.3 ranges; the argv builder rejects out-of-range
// inputs.
type Limits struct {
	WallTimeS    int
	CPUTimeS     int
	MemoryMB     int
	ProcessCount int
	OutputSizeMB int
}

// BindMount describes one filesystem bind mount passed to nsjail.
//
// The argv builder enforces (a) exactly one rw mount, namely the
// per-request JOB_DIR mounted at /sandbox; (b) every other mount marked
// read-only.
type BindMount struct {
	HostPath  string
	GuestPath string
	ReadWrite bool
}

// EnvVar is one entry in the closed env allowlist passed to nsjail.
//
// The argv builder forbids Code_Submission fields from appearing in env
// (architecture REQ A-22.2(d)). The allowlist is supplied by the caller;
// the runner does not inherit the GoboxD process environment.
type EnvVar struct {
	Name  string
	Value string
}

// Job is the in-memory job context shared across argv build, sandbox
// execution, and result aggregation. Wave A only fills the fields the
// argv builder and classifier need; later waves extend it additively.
type Job struct {
	// LanguageID is the registry language identifier (e.g. "py3").
	LanguageID string

	// SourceFilename and BinaryFilename are the validated basenames from
	// the registry; the argv builder substitutes them into placeholders.
	SourceFilename string
	BinaryFilename string

	// JobDir is the per-request Sandbox_Job_Directory absolute path on
	// the host. The argv builder mounts it rw at /sandbox.
	JobDir string

	// StdinFile is the basename (under JobDir) the runner writes the
	// stdin payload to. Empty means "no stdin file" — the argv builder
	// then forbids the {{STDIN_FILE}} placeholder appearing in args.
	StdinFile string

	// EffectiveLimits is the resolved Resource_Limits the runner enforces.
	EffectiveLimits Limits

	// CompileLimits is the resolved Resource_Limits the compile step enforces.
	CompileLimits Limits

	// Source is the raw UTF-8 source bytes the runner writes to
	// SourceFilename inside the workspace. Wave C added this field; the
	// argv builder does not read it.
	Source string

	// CompileTemplate carries the registry-bound compile-step
	// command/args. An empty Command means "skip the compile step"
	// (architecture spec §"Languages with no compile block").
	CompileTemplate CommandTemplate

	// RunTemplate carries the registry-bound run-step command/args.
	// Required.
	RunTemplate CommandTemplate
}
