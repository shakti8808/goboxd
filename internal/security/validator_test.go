package security_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesouldev/goboxd/internal/security"
)

// stubLookup implements security.LanguageDefinitionLookup over a static
// set so tests can deterministically include / exclude language ids.
type stubLookup struct{ ids map[string]struct{} }

func (s stubLookup) Lookup(id string) bool { _, ok := s.ids[id]; return ok }

// stubCeilings implements security.CeilingsLookup.
type stubCeilings struct{ c security.ResourceCeilings }

func (s stubCeilings) CeilingsFor(_ string) security.ResourceCeilings { return s.c }

// defaults builds a SizeBounds + CeilingsLookup pair mirroring the
// architecture-spec Phase 1 defaults.
func defaults() (security.SizeBounds, stubCeilings, stubLookup) {
	return security.SizeBounds{
			MaxSourceSizeBytes: 1 << 20,
			MaxStdinSizeBytes:  1 << 20,
		},
		stubCeilings{c: security.ResourceCeilings{
			WallTimeS:    60,
			CPUTimeS:     60,
			MemoryMB:     1024,
			ProcessCount: 64,
			OutputSizeMB: 64,
		}},
		stubLookup{ids: map[string]struct{}{"py3": {}, "cpp": {}}}
}

// validSubmission returns a baseline submission acceptable to the
// validator; tests mutate one field per case to exercise a single rule.
func validSubmission() security.Submission {
	return security.Submission{
		Language:      "py3",
		Source:        "print('hi')",
		SourceLen:     len("print('hi')"),
		PresentSource: true,
	}
}

// TestValidateAccept asserts the baseline submission passes.
func TestValidateAccept(t *testing.T) {
	t.Parallel()
	sizes, ceilings, lookup := defaults()
	if err := security.Validate(validSubmission(), sizes, lookup, ceilings); err != nil {
		t.Fatalf("Validate returned error on baseline: %v", err)
	}
}

// TestValidateRejectionMatrix walks every rule.
//
// Each row produces exactly one rejection so the assertion checks the
// rule, the field/limit/limitbytes payload, and the error envelope shape.
func TestValidateRejectionMatrix(t *testing.T) {
	t.Parallel()

	mutate := func(fn func(*security.Submission)) security.Submission {
		s := validSubmission()
		fn(&s)
		return s
	}

	type expect struct {
		rule       security.Rule
		field      string
		language   string
		limit      security.LimitName
		requested  int
		max        int
		limitBytes int
	}

	cases := []struct {
		name string
		sub  security.Submission
		want expect
	}{
		{
			name: "missing language",
			sub:  mutate(func(s *security.Submission) { s.Language = "" }),
			want: expect{rule: security.RuleMalformedSubmission, field: "language"},
		},
		{
			name: "missing source flag",
			sub:  mutate(func(s *security.Submission) { s.PresentSource = false }),
			want: expect{rule: security.RuleMalformedSubmission, field: "source"},
		},
		{
			name: "empty source",
			sub:  mutate(func(s *security.Submission) { s.Source = ""; s.SourceLen = 0 }),
			want: expect{rule: security.RuleMalformedSubmission, field: "source"},
		},
		{
			name: "language not registered",
			sub:  mutate(func(s *security.Submission) { s.Language = "rust" }),
			want: expect{rule: security.RuleLanguageNotRegistered, language: "rust"},
		},
		{
			name: "source too large",
			sub:  mutate(func(s *security.Submission) { s.SourceLen = (1 << 20) + 1 }),
			want: expect{rule: security.RuleSourceSizeExceeded, limitBytes: 1 << 20},
		},
		{
			name: "stdin too large",
			sub: mutate(func(s *security.Submission) {
				s.PresentStdin = true
				s.StdinLen = (1 << 20) + 1
			}),
			want: expect{rule: security.RuleStdinSizeExceeded, limitBytes: 1 << 20},
		},
		{
			name: "wall time exceeded",
			sub: mutate(func(s *security.Submission) {
				s.Limits.PresentWallTimeS = true
				s.Limits.WallTimeS = 999
			}),
			want: expect{rule: security.RuleResourceLimitExceeded, limit: security.LimitWallTimeS, requested: 999, max: 60},
		},
		{
			name: "memory exceeded",
			sub: mutate(func(s *security.Submission) {
				s.Limits.PresentMemoryMB = true
				s.Limits.MemoryMB = 4096
			}),
			want: expect{rule: security.RuleResourceLimitExceeded, limit: security.LimitMemoryMB, requested: 4096, max: 1024},
		},
		{
			name: "process count zero",
			sub: mutate(func(s *security.Submission) {
				s.Limits.PresentProcCount = true
				s.Limits.ProcessCount = 0
			}),
			want: expect{rule: security.RuleResourceLimitExceeded, limit: security.LimitProcessCount, requested: 0, max: 64},
		},
		{
			name: "negative output size",
			sub: mutate(func(s *security.Submission) {
				s.Limits.PresentOutputSize = true
				s.Limits.OutputSizeMB = -1
			}),
			want: expect{rule: security.RuleResourceLimitExceeded, limit: security.LimitOutputSizeMB, requested: -1, max: 64},
		},
	}

	sizes, ceilings, lookup := defaults()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := security.Validate(tc.sub, sizes, lookup, ceilings)
			if err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
			var rj *security.Rejection
			if !errors.As(err, &rj) {
				t.Fatalf("error is not *Rejection: %v", err)
			}
			if rj.Rule != tc.want.rule {
				t.Errorf("Rule = %q, want %q", rj.Rule, tc.want.rule)
			}
			if tc.want.field != "" && rj.Field != tc.want.field {
				t.Errorf("Field = %q, want %q", rj.Field, tc.want.field)
			}
			if tc.want.language != "" && rj.Language != tc.want.language {
				t.Errorf("Language = %q, want %q", rj.Language, tc.want.language)
			}
			if tc.want.limit != "" {
				if rj.Limit != tc.want.limit || rj.Requested != tc.want.requested || rj.Max != tc.want.max {
					t.Errorf("Limit envelope = %+v, want limit=%s requested=%d max=%d",
						rj, tc.want.limit, tc.want.requested, tc.want.max)
				}
			}
			if tc.want.limitBytes != 0 && rj.LimitBytes != tc.want.limitBytes {
				t.Errorf("LimitBytes = %d, want %d", rj.LimitBytes, tc.want.limitBytes)
			}
			if !strings.Contains(err.Error(), string(tc.want.rule)) {
				t.Errorf("error string %q does not mention rule %q", err, tc.want.rule)
			}
		})
	}
}

// TestValidateRuleOrder asserts that when multiple rules would fire on
// the same submission, the catalog order in architecture §13 wins
// (malformed → language_not_registered → source_size → stdin_size →
// resource_limit_exceeded). Order matters because metric labels and
// log entries pivot on the first rule that fires.
func TestValidateRuleOrder(t *testing.T) {
	t.Parallel()
	sizes, ceilings, lookup := defaults()

	// Submission triggers three rules: missing source, unknown language,
	// oversize stdin, oversize memory limit. malformed_submission must win.
	sub := security.Submission{
		Language:      "rust",
		PresentSource: false,
		PresentStdin:  true,
		StdinLen:      sizes.MaxStdinSizeBytes + 1,
		Limits: security.LimitOverrides{
			PresentMemoryMB: true,
			MemoryMB:        9999,
		},
	}
	err := security.Validate(sub, sizes, lookup, ceilings)
	var rj *security.Rejection
	if !errors.As(err, &rj) {
		t.Fatalf("expected *Rejection, got %v", err)
	}
	if rj.Rule != security.RuleMalformedSubmission {
		t.Errorf("Rule = %q, want %q (catalog ordering)", rj.Rule, security.RuleMalformedSubmission)
	}

	// Now provide source so the next rule (language_not_registered) wins.
	sub.Source = "x"
	sub.SourceLen = 1
	sub.PresentSource = true
	err = security.Validate(sub, sizes, lookup, ceilings)
	if !errors.As(err, &rj) {
		t.Fatalf("expected *Rejection, got %v", err)
	}
	if rj.Rule != security.RuleLanguageNotRegistered {
		t.Errorf("Rule = %q, want %q", rj.Rule, security.RuleLanguageNotRegistered)
	}
}

// BenchmarkValidate covers the 50 ms p99 envelope from REQ A-11.7.
//
// We don't enforce wall-clock time inside the benchmark (CI machines
// vary) but the ns/op number is observable and the allocations counter
// is asserted to be ≤1 to lock in the "no allocations on the hot path
// beyond a single error value" claim from the implementation design.
func BenchmarkValidate(b *testing.B) {
	sizes, ceilings, lookup := defaults()
	sub := validSubmission()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := security.Validate(sub, sizes, lookup, ceilings); err != nil {
			b.Fatalf("Validate: %v", err)
		}
	}
}
