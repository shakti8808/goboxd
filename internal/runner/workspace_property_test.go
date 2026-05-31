package runner_test

import (
	"errors"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"

	"github.com/thesouldev/goboxd/internal/runner"
)

// uuidV4SuffixRE captures the canonical UUIDv4 form (8-4-4-4-12 hex with
// the version-4 nibble pinned to 4 and the variant nibble in {8,9,a,b}).
var uuidV4SuffixRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestPropertyWorkspaceUniqueness — Property 39.
//
// For arbitrary pairs of workspace allocations under the same root the
// resulting paths are distinct, both contain a UUIDv4 segment, and the
// UUIDv4 segment matches the canonical regex (≥128 bits of entropy
// inside the segment). Rapid runs ≥100 iterations per invocation, which
// satisfies Req I-4.2.
func TestPropertyWorkspaceUniqueness(t *testing.T) {
	root := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		idA := uuid.New()
		idB := uuid.New()
		// Reject the (astronomically unlikely) collision so the property
		// expresses what it intends to express.
		if idA == idB {
			t.Skip("uuid collision in generator")
		}

		wsA, err := runner.AllocateWorkspace(root, idA)
		if err != nil {
			t.Fatalf("AllocateWorkspace A: %v", err)
		}
		wsB, err := runner.AllocateWorkspace(root, idB)
		if err != nil {
			_ = wsA.Cleanup()
			t.Fatalf("AllocateWorkspace B: %v", err)
		}
		defer func() {
			_ = wsA.Cleanup()
			_ = wsB.Cleanup()
		}()

		if wsA.Path() == wsB.Path() {
			t.Fatalf("workspace paths collide: A=%q B=%q", wsA.Path(), wsB.Path())
		}

		for _, ws := range []*runner.Workspace{wsA, wsB} {
			base := filepath.Base(ws.Path())
			if len(base) <= len("job-") || base[:4] != "job-" {
				t.Fatalf("path %q does not match job-<UUIDv4> shape", ws.Path())
			}
			if !uuidV4SuffixRE.MatchString(base[4:]) {
				t.Fatalf("path %q UUID segment %q is not canonical UUIDv4", ws.Path(), base[4:])
			}
		}
	})
}

// TestPropertyWorkspaceVerifyInert — Property 41.
//
// For arbitrary suffix strings, Verify returns nil iff the input equals
// the workspace's recorded path (after filepath.Clean) and otherwise
// returns *WorkspaceIsolationError. Foreign paths are synthesised by
// appending a non-empty random suffix so filepath.Clean cannot collapse
// them back to the recorded path.
func TestPropertyWorkspaceVerifyInert(t *testing.T) {
	root := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		ws, err := runner.AllocateWorkspace(root, uuid.New())
		if err != nil {
			t.Fatalf("AllocateWorkspace: %v", err)
		}
		defer func() { _ = ws.Cleanup() }()

		obs := &recordingObserver{}

		// Case A: own path always passes.
		if err := ws.Verify(ws.Path(), obs); err != nil {
			t.Fatalf("Verify(own path) = %v", err)
		}

		// Case B: arbitrary path that is NOT equal to ws.Path() must
		// fail. The suffix is `/` + lowercase letters so we never hit
		// a filepath.Clean equivalence with the recorded path.
		suffix := rapid.StringMatching(`/[a-z]{1,8}`).Draw(t, "suffix")
		foreign := ws.Path() + suffix
		err = ws.Verify(foreign, obs)
		if err == nil {
			t.Fatalf("Verify(%q) returned nil for foreign path", foreign)
		}
		var viol *runner.WorkspaceIsolationError
		if !errors.As(err, &viol) {
			t.Fatalf("error is not *WorkspaceIsolationError: %v", err)
		}
		if viol.Expected != ws.Path() {
			t.Fatalf("Expected = %q, want %q", viol.Expected, ws.Path())
		}
	})
}
