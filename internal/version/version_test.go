package version_test

import (
	"strings"
	"testing"

	"github.com/thesouldev/goboxd/internal/version"
)

// TestDefaults asserts the unmodified-source defaults so a stray edit that
// blanks one of them is caught at CI time. The architecture spec mandates
// non-empty values for both /info fields (REQ A-6.1).
func TestDefaults(t *testing.T) {
	if strings.TrimSpace(version.Version) == "" {
		t.Errorf("Version default must be non-empty, got %q", version.Version)
	}
	if strings.TrimSpace(version.ServiceName) == "" {
		t.Errorf("ServiceName default must be non-empty, got %q", version.ServiceName)
	}
}

// TestOverridable confirms the variables are mutable so a -ldflags -X
// override at build time will work. The package treats them as build-time
// constants but they are package-level vars precisely so the linker can
// rewrite them.
func TestOverridable(t *testing.T) {
	origVersion := version.Version
	origService := version.ServiceName
	t.Cleanup(func() {
		version.Version = origVersion
		version.ServiceName = origService
	})

	version.Version = "v9.9.9-test"
	version.ServiceName = "goboxd-test"
	if version.Version != "v9.9.9-test" {
		t.Errorf("Version override failed: got %q", version.Version)
	}
	if version.ServiceName != "goboxd-test" {
		t.Errorf("ServiceName override failed: got %q", version.ServiceName)
	}
}
