package runner_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/security"
)

// limitsOK returns a Resource_Limits set inside every documented range.
func limitsOK() runner.Limits {
	return runner.Limits{
		WallTimeS:    10,
		CPUTimeS:     10,
		MemoryMB:     256,
		ProcessCount: 16,
		OutputSizeMB: 4,
	}
}

// argvInputPy3 returns the architecture §12 worked example for py3.
func argvInputPy3() runner.ArgvInput {
	return runner.ArgvInput{
		NsJailPath: "/usr/local/bin/nsjail",
		Step:       runner.StepRun,
		Template: runner.CommandTemplate{
			Command: "/usr/bin/python3",
			Args:    []string{"{{SOURCE_FILENAME}}"},
		},
		Job: runner.Job{
			LanguageID:      "py3",
			SourceFilename:  "main.py",
			JobDir:          "/var/lib/goboxd/sandbox/job-abcd",
			EffectiveLimits: limitsOK(),
		},
		EnvAllowlist: []runner.EnvVar{
			{Name: "PATH", Value: "/usr/bin:/bin"},
			{Name: "LANG", Value: "C.UTF-8"},
		},
		BindMounts: []runner.BindMount{
			{HostPath: "/usr", GuestPath: "/usr", ReadWrite: false},
			{HostPath: "/lib", GuestPath: "/lib", ReadWrite: false},
		},
		SeccompPolicy: "DEFAULT KILL",
	}
}

// argvInputCPP returns the architecture §12 worked example for cpp's
// compile step.
func argvInputCPP() runner.ArgvInput {
	in := argvInputPy3()
	in.Step = runner.StepCompile
	in.Template = runner.CommandTemplate{
		Command: "/usr/bin/g++",
		Args:    []string{"-O2", "-std=c++17", "-o", "{{BINARY_FILENAME}}", "{{SOURCE_FILENAME}}"},
	}
	in.Job.LanguageID = "cpp"
	in.Job.SourceFilename = "main.cpp"
	in.Job.BinaryFilename = "main"
	return in
}

// TestBuildArgvPy3 asserts the exact argv shape for the py3 worked example.
//
// The assertion uses contains-style checks for the substituted bits so
// the test does not need to know the order of every nsjail flag — but
// it asserts argv[0] is nsjail, the rw bind mount is exactly the job
// dir, the source filename is substituted, and no Code_Submission field
// could leak into the argv (the test never references one).
func TestBuildArgvPy3(t *testing.T) {
	t.Parallel()
	argv, err := runner.BuildArgv(argvInputPy3())
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	if argv[0] != "/usr/local/bin/nsjail" {
		t.Errorf("argv[0] = %q, want /usr/local/bin/nsjail", argv[0])
	}
	if !contains(argv, "--time_limit") || !nextEquals(argv, "--time_limit", "10") {
		t.Errorf("missing --time_limit 10 in argv: %v", argv)
	}
	if !nextEquals(argv, "--rlimit_as", "256") {
		t.Errorf("missing --rlimit_as 256 in argv: %v", argv)
	}
	if !nextEquals(argv, "--bindmount", "/var/lib/goboxd/sandbox/job-abcd:/sandbox") {
		t.Errorf("missing rw bind mount: %v", argv)
	}
	// Read-only mounts present.
	if !contains(argv, "--bindmount_ro") {
		t.Error("argv missing read-only mounts")
	}
	// Source filename substituted into child argv.
	last := argv[len(argv)-1]
	if last != "main.py" {
		t.Errorf("last argv = %q, want main.py (substituted)", last)
	}
	// `--` separator between nsjail flags and child argv.
	idx := indexOf(argv, "--")
	if idx == -1 {
		t.Fatal("argv missing -- separator")
	}
	if argv[idx+1] != "/usr/bin/python3" {
		t.Errorf("child command = %q, want /usr/bin/python3", argv[idx+1])
	}
}

// TestBuildArgvCPP asserts placeholders survive the multi-arg cpp template.
func TestBuildArgvCPP(t *testing.T) {
	t.Parallel()
	argv, err := runner.BuildArgv(argvInputCPP())
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	idx := indexOf(argv, "--")
	if idx == -1 {
		t.Fatal("argv missing -- separator")
	}
	tail := argv[idx+1:]
	want := []string{"/usr/bin/g++", "-O2", "-std=c++17", "-o", "main", "main.cpp"}
	if len(tail) != len(want) {
		t.Fatalf("child argv = %v, want %v", tail, want)
	}
	for i := range want {
		if tail[i] != want[i] {
			t.Errorf("tail[%d] = %q, want %q", i, tail[i], want[i])
		}
	}
}

// TestBuildArgvUnknownPlaceholder confirms ArgvError + ErrInvalidArgvInput
// surface together for unknown placeholder tokens.
func TestBuildArgvUnknownPlaceholder(t *testing.T) {
	t.Parallel()
	in := argvInputPy3()
	in.Template.Args = []string{"{{HOSTILE}}"}
	_, err := runner.BuildArgv(in)
	if err == nil {
		t.Fatal("expected error for unknown placeholder")
	}
	if !errors.Is(err, runner.ErrInvalidArgvInput) {
		t.Errorf("errors.Is(err, ErrInvalidArgvInput) = false; want true")
	}
	var ae *runner.ArgvError
	if !errors.As(err, &ae) {
		t.Fatalf("error is not *ArgvError: %v", err)
	}
	if ae.Reason != "unknown_placeholder" {
		t.Errorf("Reason = %q, want unknown_placeholder", ae.Reason)
	}
	if !strings.Contains(ae.Detail, "HOSTILE") {
		t.Errorf("Detail %q does not name the offending placeholder", ae.Detail)
	}
}

// TestBuildArgvUnterminatedPlaceholder catches the {{… without closing }}
// case.
func TestBuildArgvUnterminatedPlaceholder(t *testing.T) {
	t.Parallel()
	in := argvInputPy3()
	in.Template.Args = []string{"{{SOURCE_FILENAME"}
	_, err := runner.BuildArgv(in)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *runner.ArgvError
	if !errors.As(err, &ae) || ae.Reason != "unterminated_placeholder" {
		t.Errorf("err = %v, want unterminated_placeholder", err)
	}
}

// TestBuildArgvStdinFilePlaceholderRequiresJob confirms the runner refuses
// to expand {{STDIN_FILE}} when the job did not declare a stdin file —
// this prevents a registry from naming a stdin file the runner has not
// created.
func TestBuildArgvStdinFilePlaceholderRequiresJob(t *testing.T) {
	t.Parallel()
	in := argvInputPy3()
	in.Template.Args = []string{"{{STDIN_FILE}}"}
	_, err := runner.BuildArgv(in)
	if err == nil {
		t.Fatal("expected error when STDIN_FILE placeholder used without Job.StdinFile")
	}
	if !errors.Is(err, runner.ErrInvalidArgvInput) {
		t.Errorf("errors.Is(err, ErrInvalidArgvInput) = false")
	}

	// Now provide the file and assert it substitutes correctly.
	in.Job.StdinFile = "stdin.bin"
	argv, err := runner.BuildArgv(in)
	if err != nil {
		t.Fatalf("BuildArgv: %v", err)
	}
	if argv[len(argv)-1] != "stdin.bin" {
		t.Errorf("STDIN_FILE substitution failed: last argv = %q", argv[len(argv)-1])
	}
}

// TestBuildArgvLimitOutOfRange asserts every limit is bounds-checked.
func TestBuildArgvLimitOutOfRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		mut  func(*runner.Limits)
		want string
	}{
		{"wall too high", func(l *runner.Limits) { l.WallTimeS = 999 }, "wall_time_s"},
		{"cpu zero", func(l *runner.Limits) { l.CPUTimeS = 0 }, "cpu_time_s"},
		{"memory below floor", func(l *runner.Limits) { l.MemoryMB = 8 }, "memory_mb"},
		{"process count zero", func(l *runner.Limits) { l.ProcessCount = 0 }, "process_count"},
		{"output size above ceiling", func(l *runner.Limits) { l.OutputSizeMB = 999 }, "output_size_mb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := argvInputPy3()
			lim := limitsOK()
			tc.mut(&lim)
			in.Job.EffectiveLimits = lim
			_, err := runner.BuildArgv(in)
			if err == nil {
				t.Fatal("expected error")
			}
			var ae *runner.ArgvError
			if !errors.As(err, &ae) {
				t.Fatalf("error is not *ArgvError: %v", err)
			}
			if ae.Reason != "limit_out_of_range" {
				t.Errorf("Reason = %q, want limit_out_of_range", ae.Reason)
			}
			if !strings.Contains(ae.Detail, tc.want) {
				t.Errorf("Detail %q does not name the offending limit %q", ae.Detail, tc.want)
			}
		})
	}
}

// TestBuildArgvRejectsRWMount asserts the architecture §11 rule that the
// only rw bind mount is the per-request JOB_DIR.
func TestBuildArgvRejectsRWMount(t *testing.T) {
	t.Parallel()
	in := argvInputPy3()
	in.BindMounts = append(in.BindMounts, runner.BindMount{HostPath: "/etc", GuestPath: "/etc", ReadWrite: true})
	_, err := runner.BuildArgv(in)
	if err == nil {
		t.Fatal("expected error for extra rw mount")
	}
	var ae *runner.ArgvError
	if !errors.As(err, &ae) || ae.Reason != "extra_rw_mount" {
		t.Errorf("err = %v, want extra_rw_mount", err)
	}
}

// TestBuildArgvUnsafeFilename confirms ValidateBasename is invoked on
// every filename field — so a registry that somehow slips an unsafe
// basename past load-time can't reach the sandbox at request time.
// (Property 35 request-time half lives here.)
func TestBuildArgvUnsafeFilename(t *testing.T) {
	t.Parallel()
	in := argvInputPy3()
	in.Job.SourceFilename = "../etc/passwd"
	_, err := runner.BuildArgv(in)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *runner.ArgvError
	if !errors.As(err, &ae) || ae.Reason != "unsafe_filename" {
		t.Errorf("err = %v, want unsafe_filename", err)
	}
	if !errors.Is(err, security.ErrUnsafeFilename) {
		t.Errorf("errors.Is(err, security.ErrUnsafeFilename) = false")
	}
}

// TestBuildArgvMissingNsjailPath / TestBuildArgvMissingJobDir cover the
// remaining structural rejection paths.
func TestBuildArgvMissingNsjailPath(t *testing.T) {
	t.Parallel()
	in := argvInputPy3()
	in.NsJailPath = ""
	_, err := runner.BuildArgv(in)
	if err == nil || !errors.Is(err, runner.ErrInvalidArgvInput) {
		t.Errorf("err = %v, want ErrInvalidArgvInput", err)
	}
}

func TestBuildArgvRelativeJobDir(t *testing.T) {
	t.Parallel()
	in := argvInputPy3()
	in.Job.JobDir = "relative/path"
	_, err := runner.BuildArgv(in)
	if err == nil {
		t.Fatal("expected error for relative JobDir")
	}
	var ae *runner.ArgvError
	if !errors.As(err, &ae) || ae.Reason != "invalid_field" {
		t.Errorf("err = %v, want invalid_field", err)
	}
}

// helpers -------------------------------------------------------------

// contains reports whether haystack contains v.
func contains(haystack []string, v string) bool {
	for _, h := range haystack {
		if h == v {
			return true
		}
	}
	return false
}

// indexOf returns the index of the first matching element, or -1.
func indexOf(haystack []string, v string) int {
	for i, h := range haystack {
		if h == v {
			return i
		}
	}
	return -1
}

// nextEquals reports whether the element following the first occurrence
// of `flag` in argv equals `want`.
func nextEquals(argv []string, flag, want string) bool {
	for i, h := range argv {
		if h == flag && i+1 < len(argv) {
			return argv[i+1] == want
		}
	}
	return false
}
