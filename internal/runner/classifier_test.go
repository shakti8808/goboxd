package runner_test

import (
	"testing"

	"github.com/thesouldev/goboxd/internal/runner"
)

// TestClassifyTable walks every row of the architecture §11 mapping
// table.
func TestClassifyTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   runner.ClassifierInput
		want runner.Status
	}{
		// Invocation failure dominates.
		{"invocation failure compile", runner.ClassifierInput{Step: runner.StepCompile, InvocationFailure: true}, runner.StatusInternalError},
		{"invocation failure run", runner.ClassifierInput{Step: runner.StepRun, InvocationFailure: true}, runner.StatusInternalError},

		// Run step happy path.
		{"run ok", runner.ClassifierInput{Step: runner.StepRun, ExitCode: 0}, runner.StatusOK},

		// Run step limit-attributable failures.
		{"run wall limit", runner.ClassifierInput{Step: runner.StepRun, LimitIndicator: runner.LimitIndicatorWall, TerminatedBySignal: true}, runner.StatusTimeLimitExceeded},
		{"run cpu limit", runner.ClassifierInput{Step: runner.StepRun, LimitIndicator: runner.LimitIndicatorCPU, TerminatedBySignal: true}, runner.StatusTimeLimitExceeded},
		{"run memory limit", runner.ClassifierInput{Step: runner.StepRun, LimitIndicator: runner.LimitIndicatorMemory, TerminatedBySignal: true}, runner.StatusMemoryLimitExceeded},

		// Run step generic failures.
		{"run nonzero exit", runner.ClassifierInput{Step: runner.StepRun, ExitCode: 1}, runner.StatusRuntimeError},
		{"run signal no indicator", runner.ClassifierInput{Step: runner.StepRun, TerminatedBySignal: true}, runner.StatusRuntimeError},

		// Compile step happy path.
		{"compile ok", runner.ClassifierInput{Step: runner.StepCompile, ExitCode: 0}, runner.StatusOK},

		// Compile step failures.
		{"compile nonzero exit", runner.ClassifierInput{Step: runner.StepCompile, ExitCode: 1}, runner.StatusCompilationError},
		{"compile signal", runner.ClassifierInput{Step: runner.StepCompile, TerminatedBySignal: true}, runner.StatusCompilationError},
		{"compile time limit", runner.ClassifierInput{Step: runner.StepCompile, LimitIndicator: runner.LimitIndicatorWall, TerminatedBySignal: true}, runner.StatusCompilationError},
		{"compile memory limit", runner.ClassifierInput{Step: runner.StepCompile, LimitIndicator: runner.LimitIndicatorMemory, TerminatedBySignal: true}, runner.StatusCompilationError},

		// Unknown step kind.
		{"unknown step", runner.ClassifierInput{Step: runner.StepKind("bogus")}, runner.StatusInternalError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runner.Classify(tc.in)
			if got != tc.want {
				t.Errorf("Classify(%+v) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestClassifyDeterminism asserts the classifier is idempotent — Property
// 19's "identical inputs always produce identical outputs" claim — and
// catches any future drift toward stateful behaviour (a global counter,
// a time-of-day branch, etc.).
func TestClassifyDeterminism(t *testing.T) {
	t.Parallel()
	seed := runner.ClassifierInput{
		Step: runner.StepRun, ExitCode: 137, TerminatedBySignal: true,
		LimitIndicator: runner.LimitIndicatorMemory,
	}
	first := runner.Classify(seed)
	for i := 0; i < 1024; i++ {
		if got := runner.Classify(seed); got != first {
			t.Fatalf("Classify drift: iteration %d returned %s, want %s", i, got, first)
		}
	}
}
