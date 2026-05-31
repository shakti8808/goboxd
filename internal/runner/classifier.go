package runner

// LimitIndicator labels which sandbox-enforced limit nsjail killed the
// child for, when one fired.
//
// nsjail emits log lines like `time >= ...` or `process_groups: ...
// out of memory` that the orchestrator parses into one of these values.
// The classifier itself never parses logs; it consumes the parsed
// indicator so the function stays pure.
type LimitIndicator string

// LimitIndicator values.
const (
	LimitIndicatorNone   LimitIndicator = ""
	LimitIndicatorWall   LimitIndicator = "wall_time"
	LimitIndicatorCPU    LimitIndicator = "cpu_time"
	LimitIndicatorMemory LimitIndicator = "memory"
)

// ClassifierInput is the closed set of fields the status classifier
// inspects (architecture §11 deterministic mapping table).
//
// The struct exists so adding a new dimension later is a non-breaking
// change: callers stop fighting positional argument lists and the
// classifier itself remains a pure function of its input.
type ClassifierInput struct {
	// Step is StepCompile or StepRun.
	Step StepKind

	// ExitCode is the child process exit code. Ignored when
	// TerminatedBySignal is true.
	ExitCode int

	// TerminatedBySignal is true when the child was killed by a signal
	// rather than exiting cleanly. The signal name is not part of the
	// classifier — the LimitIndicator field carries the limit-attribution
	// signal when nsjail provides one, and otherwise the classifier maps
	// signal-termination of a run step to RUNTIME_ERROR.
	TerminatedBySignal bool

	// LimitIndicator is the parsed nsjail limit indicator. Set to
	// LimitIndicatorNone when no limit fired.
	LimitIndicator LimitIndicator

	// InvocationFailure is true when nsjail itself could not be located,
	// failed to launch within NSJAIL_LAUNCH_TIMEOUT_S, or exited before
	// producing a parseable outcome. This dominates every other input.
	InvocationFailure bool
}

// Classify maps a ClassifierInput to a Status per architecture §11.
//
// The mapping is deterministic and total. The function has no side
// effects; identical inputs always produce identical outputs (Property
// 19).
//
// Mapping (precedence top-to-bottom — the first matching row wins):
//
//	InvocationFailure                                            → INTERNAL_ERROR
//	Step=run    AND LimitIndicator ∈ {wall, cpu}                 → TIME_LIMIT_EXCEEDED
//	Step=run    AND LimitIndicator = memory                      → MEMORY_LIMIT_EXCEEDED
//	Step=compile AND LimitIndicator ∈ {wall, cpu, memory}        → COMPILATION_ERROR
//	Step=compile AND ExitCode ≠ 0                                → COMPILATION_ERROR
//	Step=compile AND TerminatedBySignal                          → COMPILATION_ERROR
//	Step=run    AND ExitCode = 0 AND ¬TerminatedBySignal         → OK
//	Step=run    AND (ExitCode ≠ 0 OR TerminatedBySignal)         → RUNTIME_ERROR
//	Step=compile AND ExitCode = 0                                → OK (caller proceeds to run)
//	(any other tuple, including unknown StepKind)                → INTERNAL_ERROR
//
// Compile-step time/memory limits map to COMPILATION_ERROR rather than
// TIME_LIMIT_EXCEEDED because the architecture §11 deterministic mapping
// classifies compile-step limits as compile failures: from the caller's
// perspective the compile didn't finish, so the next step (run) was
// never reached.
func Classify(in ClassifierInput) Status {
	if in.InvocationFailure {
		return StatusInternalError
	}

	switch in.Step {
	case StepRun:
		switch in.LimitIndicator {
		case LimitIndicatorWall, LimitIndicatorCPU:
			return StatusTimeLimitExceeded
		case LimitIndicatorMemory:
			return StatusMemoryLimitExceeded
		}
		if in.ExitCode == 0 && !in.TerminatedBySignal {
			return StatusOK
		}
		return StatusRuntimeError

	case StepCompile:
		// Any limit hit during compile is reported to the caller as a
		// compilation failure: the compile didn't reach a clean exit.
		if in.LimitIndicator != LimitIndicatorNone {
			return StatusCompilationError
		}
		if in.TerminatedBySignal {
			return StatusCompilationError
		}
		if in.ExitCode != 0 {
			return StatusCompilationError
		}
		return StatusOK

	default:
		// Unknown StepKind — defensive. The orchestrator never produces
		// this case; the branch keeps Classify total.
		return StatusInternalError
	}
}
