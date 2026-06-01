package runner

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/thesouldev/goboxd/internal/security"
)

// CommandTemplate is the trusted, registry-bound command + args pair the
// argv builder substitutes per request.
//
// Args may contain placeholders of the form {{NAME}}; only NAME values
// in the closed allowlist (security.AllowedPlaceholders) are accepted.
// The runner refuses to execute anything else.
type CommandTemplate struct {
	Command string
	Args    []string
}

// ArgvInput is the closed set of fields BuildArgv consumes.
//
// Every field is provided by trusted code (Configuration, Language_Registry,
// Run_Handler) — no Code_Submission field reaches the builder. Property 36
// asserts this invariant by drawing arbitrary submissions and checking the
// resulting argv depends only on the trusted inputs.
type ArgvInput struct {
	// NsJailPath is the absolute path to the nsjail binary. Required.
	NsJailPath string

	// Step distinguishes the compile vs run invocation; it picks the
	// command template from the language registry.
	Step StepKind

	// Template is the registry-bound CommandTemplate for the step.
	Template CommandTemplate

	// Job carries the per-request validated inputs (filenames, limits,
	// workspace path).
	Job Job

	// EnvAllowlist is the closed set of environment variables passed to
	// the sandboxed child. PATH and LANG are mandatory; per-language
	// extras come from the language registry. The slice is copied
	// verbatim into the argv as `--env NAME=VALUE` flags.
	EnvAllowlist []EnvVar

	// BindMounts is the read-only host paths nsjail bind-mounts into
	// the sandbox alongside JOB_DIR.
	BindMounts []BindMount

	// SeccompPolicy is a non-empty static string passed via
	// --seccomp_string. The architecture spec leaves the contents to
	// the implementation; this builder just propagates it.
	SeccompPolicy string

	// SandboxGuestDir is the path inside the sandbox where JOB_DIR is
	// bind-mounted (default "/sandbox"). Empty means use the default.
	SandboxGuestDir string

	// User and Group map the sandboxed child to an unprivileged uid/gid
	// inside the user namespace. Default 65534/65534 (nobody:nogroup).
	User  int
	Group int
}

// ErrInvalidArgvInput is returned by BuildArgv when its input is
// structurally invalid (missing nsjail path, missing job dir, empty
// template, etc.). Use errors.Is to match.
var ErrInvalidArgvInput = errors.New("runner: invalid argv input")

// ArgvError carries the structured fields the run handler logs when the
// argv builder rejects an input.
type ArgvError struct {
	Reason string
	Detail string

	// Cause optionally wraps the underlying error so errors.Is /
	// errors.As walks past the *ArgvError envelope. Most rejections
	// have no cause; unsafe-filename failures wrap the
	// *security.FilenameError so callers can still match
	// security.ErrUnsafeFilename.
	Cause error
}

// Error implements error.
func (e *ArgvError) Error() string { return fmt.Sprintf("runner: argv: %s: %s", e.Reason, e.Detail) }

// Is supports errors.Is(err, ErrInvalidArgvInput).
func (e *ArgvError) Is(target error) bool { return target == ErrInvalidArgvInput }

// Unwrap returns the wrapped cause for errors.Is / errors.As traversal.
func (e *ArgvError) Unwrap() error { return e.Cause }

// Documented Resource_Limits ranges from architecture spec §10.3. The
// argv builder rejects any limit outside these bounds before emitting
// the corresponding nsjail flag.
const (
	wallTimeMin    = 1
	wallTimeMax    = 60
	cpuTimeMin     = 1
	cpuTimeMax     = 60
	memoryMin      = 16
	memoryMax      = 1024
	processMin     = 1
	processMax     = 64
	outputSizeMin  = 1
	outputSizeMax  = 64
)

// BuildArgv builds the full nsjail argv for a single sandbox invocation.
//
// The output is a pure function of in. No file I/O, no goroutines, no
// global state; identical inputs produce identical argv slices (Property
// 17). The builder enforces:
//
//   - Argv[0] equals in.NsJailPath, never anything else (Property 16).
//   - Namespace flags --user/--pid/--mount/--net/--ipc/--uts and
//     --disable_proc are emitted unconditionally (architecture §10.2).
//   - Resource-limit flags (--time_limit, --rlimit_cpu, --rlimit_as,
//     --rlimit_nproc, --rlimit_fsize) carry exactly the values from
//     in.Job.EffectiveLimits, validated against architecture §10.3
//     ranges.
//   - Exactly one rw bind mount, --bindmount {{JOB_DIR}}:{{guest}}.
//   - All in.BindMounts are emitted as --bindmount/ro pairs.
//   - Env vars come from in.EnvAllowlist only; no Code_Submission field
//     can flow into the env (Property 36).
//   - Placeholder substitution is restricted to security.AllowedPlaceholders;
//     unknown names produce *security.PlaceholderError wrapped in
//     ArgvError (Property 37).
//   - The argv ends with `--` followed by the resolved child argv. No
//     shell interpreter is ever invoked (architecture §11 "Command
//     Template and Argument Safety").
func BuildArgv(in ArgvInput) ([]string, error) {
	if err := validateInput(in); err != nil {
		return nil, err
	}
	guest := in.SandboxGuestDir
	if guest == "" {
		guest = "/sandbox"
	}
	user := in.User
	if user == 0 {
		user = 65534
	}
	group := in.Group
	if group == 0 {
		group = 65534
	}

	limits := in.Job.EffectiveLimits
	if err := validateLimits(limits); err != nil {
		return nil, err
	}

	// nsjail argv layout — keeping every flag explicit (no abbreviations)
	// so the test diff is readable when an architecture-spec value moves.
	argv := []string{
		in.NsJailPath,
		"--mode", "o",
		"--quiet",
		"--really_quiet",
		"--user", strconv.Itoa(user),
		"--group", strconv.Itoa(group),
		"--hostname", "goboxd-sandbox",
		"--cwd", guest,

		// Namespaces (architecture §10.2). nsjail enables these by
		// default for --mode o; we still pass --disable_proc to ensure
		// /proc is not exposed.
		"--disable_proc",

		// Resource limits (architecture §10.3).
		"--time_limit", strconv.Itoa(limits.WallTimeS),
		"--rlimit_cpu", strconv.Itoa(limits.CPUTimeS),
		"--rlimit_as", strconv.Itoa(limits.MemoryMB),
		"--rlimit_nproc", strconv.Itoa(limits.ProcessCount),
		"--rlimit_fsize", strconv.Itoa(limits.OutputSizeMB),
		"--rlimit_nofile", "64",
		"--max_cpus", "1",

		// Per-request rw bind mount: JOB_DIR at /sandbox.
		"--bindmount", in.Job.JobDir + ":" + guest,
	}

	// Read-only bind mounts.
	for _, m := range in.BindMounts {
		if m.ReadWrite {
			return nil, &ArgvError{
				Reason: "extra_rw_mount",
				Detail: fmt.Sprintf("only the per-request job dir may be rw, got %s rw", m.HostPath),
			}
		}
		argv = append(argv, "--bindmount_ro", m.HostPath+":"+m.GuestPath)
	}

	// Tmpfs /tmp so the child has a writable scratch area outside JOB_DIR.
	argv = append(argv, "--tmpfsmount", "/tmp")

	// Closed env allowlist — NEVER includes any Code_Submission field.
	for _, e := range in.EnvAllowlist {
		argv = append(argv, "--env", e.Name+"="+e.Value)
	}

	// Seccomp policy.
	argv = append(argv, "--seccomp_string", in.SeccompPolicy)

	// nsjail's own log goes to /dev/null; the runner separately captures
	// the child's stderr.
	argv = append(argv, "--log", "/dev/null")

	// End of nsjail flags; child argv follows.
	argv = append(argv, "--")
	argv = append(argv, in.Template.Command)

	for _, raw := range in.Template.Args {
		expanded, err := expandPlaceholders(raw, in.Job)
		if err != nil {
			return nil, err
		}
		argv = append(argv, expanded)
	}

	return argv, nil
}

// validateInput rejects inputs that would produce a nonsensical argv.
func validateInput(in ArgvInput) error {
	if strings.TrimSpace(in.NsJailPath) == "" {
		return &ArgvError{Reason: "missing_field", Detail: "NsJailPath is empty"}
	}
	if in.Step != StepCompile && in.Step != StepRun {
		return &ArgvError{Reason: "unknown_step", Detail: string(in.Step)}
	}
	if strings.TrimSpace(in.Template.Command) == "" {
		return &ArgvError{Reason: "missing_field", Detail: "Template.Command is empty"}
	}
	if strings.TrimSpace(in.Job.JobDir) == "" {
		return &ArgvError{Reason: "missing_field", Detail: "Job.JobDir is empty"}
	}
	if !strings.HasPrefix(in.Job.JobDir, "/") {
		return &ArgvError{Reason: "invalid_field", Detail: "Job.JobDir must be absolute"}
	}
	if strings.TrimSpace(in.SeccompPolicy) == "" {
		return &ArgvError{Reason: "missing_field", Detail: "SeccompPolicy is empty"}
	}
	if err := security.ValidateBasename(in.Job.SourceFilename); err != nil {
		return &ArgvError{Reason: "unsafe_filename", Detail: "Job.SourceFilename: " + err.Error(), Cause: err}
	}
	if in.Job.BinaryFilename != "" {
		if err := security.ValidateBasename(in.Job.BinaryFilename); err != nil {
			return &ArgvError{Reason: "unsafe_filename", Detail: "Job.BinaryFilename: " + err.Error(), Cause: err}
		}
	}
	if in.Job.StdinFile != "" {
		if err := security.ValidateBasename(in.Job.StdinFile); err != nil {
			return &ArgvError{Reason: "unsafe_filename", Detail: "Job.StdinFile: " + err.Error(), Cause: err}
		}
	}
	return nil
}

// validateLimits enforces the architecture §10.3 inclusive ranges.
func validateLimits(l Limits) error {
	type rng struct {
		name     string
		v        int
		lo, hi   int
	}
	for _, c := range []rng{
		{"wall_time_s", l.WallTimeS, wallTimeMin, wallTimeMax},
		{"cpu_time_s", l.CPUTimeS, cpuTimeMin, cpuTimeMax},
		{"memory_mb", l.MemoryMB, memoryMin, memoryMax},
		{"process_count", l.ProcessCount, processMin, processMax},
		{"output_size_mb", l.OutputSizeMB, outputSizeMin, outputSizeMax},
	} {
		if c.v < c.lo || c.v > c.hi {
			return &ArgvError{
				Reason: "limit_out_of_range",
				Detail: fmt.Sprintf("%s = %d outside [%d, %d]", c.name, c.v, c.lo, c.hi),
			}
		}
	}
	return nil
}

// expandPlaceholders replaces every {{NAME}} occurrence with its
// allowed value. Unknown placeholders return an *ArgvError wrapping a
// *security.PlaceholderError.
//
// The substitution is single-pass and non-recursive: substituted
// values are inserted as raw strings and never re-scanned (architecture
// §11 substitution algorithm).
func expandPlaceholders(arg string, job Job) (string, error) {
	if !strings.Contains(arg, "{{") {
		return arg, nil
	}
	var b strings.Builder
	rest := arg
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			b.WriteString(rest)
			return b.String(), nil
		}
		b.WriteString(rest[:open])

		close := strings.Index(rest[open+2:], "}}")
		if close < 0 {
			return "", &ArgvError{
				Reason: "unterminated_placeholder",
				Detail: fmt.Sprintf("entry %q contains unterminated placeholder", arg),
				Cause:  &security.PlaceholderError{ArgsEntry: arg, Placeholder: rest[open:]},
			}
		}
		name := rest[open+2 : open+2+close]
		value, ok := placeholderValue(name, job)
		if !ok {
			return "", &ArgvError{
				Reason: "unknown_placeholder",
				Detail: fmt.Sprintf("entry %q contains placeholder %q (not in allowlist)", arg, name),
				Cause:  &security.PlaceholderError{ArgsEntry: arg, Placeholder: name},
			}
		}
		b.WriteString(value)
		rest = rest[open+2+close+2:]
	}
}

// placeholderValue resolves an allowlisted placeholder name to its
// validated job value.
//
// The function returns (value, ok) so callers can distinguish a
// recognised-but-empty case from a not-in-allowlist case. STDIN_FILE is
// explicitly rejected when Job.StdinFile is empty: the registry would
// otherwise let a language reference a file that the runner did not
// create.
func placeholderValue(name string, job Job) (string, bool) {
	if !security.IsAllowedPlaceholder(name) {
		return "", false
	}
	switch name {
	case "SOURCE_FILENAME":
		return job.SourceFilename, true
	case "BINARY_FILENAME":
		return job.BinaryFilename, true
	case "STDIN_FILE":
		if job.StdinFile == "" {
			return "", false
		}
		return job.StdinFile, true
	default:
		// Unreachable: IsAllowedPlaceholder gated the name above.
		return "", false
	}
}
