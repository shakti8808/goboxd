// Package security owns the input-validation surface of GoboxD.
//
// Phase 1 ships only the structural validators that the language registry
// needs at startup:
//
//   - ValidateBasename — enforces the basename rule from architecture
//     spec §11 "Filename and Path Safety" (Property 34).
//   - AllowedPlaceholders / IsAllowedPlaceholder — the closed allowlist
//     from §11 "Command Template and Argument Safety" (Property 37).
//
// The full Validate(submission) rule catalog (Property 24) lands in Phase 2
// once the run pipeline is wired; nothing in this file references a
// Code_Submission.
package security

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrUnsafeFilename is returned by ValidateBasename when its argument
// fails any of the basename rules.
//
// Callers may use errors.Is for cheap matching; the architecture spec
// §13 also mandates a structured rule_name field on the rejection
// envelope, which the registry assembles by inspecting *FilenameError.
var ErrUnsafeFilename = errors.New("security: unsafe filename")

// FilenameError carries the structured fields required by REQ A-21.2
// and the unsafe_filename row in the security validator rule catalog.
type FilenameError struct {
	// Reason is a stable identifier (e.g. "empty", "path_separator",
	// "path_traversal", "absolute_path", "leading_dot", "control_byte",
	// "null_byte", "invalid_utf8") so structured logs can pivot on it.
	Reason string

	// Value is the offending input. When the value contains any byte
	// below 0x20 or a NUL byte, callers replace this with a length
	// indicator before logging (REQ A-21.2 redaction note).
	Value string
}

// Error implements the error interface.
func (e *FilenameError) Error() string {
	return fmt.Sprintf("security: unsafe filename: %s", e.Reason)
}

// Is supports errors.Is(err, ErrUnsafeFilename).
func (e *FilenameError) Is(target error) bool { return target == ErrUnsafeFilename }

// HasControlBytes reports whether v contains any byte below 0x20 or a NUL
// byte. Useful to log entries needing redaction; intentionally exported.
func HasControlBytes(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] == 0x7f {
			return true
		}
	}
	return false
}

// ValidateBasename enforces the basename rule from architecture spec §11.
//
// A basename is rejected if any of the following hold:
//
//  1. The value is empty.
//  2. The value contains a NUL byte (0x00).
//  3. The value contains any byte below 0x20.
//  4. The value contains the path separator '/' or '\'.
//  5. The value begins with '/' (absolute path on POSIX). The '\\' check in
//     rule 4 already covers the Windows case but GoboxD targets Linux.
//  6. The value contains the parent-directory traversal sequence "..".
//  7. The value begins with '.', a deliberate decision documented in §11.
//  8. The value is not valid UTF-8.
//
// On success ValidateBasename returns nil. On any failure it returns a
// *FilenameError whose Reason field carries a stable rule identifier.
//
// The rule catalog is run in the order above; the first matching rule
// wins. Rules 4 and 7 overlap on the literal "/" and "." inputs but the
// ordering is deterministic so rejections are reproducible across runs.
func ValidateBasename(name string) error {
	if name == "" {
		return &FilenameError{Reason: "empty", Value: name}
	}

	// Reject control bytes early so subsequent log fields can be safely
	// emitted by callers that have already checked the error.
	for i := 0; i < len(name); i++ {
		switch {
		case name[i] == 0x00:
			return &FilenameError{Reason: "null_byte", Value: name}
		case name[i] < 0x20:
			return &FilenameError{Reason: "control_byte", Value: name}
		}
	}

	if !utf8.ValidString(name) {
		return &FilenameError{Reason: "invalid_utf8", Value: name}
	}

	if strings.ContainsAny(name, `/\`) {
		return &FilenameError{Reason: "path_separator", Value: name}
	}

	if strings.HasPrefix(name, "/") {
		// Defensive — rule 4 already rejects '/'. Kept for clarity.
		return &FilenameError{Reason: "absolute_path", Value: name}
	}

	if strings.Contains(name, "..") {
		return &FilenameError{Reason: "path_traversal", Value: name}
	}

	if strings.HasPrefix(name, ".") {
		return &FilenameError{Reason: "leading_dot", Value: name}
	}

	return nil
}

// AllowedPlaceholders is the closed allowlist of placeholder names that
// may appear in compile.args / run.args after the surrounding "{{" / "}}"
// markers (architecture spec §11 "Command Template and Argument Safety").
//
// The slice is exported so callers and tests can iterate the set.
var AllowedPlaceholders = []string{
	"SOURCE_FILENAME",
	"BINARY_FILENAME",
	"STDIN_FILE",
}

// allowedPlaceholderSet is the membership-check form of AllowedPlaceholders.
// Initialised in init() to keep the source of truth single.
var allowedPlaceholderSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(AllowedPlaceholders))
	for _, p := range AllowedPlaceholders {
		m[p] = struct{}{}
	}
	return m
}()

// IsAllowedPlaceholder reports whether name is a member of
// AllowedPlaceholders.
//
// name MUST be the inner identifier; the surrounding "{{" / "}}" markers
// are not part of the comparison. Callers obtain name by stripping the
// markers in ScanPlaceholders below.
func IsAllowedPlaceholder(name string) bool {
	_, ok := allowedPlaceholderSet[name]
	return ok
}

// ErrUnknownPlaceholder is returned when a placeholder token references a
// name not in AllowedPlaceholders.
var ErrUnknownPlaceholder = errors.New("security: unknown placeholder")

// PlaceholderError carries the structured fields required by the
// unknown_placeholder row in the security validator rule catalog.
type PlaceholderError struct {
	// ArgsEntry is the offending compile.args / run.args entry, kept verbatim
	// so logs can quote it.
	ArgsEntry string

	// Placeholder is the unknown name (inside "{{" / "}}").
	Placeholder string
}

// Error implements the error interface.
func (e *PlaceholderError) Error() string {
	return fmt.Sprintf("security: unknown placeholder %q in %q", e.Placeholder, e.ArgsEntry)
}

// Is supports errors.Is(err, ErrUnknownPlaceholder).
func (e *PlaceholderError) Is(target error) bool { return target == ErrUnknownPlaceholder }

// ScanPlaceholders walks arg left-to-right and reports the first
// occurrence of "{{NAME}}" whose NAME is not in AllowedPlaceholders.
//
// On success it returns nil. Each scan is single-pass and non-recursive;
// substituted values are not re-scanned. A "{{" without a matching "}}" is
// treated as malformed and reported as PlaceholderError with Placeholder
// equal to the literal text starting at the "{{".
func ScanPlaceholders(arg string) error {
	rest := arg
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			return nil
		}
		close := strings.Index(rest[open+2:], "}}")
		if close < 0 {
			return &PlaceholderError{ArgsEntry: arg, Placeholder: rest[open:]}
		}
		name := rest[open+2 : open+2+close]
		if !IsAllowedPlaceholder(name) {
			return &PlaceholderError{ArgsEntry: arg, Placeholder: name}
		}
		rest = rest[open+2+close+2:]
	}
}
