package registry_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/thesouldev/goboxd/internal/registry"
)

// writeTemp writes content under t.TempDir()/file.yaml and returns the
// path. Named so individual tests can pick distinct filenames.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// validRegistry mirrors the architecture spec §12 worked examples.
const validRegistry = `languages:
  - id: py3
    source_filename: main.py
    run:
      command: /usr/bin/python3
      args: ["main.py"]
      limits:
        wall_time_s: 10
        cpu_time_s: 10
        memory_mb: 256
        process_count: 16
        output_size_mb: 4
  - id: cpp
    source_filename: main.cpp
    binary_filename: main
    compile:
      command: /usr/bin/g++
      args: ["-O2", "-std=c++17", "-o", "{{BINARY_FILENAME}}", "{{SOURCE_FILENAME}}"]
      limits:
        wall_time_s: 20
        cpu_time_s: 20
        memory_mb: 512
        process_count: 32
        output_size_mb: 16
    run:
      command: ./main
      args: []
      limits:
        wall_time_s: 10
        cpu_time_s: 10
        memory_mb: 256
        process_count: 16
        output_size_mb: 4
`

// TestLoadHappyPath asserts the worked examples load and surface the
// expected structure on Get/List.
func TestLoadHappyPath(t *testing.T) {
	path := writeTemp(t, "good.yaml", validRegistry)
	reg, err := registry.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := reg.List()
	want := []string{"py3", "cpp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List() = %v, want %v", got, want)
	}

	py, ok := reg.Get("py3")
	if !ok {
		t.Fatal("Get(py3) missing")
	}
	if py.SourceFilename != "main.py" || py.HasCompile() {
		t.Errorf("py3 unexpected: %+v", py)
	}

	cpp, ok := reg.Get("cpp")
	if !ok {
		t.Fatal("Get(cpp) missing")
	}
	if !cpp.HasCompile() || cpp.BinaryFilename != "main" {
		t.Errorf("cpp unexpected: %+v", cpp)
	}

	// Property 7 spot check: idempotent, non-mutating.
	for i := 0; i < 5; i++ {
		other, ok := reg.Get("py3")
		if !ok || !reflect.DeepEqual(other, py) {
			t.Errorf("Get repeated invocation drift: %+v vs %+v", other, py)
		}
	}
	if !reflect.DeepEqual(reg.List(), want) {
		t.Errorf("List() order changed after Get calls")
	}

	if _, ok := reg.Get("Py3"); ok {
		t.Error("Get is not case-sensitive: matched Py3 against py3")
	}
	if _, ok := reg.Get("nosuch"); ok {
		t.Error("Get returned ok for unknown id")
	}
}

// TestLoadFailureCases enumerates every load-time rejection.
func TestLoadFailureCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		body        string
		reason      string
		field       string
		languageID  string
	}{
		{
			name:   "missing file",
			body:   "",
			reason: "io_failed",
		},
		{
			name:   "malformed yaml",
			body:   "languages: [\n",
			reason: "yaml_parse_failed",
		},
		{
			name:   "no languages",
			body:   "languages: []\n",
			reason: "missing_field",
			field:  "languages",
		},
		{
			name: "missing id",
			body: `languages:
  - source_filename: main.py
    run:
      command: /bin/true
      args: []
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
`,
			reason: "missing_field",
			field:  "id",
		},
		{
			name: "missing run command",
			body: `languages:
  - id: py3
    source_filename: main.py
    run:
      command: ""
      args: []
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
`,
			reason:     "missing_field",
			field:      "run.command",
			languageID: "py3",
		},
		{
			name: "duplicate id",
			body: `languages:
  - id: py3
    source_filename: main.py
    run:
      command: /bin/true
      args: []
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
  - id: py3
    source_filename: other.py
    run:
      command: /bin/true
      args: []
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
`,
			reason:     "duplicate_id",
			languageID: "py3",
		},
		{
			name: "unsafe source filename",
			body: `languages:
  - id: py3
    source_filename: ../etc/passwd
    run:
      command: /bin/true
      args: []
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
`,
			reason:     "unsafe_filename",
			field:      "source_filename",
			languageID: "py3",
		},
		{
			name: "unsafe binary filename",
			body: `languages:
  - id: cpp
    source_filename: main.cpp
    binary_filename: /usr/bin/sh
    compile:
      command: /usr/bin/g++
      args: []
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
    run:
      command: ./main
      args: []
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
`,
			reason:     "unsafe_filename",
			field:      "binary_filename",
			languageID: "cpp",
		},
		{
			name: "unknown placeholder in run.args",
			body: `languages:
  - id: py3
    source_filename: main.py
    run:
      command: /usr/bin/python3
      args: ["{{NOPE}}"]
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
`,
			reason:     "unknown_placeholder",
			field:      "run.args",
			languageID: "py3",
		},
		{
			name: "unknown placeholder in compile.args",
			body: `languages:
  - id: cpp
    source_filename: main.cpp
    compile:
      command: /usr/bin/g++
      args: ["{{INJECT}}"]
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
    run:
      command: ./main
      args: []
      limits: {wall_time_s: 1, cpu_time_s: 1, memory_mb: 16, process_count: 1, output_size_mb: 1}
`,
			reason:     "unknown_placeholder",
			field:      "compile.args",
			languageID: "cpp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			if tc.name == "missing file" {
				path = filepath.Join(t.TempDir(), "absent.yaml")
			} else {
				path = writeTemp(t, "case.yaml", tc.body)
			}

			_, err := registry.Load(path)
			if err == nil {
				t.Fatal("expected error")
			}
			var le *registry.LoadError
			if !errors.As(err, &le) {
				t.Fatalf("error is not *LoadError: %v", err)
			}
			if le.Reason != tc.reason {
				t.Errorf("Reason = %q, want %q (full err %v)", le.Reason, tc.reason, err)
			}
			if tc.field != "" && le.Field != tc.field {
				t.Errorf("Field = %q, want %q", le.Field, tc.field)
			}
			if tc.languageID != "" && le.LanguageID != tc.languageID {
				t.Errorf("LanguageID = %q, want %q", le.LanguageID, tc.languageID)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error string missing reason %q: %s", tc.reason, err)
			}
		})
	}
}

// TestDefaultRegistryShipped loads the YAML file shipped at
// configs/language_registry.yaml so a stray edit is caught at CI time.
func TestDefaultRegistryShipped(t *testing.T) {
	// The test runs from the package directory; configs/ is two levels up.
	path := filepath.Join("..", "..", "configs", "language_registry.yaml")
	reg, err := registry.Load(path)
	if err != nil {
		t.Fatalf("Load %s: %v", path, err)
	}
	got := reg.List()
	want := []string{"py3", "cpp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default registry languages = %v, want %v", got, want)
	}
}
