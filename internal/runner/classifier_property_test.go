package runner_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/thesouldev/goboxd/internal/runner"
)

// validStatuses is the closed set the architecture §11 mapping permits.
// The PBT below asserts every Classify return value is a member.
var validStatuses = map[runner.Status]struct{}{
	runner.StatusOK:                  {},
	runner.StatusCompilationError:    {},
	runner.StatusRuntimeError:        {},
	runner.StatusTimeLimitExceeded:   {},
	runner.StatusMemoryLimitExceeded: {},
	runner.StatusInternalError:       {},
}

// genClassifierInput emits an arbitrary ClassifierInput drawn from the
// fields' documented domains. StepKind is biased toward compile/run but
// occasionally produces a synthetic value so the unknown-step branch
// is exercised.
func genClassifierInput(t *rapid.T) runner.ClassifierInput {
	stepChoice := rapid.IntRange(0, 4).Draw(t, "step")
	var step runner.StepKind
	switch stepChoice {
	case 0, 1:
		step = runner.StepCompile
	case 2, 3:
		step = runner.StepRun
	default:
		step = runner.StepKind("synthetic")
	}

	limit := []runner.LimitIndicator{
		runner.LimitIndicatorNone,
		runner.LimitIndicatorWall,
		runner.LimitIndicatorCPU,
		runner.LimitIndicatorMemory,
	}[rapid.IntRange(0, 3).Draw(t, "limit")]

	return runner.ClassifierInput{
		Step:               step,
		ExitCode:           rapid.IntRange(-1, 255).Draw(t, "exitCode"),
		TerminatedBySignal: rapid.Bool().Draw(t, "signal"),
		LimitIndicator:     limit,
		InvocationFailure:  rapid.Bool().Draw(t, "invocationFailure"),
	}
}

// TestClassifyPropertyDeterministicTotal — Property 19.
//
// Universal claim: for every legal ClassifierInput, Classify returns
// (a) a value in the closed Status set and (b) an identical value when
// invoked again with the same input. Rapid runs ≥100 iterations per
// invocation per Req I-4.2; it default-runs 100 already.
func TestClassifyPropertyDeterministicTotal(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genClassifierInput(t)
		first := runner.Classify(in)
		if _, ok := validStatuses[first]; !ok {
			t.Fatalf("Classify(%+v) = %s, not in closed Status set", in, first)
		}
		second := runner.Classify(in)
		if first != second {
			t.Fatalf("Classify non-deterministic: %s vs %s for %+v", first, second, in)
		}
	})
}

// TestClassifyPropertyInvocationFailureDominates — Property 19 corollary.
//
// When InvocationFailure is true, the result must always be
// INTERNAL_ERROR regardless of every other field.
func TestClassifyPropertyInvocationFailureDominates(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genClassifierInput(t)
		in.InvocationFailure = true
		got := runner.Classify(in)
		if got != runner.StatusInternalError {
			t.Fatalf("Classify(%+v) = %s, want INTERNAL_ERROR (invocation failure dominates)", in, got)
		}
	})
}
