package security

// Submission carries the request-time data the Security_Validator inspects.
//
// The validator is intentionally decoupled from the Run_Handler's wire
// shape: tests can construct a Submission directly without speaking JSON,
// and the validator is reusable for any future caller (e.g. an offline
// linter) that wants the same rule catalog.
//
// All sizes are byte counts of the raw UTF-8 payload as it would be sent
// over the wire (i.e. len(string)). Callers that decode JSON should set
// PresentXxx flags only for fields the client actually sent so missing
// vs empty is distinguishable.
type Submission struct {
	// Language is the language identifier from Code_Submission.language.
	// Empty means "not present in the JSON body".
	Language string

	// Source carries the raw UTF-8 source bytes. SourceLen is the
	// authoritative byte count: callers may pass an empty Source while
	// reporting the actual streamed length so the validator never
	// reads megabytes of source code into memory just to count bytes.
	Source        string
	SourceLen     int
	PresentSource bool

	// Stdin and StdinLen mirror Source/SourceLen.
	Stdin        string
	StdinLen     int
	PresentStdin bool

	// Limits carries optional Resource_Limits overrides from the
	// request. Each Present flag mirrors whether the JSON body carried
	// a value for the corresponding field; the validator only checks
	// fields the caller actually overrode.
	Limits LimitOverrides
}

// LimitOverrides records any Resource_Limits override fields the caller
// explicitly set. Fields without a Present flag are treated as "use
// language default".
type LimitOverrides struct {
	WallTimeS         int
	PresentWallTimeS  bool
	CPUTimeS          int
	PresentCPUTimeS   bool
	MemoryMB          int
	PresentMemoryMB   bool
	ProcessCount      int
	PresentProcCount  bool
	OutputSizeMB      int
	PresentOutputSize bool
}

// Rule is one of the closed set of validator rule identifiers. The values
// are the metric label values for goboxd_security_rejections_total.
type Rule string

// Rule identifiers from architecture spec §13.
const (
	RuleMalformedSubmission   Rule = "malformed_submission"
	RuleLanguageNotRegistered Rule = "language_not_registered"
	RuleSourceSizeExceeded    Rule = "source_size_exceeded"
	RuleStdinSizeExceeded     Rule = "stdin_size_exceeded"
	RuleResourceLimitExceeded Rule = "resource_limit_exceeded"
)

// LimitName names a single Resource_Limits field. Used by Rejection.Detail
// when Rule == RuleResourceLimitExceeded so the response envelope can
// identify which limit was violated.
type LimitName string

// LimitName identifiers.
const (
	LimitWallTimeS    LimitName = "wall_time_s"
	LimitCPUTimeS     LimitName = "cpu_time_s"
	LimitMemoryMB     LimitName = "memory_mb"
	LimitProcessCount LimitName = "process_count"
	LimitOutputSizeMB LimitName = "output_size_mb"
)

// Rejection is the structured error type Validate returns on rejection.
//
// The struct is intentionally flat so the run handler can serialise it
// directly to the JSON envelope shapes documented in architecture §13.
type Rejection struct {
	// Rule is the rule that fired.
	Rule Rule

	// Field names the offending Submission field for malformed_submission.
	Field string

	// Language carries the submitted language id for language_not_registered.
	Language string

	// Limit, Requested, and Max are populated for resource_limit_exceeded.
	Limit     LimitName
	Requested int
	Max       int

	// LimitBytes is populated for source_size_exceeded and stdin_size_exceeded.
	LimitBytes int
}

// Error implements error.
func (r *Rejection) Error() string {
	return "security: rejected: " + string(r.Rule)
}

// LanguageDefinitionLookup is the slice of *registry.Registry the validator
// needs.
//
// Validate calls Lookup(id) and treats a false return as
// language_not_registered. Defining the dependency as a one-method
// interface keeps the security package decoupled from registry — the
// architecture §5 dependency graph forbids security → registry edges
// unless they go through a small interface, which keeps the import
// pure-data.
type LanguageDefinitionLookup interface {
	Lookup(id string) bool
}

// CeilingsLookup resolves per-language Resource_Limits ceilings. For Phase
// 2 Wave A, callers pass *config.Config which already implements this via
// its CeilingsFor(id) method.
type CeilingsLookup interface {
	CeilingsFor(language string) ResourceCeilings
}

// ResourceCeilings is a structural copy of internal/config.ResourceCeilings
// so internal/security does not need to import internal/config (architecture
// §5 dependency graph forbids that edge).
//
// internal/config.ResourceCeilings is a plain struct with the same field
// names; callers convert by literal-init or by direct assignment from the
// adapter in the run handler.
type ResourceCeilings struct {
	WallTimeS    int
	CPUTimeS     int
	MemoryMB     int
	ProcessCount int
	OutputSizeMB int
}

// SizeBounds carries the global size caps the validator applies before
// inspecting Resource_Limits ceilings.
type SizeBounds struct {
	MaxSourceSizeBytes int
	MaxStdinSizeBytes  int
}

// Validate runs the rule catalog against sub. It returns nil on accept
// and a *Rejection on the first failing rule. Validation is constant-time
// in the size of sub.Source/sub.Stdin (it never reads the bodies).
//
// Rule order mirrors architecture §13:
//  1. malformed_submission (missing/blank fields)
//  2. language_not_registered
//  3. source_size_exceeded
//  4. stdin_size_exceeded
//  5. resource_limit_exceeded (per-field, deterministic field order)
//
// The validator never touches the submission's Source or Stdin contents
// beyond reading their reported byte length.
func Validate(sub Submission, sizes SizeBounds, lookup LanguageDefinitionLookup, ceilings CeilingsLookup) error {
	// 1. malformed_submission — required fields.
	if sub.Language == "" {
		return &Rejection{Rule: RuleMalformedSubmission, Field: "language"}
	}
	if !sub.PresentSource {
		return &Rejection{Rule: RuleMalformedSubmission, Field: "source"}
	}
	if sub.SourceLen <= 0 {
		// Empty source is treated as malformed: every supported language
		// requires at least one byte of source code (architecture §11
		// Code_Submission table marks `source` non-empty UTF-8).
		return &Rejection{Rule: RuleMalformedSubmission, Field: "source"}
	}

	// 2. language_not_registered.
	if !lookup.Lookup(sub.Language) {
		return &Rejection{Rule: RuleLanguageNotRegistered, Language: sub.Language}
	}

	// 3. source_size_exceeded.
	if sub.SourceLen > sizes.MaxSourceSizeBytes {
		return &Rejection{Rule: RuleSourceSizeExceeded, LimitBytes: sizes.MaxSourceSizeBytes}
	}

	// 4. stdin_size_exceeded.
	if sub.PresentStdin && sub.StdinLen > sizes.MaxStdinSizeBytes {
		return &Rejection{Rule: RuleStdinSizeExceeded, LimitBytes: sizes.MaxStdinSizeBytes}
	}

	// 5. resource_limit_exceeded.
	if ceilings != nil {
		c := ceilings.CeilingsFor(sub.Language)
		if rej := checkLimit(LimitWallTimeS, sub.Limits.PresentWallTimeS, sub.Limits.WallTimeS, c.WallTimeS); rej != nil {
			return rej
		}
		if rej := checkLimit(LimitCPUTimeS, sub.Limits.PresentCPUTimeS, sub.Limits.CPUTimeS, c.CPUTimeS); rej != nil {
			return rej
		}
		if rej := checkLimit(LimitMemoryMB, sub.Limits.PresentMemoryMB, sub.Limits.MemoryMB, c.MemoryMB); rej != nil {
			return rej
		}
		if rej := checkLimit(LimitProcessCount, sub.Limits.PresentProcCount, sub.Limits.ProcessCount, c.ProcessCount); rej != nil {
			return rej
		}
		if rej := checkLimit(LimitOutputSizeMB, sub.Limits.PresentOutputSize, sub.Limits.OutputSizeMB, c.OutputSizeMB); rej != nil {
			return rej
		}
	}

	return nil
}

// checkLimit returns a *Rejection when the present-and-overridden value
// exceeds the per-language ceiling. Negative requested values are also
// rejected — Resource_Limits fields are positive integers per architecture
// §11.
func checkLimit(name LimitName, present bool, requested, ceiling int) *Rejection {
	if !present {
		return nil
	}
	if requested <= 0 || requested > ceiling {
		return &Rejection{
			Rule:      RuleResourceLimitExceeded,
			Limit:     name,
			Requested: requested,
			Max:       ceiling,
		}
	}
	return nil
}
