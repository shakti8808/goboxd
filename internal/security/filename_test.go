package security_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thesouldev/goboxd/internal/security"
)

// TestValidateBasenameAccept covers the canonical happy-path inputs from
// the architecture spec §12 worked examples.
func TestValidateBasenameAccept(t *testing.T) {
	t.Parallel()
	cases := []string{"main.py", "main.cpp", "main", "hello_world", "a", "x123.bin", "你好.py"}
	for _, c := range cases {
		if err := security.ValidateBasename(c); err != nil {
			t.Errorf("ValidateBasename(%q) returned error: %v", c, err)
		}
	}
}

// TestValidateBasenameReject walks every documented rejection rule.
func TestValidateBasenameReject(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		in     string
		reason string
	}{
		{"empty", "", "empty"},
		{"null byte", "main\x00.py", "null_byte"},
		{"control byte tab", "main\t.py", "control_byte"},
		{"control byte newline", "main\n.py", "control_byte"},
		{"path separator forward slash", "../main.py", "path_separator"},
		{"path separator forward slash root", "/main.py", "path_separator"},
		{"path separator backslash", `main\.py`, "path_separator"},
		{"traversal sequence", "..main.py", "path_traversal"},
		{"traversal sequence in middle", "a..b", "path_traversal"},
		{"leading dot", ".env", "leading_dot"},
		{"leading dot single", ".", "leading_dot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := security.ValidateBasename(tc.in)
			if err == nil {
				t.Fatalf("ValidateBasename(%q) returned nil; want rejection", tc.in)
			}
			var fe *security.FilenameError
			if !errors.As(err, &fe) {
				t.Fatalf("error is not *FilenameError: %v", err)
			}
			if fe.Reason != tc.reason {
				t.Errorf("Reason = %q, want %q", fe.Reason, tc.reason)
			}
			if !errors.Is(err, security.ErrUnsafeFilename) {
				t.Errorf("errors.Is(err, ErrUnsafeFilename) = false; want true")
			}
		})
	}
}

// TestValidateBasenameInvalidUTF8 confirms invalid UTF-8 is rejected
// after the cheaper byte-level checks.
func TestValidateBasenameInvalidUTF8(t *testing.T) {
	bad := string([]byte{0x80, 0x81, 0x82})
	err := security.ValidateBasename(bad)
	if err == nil {
		t.Fatal("expected rejection for invalid UTF-8")
	}
	var fe *security.FilenameError
	if !errors.As(err, &fe) || fe.Reason != "invalid_utf8" {
		t.Errorf("Reason = %v, want invalid_utf8", fe)
	}
}

// TestHasControlBytes spot-checks the redaction helper.
func TestHasControlBytes(t *testing.T) {
	t.Parallel()
	if security.HasControlBytes("main.py") {
		t.Error("plain filename flagged as control-byte-containing")
	}
	if !security.HasControlBytes("main\x00.py") {
		t.Error("null byte not detected")
	}
	if !security.HasControlBytes("main\n.py") {
		t.Error("newline not detected")
	}
}

// TestPlaceholderAllowlist asserts the closed allowlist content and
// IsAllowedPlaceholder semantics.
func TestPlaceholderAllowlist(t *testing.T) {
	t.Parallel()
	want := map[string]struct{}{
		"SOURCE_FILENAME": {},
		"BINARY_FILENAME": {},
		"STDIN_FILE":      {},
	}
	if len(security.AllowedPlaceholders) != len(want) {
		t.Fatalf("AllowedPlaceholders has %d entries, want %d", len(security.AllowedPlaceholders), len(want))
	}
	for _, name := range security.AllowedPlaceholders {
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected entry %q in AllowedPlaceholders", name)
		}
		if !security.IsAllowedPlaceholder(name) {
			t.Errorf("IsAllowedPlaceholder(%q) = false; want true", name)
		}
	}
	for _, bad := range []string{"", "PATH", "source_filename", "EXTRA"} {
		if security.IsAllowedPlaceholder(bad) {
			t.Errorf("IsAllowedPlaceholder(%q) = true; want false", bad)
		}
	}
}

// TestScanPlaceholders covers acceptance, multi-token, and rejection.
func TestScanPlaceholders(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		arg         string
		err         bool
		placeholder string
	}{
		{"plain string", "main.cpp", false, ""},
		{"single allowed", "{{SOURCE_FILENAME}}", false, ""},
		{"two allowed", "{{SOURCE_FILENAME}} {{STDIN_FILE}}", false, ""},
		{"unknown", "{{UNKNOWN}}", true, "UNKNOWN"},
		{"mixed", "{{SOURCE_FILENAME}} -o {{BINARY}}", true, "BINARY"},
		{"unterminated", "{{SOURCE_FILENAME", true, "{{SOURCE_FILENAME"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := security.ScanPlaceholders(tc.arg)
			if tc.err {
				if err == nil {
					t.Fatalf("ScanPlaceholders(%q) = nil; want error", tc.arg)
				}
				if !errors.Is(err, security.ErrUnknownPlaceholder) {
					t.Errorf("errors.Is(err, ErrUnknownPlaceholder) = false; want true")
				}
				var pe *security.PlaceholderError
				if !errors.As(err, &pe) {
					t.Fatalf("error is not *PlaceholderError: %v", err)
				}
				if pe.Placeholder != tc.placeholder {
					t.Errorf("Placeholder = %q, want %q", pe.Placeholder, tc.placeholder)
				}
				if !strings.Contains(err.Error(), pe.Placeholder) {
					t.Errorf("error message %q does not mention placeholder %q", err, pe.Placeholder)
				}
				return
			}
			if err != nil {
				t.Errorf("ScanPlaceholders(%q) returned error: %v", tc.arg, err)
			}
		})
	}
}
