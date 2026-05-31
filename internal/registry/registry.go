// Package registry owns the YAML language registry.
//
// On startup the registry loads every Language_Definition from a YAML file
// whose path is supplied via configuration. Loading is fail-fast: any
// malformed YAML, missing required field, duplicate identifier, unsafe
// filename, or unknown placeholder causes Load to return an error and
// cmd/goboxd to exit non-zero before opening the HTTP listener
// (architecture REQs A-7.1..A-7.7, A-21.1, A-21.2, A-22.4).
//
// After Load returns successfully the *Registry is immutable. Get is a
// case-sensitive exact-match lookup; List returns the registered identifiers
// in deterministic file order. Phase 1 satisfies Properties 7, 8, 9, and the
// load-time half of 35.
package registry

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesouldev/goboxd/internal/security"
)

// ResourceLimits mirrors the YAML schema's compile.limits / run.limits block.
//
// The Phase 1 loader does not enforce range bounds on these fields; that
// validation is the architecture-spec responsibility of the runner argv
// builder in Phase 2 (where the same struct is used).
type ResourceLimits struct {
	WallTimeS     int `yaml:"wall_time_s"`
	CPUTimeS      int `yaml:"cpu_time_s"`
	MemoryMB      int `yaml:"memory_mb"`
	ProcessCount  int `yaml:"process_count"`
	OutputSizeMB  int `yaml:"output_size_mb"`
}

// Step describes one of compile / run.
type Step struct {
	Command string         `yaml:"command"`
	Args    []string       `yaml:"args"`
	Limits  ResourceLimits `yaml:"limits"`
}

// Definition is one entry in the language registry.
//
// Compile is optional; an absent Compile block (yaml: omitted entirely)
// instructs the runner to skip the compile step (REQ A-8.3).
type Definition struct {
	ID             string `yaml:"id"`
	SourceFilename string `yaml:"source_filename"`
	BinaryFilename string `yaml:"binary_filename,omitempty"`

	Compile *Step `yaml:"compile,omitempty"`
	Run     Step  `yaml:"run"`
}

// HasCompile reports whether the definition declares a compile step.
//
// The Phase 1 registry treats an empty compile.command as "no compile
// step" so the YAML schema can include a placeholder block without
// triggering compilation.
func (d *Definition) HasCompile() bool {
	return d.Compile != nil && strings.TrimSpace(d.Compile.Command) != ""
}

// fileShape mirrors the top-level YAML structure.
type fileShape struct {
	Languages []Definition `yaml:"languages"`
}

// LoadError describes a registry load failure.
//
// It is returned both for malformed YAML and for any per-definition
// validation failure (missing required field, duplicate id, unsafe
// filename, unknown placeholder). The fields are emitted by cmd/goboxd's
// startup-failure log entry (REQ A-7.3, REQ A-7.4, REQ A-7.7, REQ A-21.2,
// REQ A-22.4).
type LoadError struct {
	// Path is the YAML file path that failed to load.
	Path string

	// Reason is a stable identifier for the failure category (e.g.
	// "io_failed", "yaml_parse_failed", "missing_field",
	// "duplicate_id", "unsafe_filename", "unknown_placeholder").
	Reason string

	// LanguageID is the offending entry's id when known; empty when the
	// failure occurred before the id was parsed.
	LanguageID string

	// Field names the failing field for missing-field / unsafe-filename
	// reasons; empty otherwise.
	Field string

	// Detail is a free-form human-readable suffix (e.g. wrapped error
	// message). Never contains user-controlled bytes verbatim.
	Detail string

	// Err wraps any underlying cause (e.g. yaml.TypeError) for
	// errors.Is/Unwrap.
	Err error
}

// Error implements error.
func (e *LoadError) Error() string {
	parts := []string{
		fmt.Sprintf("registry: load failed: %s", e.Reason),
	}
	if e.Path != "" {
		parts = append(parts, fmt.Sprintf("path=%s", e.Path))
	}
	if e.LanguageID != "" {
		parts = append(parts, fmt.Sprintf("language_id=%s", e.LanguageID))
	}
	if e.Field != "" {
		parts = append(parts, fmt.Sprintf("field=%s", e.Field))
	}
	if e.Detail != "" {
		parts = append(parts, e.Detail)
	}
	return strings.Join(parts, " ")
}

// Unwrap returns the wrapped cause for errors.Is/Unwrap.
func (e *LoadError) Unwrap() error { return e.Err }

// Registry is an immutable snapshot of loaded language definitions.
type Registry struct {
	defs  []Definition
	byID  map[string]Definition
	order []string
}

// Load reads and validates a YAML registry file.
//
// On any failure Load returns a *LoadError describing the failure category
// and the offending field/id when known.
func Load(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path comes from validated config
	if err != nil {
		return nil, &LoadError{Path: path, Reason: "io_failed", Detail: err.Error(), Err: err}
	}
	defer func() { _ = f.Close() }()

	return parse(path, f)
}

// parse decodes and validates the YAML stream from r.
//
// Split out from Load so tests can drive the parser without touching the
// filesystem.
func parse(path string, r io.Reader) (*Registry, error) {
	var shape fileShape
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&shape); err != nil {
		return nil, &LoadError{Path: path, Reason: "yaml_parse_failed", Detail: err.Error(), Err: err}
	}
	if len(shape.Languages) == 0 {
		return nil, &LoadError{Path: path, Reason: "missing_field", Field: "languages", Detail: "registry contains no languages"}
	}

	reg := &Registry{
		defs:  make([]Definition, 0, len(shape.Languages)),
		byID:  make(map[string]Definition, len(shape.Languages)),
		order: make([]string, 0, len(shape.Languages)),
	}

	for i, def := range shape.Languages {
		if err := validateDefinition(path, i, &def); err != nil {
			return nil, err
		}
		if _, dup := reg.byID[def.ID]; dup {
			return nil, &LoadError{
				Path:       path,
				Reason:     "duplicate_id",
				LanguageID: def.ID,
				Detail:     fmt.Sprintf("language id %q appears more than once", def.ID),
			}
		}
		reg.defs = append(reg.defs, def)
		reg.byID[def.ID] = def
		reg.order = append(reg.order, def.ID)
	}

	return reg, nil
}

// validateDefinition runs the per-entry checks required by Phase 1.
//
// Required fields (REQ A-7.4): id, source_filename, run.command.
// Filename rules (REQ A-21.1): source_filename and binary_filename (when
// present) must be safe basenames.
// Placeholder allowlist (REQ A-22.4): every placeholder in compile.args /
// run.args must be in security.AllowedPlaceholders.
func validateDefinition(path string, index int, def *Definition) error {
	if strings.TrimSpace(def.ID) == "" {
		return &LoadError{
			Path:   path,
			Reason: "missing_field",
			Field:  "id",
			Detail: fmt.Sprintf("entry at index %d missing id", index),
		}
	}
	if strings.TrimSpace(def.SourceFilename) == "" {
		return &LoadError{
			Path:       path,
			Reason:     "missing_field",
			LanguageID: def.ID,
			Field:      "source_filename",
		}
	}
	if err := security.ValidateBasename(def.SourceFilename); err != nil {
		return wrapFilenameError(path, def.ID, "source_filename", err)
	}
	if def.BinaryFilename != "" {
		if err := security.ValidateBasename(def.BinaryFilename); err != nil {
			return wrapFilenameError(path, def.ID, "binary_filename", err)
		}
	}

	if strings.TrimSpace(def.Run.Command) == "" {
		return &LoadError{
			Path:       path,
			Reason:     "missing_field",
			LanguageID: def.ID,
			Field:      "run.command",
		}
	}
	if err := scanArgs(path, def.ID, "run.args", def.Run.Args); err != nil {
		return err
	}

	if def.Compile != nil && strings.TrimSpace(def.Compile.Command) != "" {
		if err := scanArgs(path, def.ID, "compile.args", def.Compile.Args); err != nil {
			return err
		}
	}

	return nil
}

// scanArgs runs security.ScanPlaceholders over each entry in args.
func scanArgs(path, langID, field string, args []string) error {
	for _, a := range args {
		if err := security.ScanPlaceholders(a); err != nil {
			var pe *security.PlaceholderError
			if errors.As(err, &pe) {
				return &LoadError{
					Path:       path,
					Reason:     "unknown_placeholder",
					LanguageID: langID,
					Field:      field,
					Detail:     fmt.Sprintf("entry %q contains unknown placeholder %q", pe.ArgsEntry, pe.Placeholder),
					Err:        err,
				}
			}
			return &LoadError{Path: path, Reason: "unknown_placeholder", LanguageID: langID, Field: field, Detail: err.Error(), Err: err}
		}
	}
	return nil
}

// wrapFilenameError turns a *security.FilenameError into a *LoadError.
func wrapFilenameError(path, langID, field string, err error) error {
	var fe *security.FilenameError
	if errors.As(err, &fe) {
		// Redact the value when it contains control bytes (REQ A-21.2).
		detail := fmt.Sprintf("reason=%s value=%q", fe.Reason, fe.Value)
		if security.HasControlBytes(fe.Value) {
			detail = fmt.Sprintf("reason=%s length=%d", fe.Reason, len(fe.Value))
		}
		return &LoadError{
			Path:       path,
			Reason:     "unsafe_filename",
			LanguageID: langID,
			Field:      field,
			Detail:     detail,
			Err:        err,
		}
	}
	return &LoadError{Path: path, Reason: "unsafe_filename", LanguageID: langID, Field: field, Detail: err.Error(), Err: err}
}

// Get returns the Definition matching id by case-sensitive exact match.
//
// Property 7: side-effect-free, deterministic, idempotent. The boolean
// distinguishes a not-found result from a successful lookup of an entry
// whose zero value happens to match the default Definition struct.
func (r *Registry) Get(id string) (Definition, bool) {
	d, ok := r.byID[id]
	return d, ok
}

// List returns every registered language identifier in file order.
//
// The returned slice is freshly allocated; callers may mutate it.
func (r *Registry) List() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}
