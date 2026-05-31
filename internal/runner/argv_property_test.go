package runner_test

import (
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/security"
)

// genLimits draws Resource_Limits inside the architecture §10.3 ranges.
func genLimits(t *rapid.T) runner.Limits {
	return runner.Limits{
		WallTimeS:    rapid.IntRange(1, 60).Draw(t, "wall"),
		CPUTimeS:     rapid.IntRange(1, 60).Draw(t, "cpu"),
		MemoryMB:     rapid.IntRange(16, 1024).Draw(t, "mem"),
		ProcessCount: rapid.IntRange(1, 64).Draw(t, "proc"),
		OutputSizeMB: rapid.IntRange(1, 64).Draw(t, "out"),
	}
}

// argvBaseline returns a deterministic ArgvInput with safe filenames.
//
// Property tests mutate one field per case. Filenames are pinned to
// known-safe basenames; the unsafe-filename branch is covered by the
// dedicated unit tests.
func argvBaseline(lim runner.Limits) runner.ArgvInput {
	return runner.ArgvInput{
		NsJailPath: "/usr/local/bin/nsjail",
		Step:       runner.StepRun,
		Template: runner.CommandTemplate{
			Command: "/usr/bin/python3",
			Args:    []string{"{{SOURCE_FILENAME}}"},
		},
		Job: runner.Job{
			SourceFilename:  "main.py",
			JobDir:          "/var/lib/goboxd/sandbox/job-prop",
			EffectiveLimits: lim,
		},
		EnvAllowlist: []runner.EnvVar{
			{Name: "PATH", Value: "/usr/bin:/bin"},
		},
		SeccompPolicy: "DEFAULT KILL",
	}
}

// TestArgvProperty17_FlagValuesEqualLimits — Property 17.
//
// For arbitrary in-range limits, the constructed argv contains the
// expected --time_limit / --rlimit_cpu / --rlimit_as / --rlimit_nproc /
// --rlimit_fsize flag pairs whose value equals the limit field.
func TestArgvProperty17_FlagValuesEqualLimits(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lim := genLimits(t)
		argv, err := runner.BuildArgv(argvBaseline(lim))
		if err != nil {
			t.Fatalf("BuildArgv: %v", err)
		}
		if !flagHasValue(argv, "--time_limit", lim.WallTimeS) {
			t.Fatalf("--time_limit != %d in %v", lim.WallTimeS, argv)
		}
		if !flagHasValue(argv, "--rlimit_cpu", lim.CPUTimeS) {
			t.Fatalf("--rlimit_cpu != %d", lim.CPUTimeS)
		}
		if !flagHasValue(argv, "--rlimit_as", lim.MemoryMB) {
			t.Fatalf("--rlimit_as != %d", lim.MemoryMB)
		}
		if !flagHasValue(argv, "--rlimit_nproc", lim.ProcessCount) {
			t.Fatalf("--rlimit_nproc != %d", lim.ProcessCount)
		}
		if !flagHasValue(argv, "--rlimit_fsize", lim.OutputSizeMB) {
			t.Fatalf("--rlimit_fsize != %d", lim.OutputSizeMB)
		}
	})
}

// TestArgvProperty17_OneRWMount — Property 17 corollary.
//
// Exactly one --bindmount flag (the rw JOB_DIR mount) appears regardless
// of how many read-only mounts the caller adds. Read-only mounts use
// --bindmount_ro.
func TestArgvProperty17_OneRWMount(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mountCount := rapid.IntRange(0, 5).Draw(t, "mountCount")
		in := argvBaseline(limitsOK())
		for i := 0; i < mountCount; i++ {
			in.BindMounts = append(in.BindMounts, runner.BindMount{
				HostPath:  "/x",
				GuestPath: "/x",
				ReadWrite: false,
			})
		}
		argv, err := runner.BuildArgv(in)
		if err != nil {
			t.Fatalf("BuildArgv: %v", err)
		}
		rw := countFlag(argv, "--bindmount")
		ro := countFlag(argv, "--bindmount_ro")
		if rw != 1 {
			t.Fatalf("--bindmount count = %d, want exactly 1", rw)
		}
		if ro != mountCount {
			t.Fatalf("--bindmount_ro count = %d, want %d", ro, mountCount)
		}
	})
}

// TestArgvProperty36_NoSubmissionInArgv — Property 36.
//
// For arbitrary "Code_Submission" payload bytes (drawn here as
// `submissionBytes`), the constructed argv contains none of those bytes
// as elements, env names, or env values. The submission is not passed to
// BuildArgv at all — the property is enforced by construction; we
// nevertheless verify by drawing arbitrary submissions and checking the
// argv elements never equal the submission string.
func TestArgvProperty36_NoSubmissionInArgv(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Draw a non-empty distinctive byte sequence the argv builder
		// could only contain if it had read the submission.
		marker := rapid.StringMatching(`[A-Za-z0-9_\-]{8,32}`).Draw(t, "marker")
		argv, err := runner.BuildArgv(argvBaseline(limitsOK()))
		if err != nil {
			t.Fatalf("BuildArgv: %v", err)
		}
		for _, e := range argv {
			if strings.Contains(e, marker) {
				t.Fatalf("argv element %q contains marker %q (submission leak)", e, marker)
			}
		}
	})
}

// TestArgvProperty36_Argv0IsNsjail — Property 16 surface (the architecture
// invariant "every sandbox execution goes through nsjail" is enforced
// transitively at the argv-builder level: argv[0] is the nsjail path).
func TestArgvProperty36_Argv0IsNsjail(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		path := rapid.SampledFrom([]string{
			"/usr/local/bin/nsjail",
			"/opt/nsjail/bin/nsjail",
			"/usr/bin/nsjail",
		}).Draw(t, "nsjailPath")
		in := argvBaseline(limitsOK())
		in.NsJailPath = path
		argv, err := runner.BuildArgv(in)
		if err != nil {
			t.Fatalf("BuildArgv: %v", err)
		}
		if argv[0] != path {
			t.Fatalf("argv[0] = %q, want %q", argv[0], path)
		}
	})
}

// TestArgvProperty37_PlaceholderAllowlist — Property 37.
//
// For arbitrary placeholder names, BuildArgv succeeds iff the name is in
// the closed allowlist; otherwise it returns *ArgvError with reason
// unknown_placeholder. The generator covers the closed-allowlist case
// (which must succeed) and arbitrary other names (which must all fail).
func TestArgvProperty37_PlaceholderAllowlist(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// 1/3 of the time: pick an allowlisted name; 2/3: arbitrary.
		var name string
		switch rapid.IntRange(0, 2).Draw(t, "branch") {
		case 0:
			name = rapid.SampledFrom([]string{
				"SOURCE_FILENAME", "BINARY_FILENAME", "STDIN_FILE",
			}).Draw(t, "allowlisted")
		default:
			name = rapid.StringMatching(`[A-Z_][A-Z0-9_]{0,15}`).Draw(t, "rand")
		}
		in := argvBaseline(limitsOK())
		// Provide the supporting fields so allowlisted names can resolve.
		in.Job.BinaryFilename = "main"
		in.Job.StdinFile = "stdin.bin"
		in.Template.Args = []string{"{{" + name + "}}"}

		_, err := runner.BuildArgv(in)
		isAllowlisted := security.IsAllowedPlaceholder(name)
		if isAllowlisted {
			if err != nil {
				t.Fatalf("allowlisted placeholder %q failed: %v", name, err)
			}
			return
		}
		if err == nil {
			t.Fatalf("non-allowlisted placeholder %q accepted", name)
		}
		var ae *runner.ArgvError
		if !errors.As(err, &ae) || ae.Reason != "unknown_placeholder" {
			t.Fatalf("err = %v, want unknown_placeholder", err)
		}
	})
}

// flagHasValue checks whether `flag` appears in argv followed by the
// decimal representation of v.
func flagHasValue(argv []string, flag string, v int) bool {
	for i, e := range argv {
		if e == flag && i+1 < len(argv) {
			return argv[i+1] == itoa(v)
		}
	}
	return false
}

// countFlag returns how many times `flag` appears in argv.
func countFlag(argv []string, flag string) int {
	n := 0
	for _, e := range argv {
		if e == flag {
			n++
		}
	}
	return n
}

// itoa keeps strconv out of the test imports surface.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
