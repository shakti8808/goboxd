package runner_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/thesouldev/goboxd/internal/runner"
)


// memFS is an in-memory ReaperFS implementation used by reaper unit
// tests. It records every RemoveAll call so tests can assert exactly
// which paths were touched.
type memFS struct {
	mu      sync.Mutex
	entries map[string][]memEntry // key: directory path
	mtimes  map[string]time.Time  // key: full file path
	removed []string
	readErr error
	statErr map[string]error
	rmErr   map[string]error
}

type memEntry struct {
	name  string
	isDir bool
}

func (e memEntry) Name() string               { return e.name }
func (e memEntry) IsDir() bool                { return e.isDir }
func (e memEntry) Type() fs.FileMode          { return 0 }
func (e memEntry) Info() (fs.FileInfo, error) { return nil, errors.New("memEntry: Info not used") }

func newMemFS() *memFS {
	return &memFS{
		entries: map[string][]memEntry{},
		mtimes:  map[string]time.Time{},
		statErr: map[string]error{},
		rmErr:   map[string]error{},
	}
}


func (m *memFS) addJobDir(root, name string, mtime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[root] = append(m.entries[root], memEntry{name: name, isDir: true})
	m.mtimes[root+"/"+name] = mtime
}

func (m *memFS) addFile(root, name string, mtime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[root] = append(m.entries[root], memEntry{name: name, isDir: false})
	m.mtimes[root+"/"+name] = mtime
}

func (m *memFS) ReadDir(path string) ([]fs.DirEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readErr != nil {
		return nil, m.readErr
	}
	es := m.entries[path]
	out := make([]fs.DirEntry, len(es))
	for i := range es {
		out[i] = es[i]
	}
	return out, nil
}

// memFileInfo is a minimal fs.FileInfo satisfying just the bits the
// reaper inspects.
type memFileInfo struct {
	name  string
	dir   bool
	mtime time.Time
}

func (i memFileInfo) Name() string       { return i.name }
func (i memFileInfo) Size() int64        { return 0 }
func (i memFileInfo) Mode() fs.FileMode  { return 0 }
func (i memFileInfo) ModTime() time.Time { return i.mtime }
func (i memFileInfo) IsDir() bool        { return i.dir }
func (i memFileInfo) Sys() interface{}   { return nil }

func (m *memFS) Stat(path string) (fs.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := m.statErr[path]; ok {
		return nil, err
	}
	mt, ok := m.mtimes[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return memFileInfo{name: path, dir: true, mtime: mt}, nil
}

func (m *memFS) RemoveAll(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err, ok := m.rmErr[path]; ok {
		return err
	}
	m.removed = append(m.removed, path)
	return nil
}


// recordingReaperObserver captures reap and reap-failure events so
// tests can assert the metric/log surface.
type recordingReaperObserver struct {
	mu       sync.Mutex
	reaped   []reaperReap
	failures []reaperFail
}

type reaperReap struct {
	Path string
	Age  time.Duration
}

type reaperFail struct {
	Path string
	Err  error
}

func (o *recordingReaperObserver) OnReap(path string, age time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reaped = append(o.reaped, reaperReap{path, age})
}

func (o *recordingReaperObserver) OnReapFailure(path string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures = append(o.failures, reaperFail{path, err})
}

// newReaper builds an OrphanReaper with sensible Wave C defaults and
// the supplied filesystem stub.
func newReaper(t *testing.T, root string, ttl time.Duration, fs *memFS, obs runner.ReaperObserver) *runner.OrphanReaper {
	t.Helper()
	r, err := runner.NewOrphanReaper(runner.ReaperConfig{
		Root:     root,
		TTL:      ttl,
		Tick:     ttl / 2,
		Now:      func() time.Time { return time.Time{} }, // unused; tests pass explicit now
		FS:       fs,
		Observer: obs,
	})
	if err != nil {
		t.Fatalf("NewOrphanReaper: %v", err)
	}
	return r
}


// TestReapOnceRemovesAgedJobDirs confirms a job-* directory whose age
// exceeds the TTL is removed exactly once.
func TestReapOnceRemovesAgedJobDirs(t *testing.T) {
	t.Parallel()
	const root = "/sandbox"
	now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	ttl := 10 * time.Minute

	mfs := newMemFS()
	mfs.addJobDir(root, "job-old", now.Add(-30*time.Minute))   // aged 30 min > 10 min
	mfs.addJobDir(root, "job-young", now.Add(-1*time.Minute))  // aged 1 min < 10 min

	obs := &recordingReaperObserver{}
	r := newReaper(t, root, ttl, mfs, obs)

	reaped, err := r.ReapOnce(now)
	if err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped = %d, want 1", reaped)
	}
	if len(mfs.removed) != 1 || mfs.removed[0] != root+"/job-old" {
		t.Errorf("removed paths = %v, want [%s/job-old]", mfs.removed, root)
	}
	if len(obs.reaped) != 1 || obs.reaped[0].Path != root+"/job-old" {
		t.Errorf("observer events = %+v", obs.reaped)
	}
}

// TestReapOnceSkipsNonJobEntries asserts the architecture-spec
// invariant: only job-* directories may be removed.
func TestReapOnceSkipsNonJobEntries(t *testing.T) {
	t.Parallel()
	const root = "/sandbox"
	now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	ttl := 1 * time.Minute

	mfs := newMemFS()
	// Aged regular directory but not a job-* — must not be reaped.
	mfs.addJobDir(root, "important", now.Add(-1*time.Hour))
	// Aged file (not a directory) — must not be reaped.
	mfs.addFile(root, "config.yaml", now.Add(-1*time.Hour))
	// Aged job-* directory — should be reaped.
	mfs.addJobDir(root, "job-orphan", now.Add(-1*time.Hour))

	r := newReaper(t, root, ttl, mfs, &recordingReaperObserver{})
	reaped, err := r.ReapOnce(now)
	if err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped = %d, want 1", reaped)
	}
	if len(mfs.removed) != 1 || mfs.removed[0] != root+"/job-orphan" {
		t.Errorf("removed = %v, want only the job-orphan entry", mfs.removed)
	}
}


// TestReapOnceMissingRootIsNoError confirms a missing sandbox root
// returns (0, nil) — operators may not have created the dir yet.
func TestReapOnceMissingRootIsNoError(t *testing.T) {
	t.Parallel()
	mfs := newMemFS()
	mfs.readErr = fs.ErrNotExist
	r := newReaper(t, "/sandbox", time.Minute, mfs, &recordingReaperObserver{})
	reaped, err := r.ReapOnce(time.Now())
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if reaped != 0 {
		t.Errorf("reaped = %d, want 0", reaped)
	}
}

// TestReapOnceReadDirError surfaces filesystem errors other than NotExist.
func TestReapOnceReadDirError(t *testing.T) {
	t.Parallel()
	mfs := newMemFS()
	mfs.readErr = errors.New("disk on fire")
	r := newReaper(t, "/sandbox", time.Minute, mfs, &recordingReaperObserver{})
	_, err := r.ReapOnce(time.Now())
	if err == nil || !errors.Is(err, mfs.readErr) {
		t.Errorf("err = %v, want disk on fire", err)
	}
}

// TestReapOnceStatFailureSurfacesObserver confirms a per-entry stat
// error is reported but does not abort the scan.
func TestReapOnceStatFailureSurfacesObserver(t *testing.T) {
	t.Parallel()
	const root = "/sandbox"
	now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	mfs := newMemFS()
	mfs.addJobDir(root, "job-aged", now.Add(-1*time.Hour))
	mfs.addJobDir(root, "job-broken", now.Add(-1*time.Hour))
	mfs.statErr[root+"/job-broken"] = errors.New("stat: permission denied")

	obs := &recordingReaperObserver{}
	r := newReaper(t, root, time.Minute, mfs, obs)
	reaped, err := r.ReapOnce(now)
	if err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped = %d, want 1 (broken stat skipped)", reaped)
	}
	if len(obs.failures) != 1 || obs.failures[0].Path != root+"/job-broken" {
		t.Errorf("observer failures = %+v", obs.failures)
	}
}

// TestReapOnceRemoveFailureSurfacesObserver confirms RemoveAll errors
// are reported and the scan continues.
func TestReapOnceRemoveFailureSurfacesObserver(t *testing.T) {
	t.Parallel()
	const root = "/sandbox"
	now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	mfs := newMemFS()
	mfs.addJobDir(root, "job-stuck", now.Add(-1*time.Hour))
	mfs.rmErr[root+"/job-stuck"] = errors.New("rm: busy")

	obs := &recordingReaperObserver{}
	r := newReaper(t, root, time.Minute, mfs, obs)
	reaped, _ := r.ReapOnce(now)
	if reaped != 0 {
		t.Errorf("reaped = %d, want 0 (rm failed)", reaped)
	}
	if len(obs.failures) != 1 {
		t.Errorf("observer failures = %+v, want 1", obs.failures)
	}
}


// TestNewOrphanReaperValidates walks the required-field rejection cases.
func TestNewOrphanReaperValidates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  runner.ReaperConfig
	}{
		{"empty root", runner.ReaperConfig{TTL: time.Minute}},
		{"relative root", runner.ReaperConfig{Root: "sandbox", TTL: time.Minute}},
		{"zero ttl", runner.ReaperConfig{Root: "/sandbox"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runner.NewOrphanReaper(tc.cfg)
			if !errors.Is(err, runner.ErrInvalidReaperConfig) {
				t.Errorf("err = %v, want ErrInvalidReaperConfig", err)
			}
		})
	}
}

// TestStartCancelsOnContext drives Start under a cancelled context and
// asserts it returns promptly.
func TestStartCancelsOnContext(t *testing.T) {
	t.Parallel()
	r, err := runner.NewOrphanReaper(runner.ReaperConfig{
		Root: "/sandbox",
		TTL:  10 * time.Minute,
		FS:   newMemFS(),
	})
	if err != nil {
		t.Fatalf("NewOrphanReaper: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = r.Start(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Start = %v, want context.Canceled", err)
	}
}


// TestStartTicksOnce drives a real reaper tick by setting Tick to a
// short interval and cancelling after one fires. Asserts the per-tick
// path runs (covers tick + Start's loop body).
func TestStartTicksOnce(t *testing.T) {
	t.Parallel()
	const root = "/sandbox"
	now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	mfs := newMemFS()
	mfs.addJobDir(root, "job-aged", now.Add(-1*time.Hour))

	clk := &fakeClock{now: now}
	obs := &recordingReaperObserver{}
	r, err := runner.NewOrphanReaper(runner.ReaperConfig{
		Root:     root,
		TTL:      time.Minute,
		Tick:     time.Millisecond,
		Now:      clk.Now,
		FS:       mfs,
		Observer: obs,
	})
	if err != nil {
		t.Fatalf("NewOrphanReaper: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.Start(ctx)

	if len(obs.reaped) == 0 {
		t.Errorf("expected at least one reap event")
	}
}


// TestOsReaperFSRoundtrip drives the production osReaperFS via a real
// temp directory so its three methods (ReadDir / Stat / RemoveAll) are
// not flagged as dead code by the coverage tool.
func TestOsReaperFSRoundtrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	job := root + "/job-real"
	if err := os.Mkdir(job, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	// Backdate the mtime so ReapOnce reaps it.
	old := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(job, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	r, err := runner.NewOrphanReaper(runner.ReaperConfig{
		Root: root,
		TTL:  time.Minute,
	})
	if err != nil {
		t.Fatalf("NewOrphanReaper: %v", err)
	}
	reaped, err := r.ReapOnce(time.Now())
	if err != nil {
		t.Fatalf("ReapOnce: %v", err)
	}
	if reaped != 1 {
		t.Errorf("reaped = %d, want 1", reaped)
	}
	if _, statErr := os.Stat(job); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("job dir not removed: %v", statErr)
	}
}
