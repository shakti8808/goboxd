package runner_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/thesouldev/goboxd/internal/runner"
)

// recordingObserver captures every isolation violation the runner reports
// so tests can assert metric/log emission shape without importing
// internal/metrics or internal/log.
type recordingObserver struct{ events []runner.WorkspaceIsolationError }

func (r *recordingObserver) OnIsolationViolation(e runner.WorkspaceIsolationError) {
	r.events = append(r.events, e)
}

// uuidv4PathSuffixRE matches the canonical UUIDv4 trailing segment we
// expect in every job directory name.
var uuidv4PathSuffixRE = regexp.MustCompile(`/job-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestAllocateWorkspaceHappyPath drives the full lifecycle: allocate →
// inspect mode → write a sentinel → verify ownership → cleanup → cleanup
// idempotent → directory absent.
func TestAllocateWorkspaceHappyPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	id := uuid.New()
	ws, err := runner.AllocateWorkspace(root, id)
	if err != nil {
		t.Fatalf("AllocateWorkspace: %v", err)
	}
	if ws.RequestID() != id {
		t.Errorf("RequestID() = %s, want %s", ws.RequestID(), id)
	}
	if !uuidv4PathSuffixRE.MatchString(ws.Path()) {
		t.Errorf("Path() = %q does not end with /job-<UUIDv4>", ws.Path())
	}
	info, err := os.Stat(ws.Path())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("Path() is not a directory: %v", info.Mode())
	}
	// Permission check is POSIX-specific; skip on Windows runners.
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("workspace mode = %o, want 0o700", got)
		}
	}

	// Write a sentinel inside the workspace to confirm we own it.
	sentinel := filepath.Join(ws.Path(), "ok.txt")
	if err := os.WriteFile(sentinel, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Ownership invariant: Verify returns nil for the recorded path.
	obs := &recordingObserver{}
	if err := ws.Verify(ws.Path(), obs); err != nil {
		t.Errorf("Verify(own path) = %v, want nil", err)
	}
	if len(obs.events) != 0 {
		t.Errorf("observer fired on legitimate path: %+v", obs.events)
	}

	// Cleanup removes the tree.
	if err := ws.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(ws.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("workspace not removed: %v", err)
	}

	// Cleanup is idempotent (architecture REQ A-24.4 deferred path).
	if err := ws.Cleanup(); err != nil {
		t.Errorf("Cleanup (idempotent) = %v, want nil", err)
	}
}

// TestAllocateWorkspaceCreatesMissingRoot confirms AllocateWorkspace
// transparently creates the root if it doesn't already exist. Operators
// pre-create the root with documented permissions; this fallback exists
// for fresh installs and tests.
func TestAllocateWorkspaceCreatesMissingRoot(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "nested", "sandbox")
	ws, err := runner.AllocateWorkspace(root, uuid.New())
	if err != nil {
		t.Fatalf("AllocateWorkspace: %v", err)
	}
	defer func() { _ = ws.Cleanup() }()
	if !strings.HasPrefix(ws.Path(), root) {
		t.Errorf("Path() = %q does not live under %q", ws.Path(), root)
	}
}

// TestAllocateWorkspaceRejectsBadRoot covers every documented invalid
// root case.
func TestAllocateWorkspaceRejectsBadRoot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		root string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"relative", "sandbox"},
		{"traversal", "/var/lib/goboxd/../../etc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runner.AllocateWorkspace(tc.root, uuid.New())
			if err == nil {
				t.Fatalf("expected error for root %q", tc.root)
			}
			if !errors.Is(err, runner.ErrInvalidWorkspaceRoot) {
				t.Errorf("errors.Is(err, ErrInvalidWorkspaceRoot) = false; got %v", err)
			}
		})
	}
}

// TestAllocateWorkspaceRejectsZeroUUID asserts the zero-value request id
// is refused — Property 39's ≥128-bit-entropy claim relies on the id
// being a real UUIDv4.
func TestAllocateWorkspaceRejectsZeroUUID(t *testing.T) {
	t.Parallel()
	_, err := runner.AllocateWorkspace(t.TempDir(), uuid.Nil)
	if !errors.Is(err, runner.ErrInvalidRequestID) {
		t.Errorf("errors.Is(err, ErrInvalidRequestID) = false; got %v", err)
	}
}

// TestVerifyForeignPath asserts the architecture §11 ownership
// invariant: Verify rejects any path the workspace did not allocate
// and notifies the observer with the expected/received pair.
func TestVerifyForeignPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws, err := runner.AllocateWorkspace(root, uuid.New())
	if err != nil {
		t.Fatalf("AllocateWorkspace: %v", err)
	}
	defer func() { _ = ws.Cleanup() }()

	obs := &recordingObserver{}
	foreign := filepath.Join(root, "job-FOREIGN")
	err = ws.Verify(foreign, obs)
	if err == nil {
		t.Fatal("expected error for foreign path")
	}
	if !errors.Is(err, runner.ErrWorkspaceIsolationViolation) {
		t.Errorf("errors.Is(err, ErrWorkspaceIsolationViolation) = false; got %v", err)
	}
	var viol *runner.WorkspaceIsolationError
	if !errors.As(err, &viol) {
		t.Fatalf("error is not *WorkspaceIsolationError: %v", err)
	}
	if viol.Expected != ws.Path() {
		t.Errorf("Expected = %q, want %q", viol.Expected, ws.Path())
	}
	if viol.Received != foreign {
		t.Errorf("Received = %q, want %q", viol.Received, foreign)
	}
	if viol.RequestID != ws.RequestID() {
		t.Errorf("RequestID = %s, want %s", viol.RequestID, ws.RequestID())
	}
	if len(obs.events) != 1 {
		t.Fatalf("observer fired %d times, want 1", len(obs.events))
	}
}

// TestVerifyHandlesNilObserverAndWorkspace confirms the defensive guards.
func TestVerifyHandlesNilObserverAndWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws, err := runner.AllocateWorkspace(root, uuid.New())
	if err != nil {
		t.Fatalf("AllocateWorkspace: %v", err)
	}
	defer func() { _ = ws.Cleanup() }()

	// nil observer must not panic.
	if err := ws.Verify(ws.Path(), nil); err != nil {
		t.Errorf("Verify(nil obs, own path) = %v, want nil", err)
	}

	// nil workspace returns isolation error rather than nil-deref.
	var dead *runner.Workspace
	if err := dead.Verify("/anywhere", nil); !errors.Is(err, runner.ErrWorkspaceIsolationViolation) {
		t.Errorf("Verify on nil workspace = %v, want ErrWorkspaceIsolationViolation", err)
	}
}

// TestCleanupNilSafe asserts Cleanup tolerates a nil receiver and an
// already-removed directory.
func TestCleanupNilSafe(t *testing.T) {
	t.Parallel()
	var dead *runner.Workspace
	if err := dead.Cleanup(); err != nil {
		t.Errorf("Cleanup on nil = %v, want nil", err)
	}
}

// TestCleanupRemovesContents confirms RemoveAll is invoked (and not just
// os.Remove on the empty dir).
func TestCleanupRemovesContents(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws, err := runner.AllocateWorkspace(root, uuid.New())
	if err != nil {
		t.Fatalf("AllocateWorkspace: %v", err)
	}
	for _, name := range []string{"a", "b/c", "b/d"} {
		full := filepath.Join(ws.Path(), name)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(name), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	if err := ws.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(ws.Path()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("workspace tree still exists: %v", err)
	}
}
