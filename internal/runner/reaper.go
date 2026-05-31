package runner

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// jobPrefix is the basename prefix every workspace AllocateWorkspace
// creates. The reaper reaps only entries whose basename begins with
// this string; anything else under the sandbox root is left alone
// (architecture spec §"avoid deleting unrelated directories").
const jobPrefix = "job-"

// ReaperFS is the filesystem seam OrphanReaper depends on. The default
// production implementation (osReaperFS) calls os.ReadDir, os.Stat, and
// os.RemoveAll. Tests substitute an in-memory implementation so the
// reaper can be exercised deterministically without sleeping.
type ReaperFS interface {
	ReadDir(root string) ([]fs.DirEntry, error)
	Stat(path string) (fs.FileInfo, error)
	RemoveAll(path string) error
}

// ReaperObserver is the runner-local hook the run handler in Wave D
// wires to internal/metrics and internal/log. The reaper does not
// import either package directly; tests inject a recording stub.
type ReaperObserver interface {
	OnReap(path string, age time.Duration)
	OnReapFailure(path string, err error)
}

// nopReaperObserver is the default ReaperObserver.
type nopReaperObserver struct{}

func (nopReaperObserver) OnReap(string, time.Duration) {}
func (nopReaperObserver) OnReapFailure(string, error)  {}

// ReaperConfig is the immutable configuration OrphanReaper reads at
// construction time.
type ReaperConfig struct {
	// Root is the absolute sandbox root directory the reaper scans.
	// Required.
	Root string

	// TTL is the minimum age a job-* directory must reach before it
	// is eligible for removal. Architecture spec §"SANDBOX_ORPHAN_TTL_S"
	// documents the default of 600 s; Wave D's run handler reads the
	// config key and supplies it here.
	TTL time.Duration

	// Tick is the wall-clock interval between reaper passes. Defaults
	// to TTL/2 with a floor of 30 s and a ceiling of 5 min so the
	// reaper sleeps reasonably regardless of TTL.
	Tick time.Duration

	// Now defaults to time.Now; tests pin a clock.
	Now func() time.Time

	// FS defaults to osReaperFS{}; tests inject an in-memory
	// implementation.
	FS ReaperFS

	// Observer defaults to nopReaperObserver{}; the run handler in
	// Wave D wires real metrics/log hooks here.
	Observer ReaperObserver
}

// ErrInvalidReaperConfig is returned by NewOrphanReaper when the
// configuration fails validation.
var ErrInvalidReaperConfig = errors.New("runner: invalid reaper config")

// OrphanReaper periodically scans a sandbox root and removes job-*
// directories whose age exceeds TTL.
//
// One OrphanReaper is shared across the process and started once at
// startup; per-tick state is local to ReapOnce. The reaper does not
// hold any state about the workspaces it removes — every tick is a
// stateless directory scan, so a workspace newly created between two
// ticks is naturally protected by its mtime.
type OrphanReaper struct {
	cfg ReaperConfig
}

// NewOrphanReaper builds an OrphanReaper from cfg, applying defaults
// for optional fields and validating required ones.
func NewOrphanReaper(cfg ReaperConfig) (*OrphanReaper, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, fmt.Errorf("%w: Root is empty", ErrInvalidReaperConfig)
	}
	if !filepath.IsAbs(cfg.Root) {
		return nil, fmt.Errorf("%w: Root %q is not absolute", ErrInvalidReaperConfig, cfg.Root)
	}
	if cfg.TTL <= 0 {
		return nil, fmt.Errorf("%w: TTL must be > 0", ErrInvalidReaperConfig)
	}
	if cfg.Tick <= 0 {
		cfg.Tick = cfg.TTL / 2
		if cfg.Tick < 30*time.Second {
			cfg.Tick = 30 * time.Second
		}
		if cfg.Tick > 5*time.Minute {
			cfg.Tick = 5 * time.Minute
		}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.FS == nil {
		cfg.FS = osReaperFS{}
	}
	if cfg.Observer == nil {
		cfg.Observer = nopReaperObserver{}
	}
	return &OrphanReaper{cfg: cfg}, nil
}

// Start runs the reaper until ctx is cancelled. It blocks the calling
// goroutine; production wires it as `go reaper.Start(ctx)` so the main
// goroutine can continue with its own work.
//
// Start performs one immediate ReapOnce pass before entering the tick
// loop so a freshly-started service collects orphans from a previous
// crash without waiting a full Tick interval.
func (r *OrphanReaper) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.tick()
	t := time.NewTicker(r.cfg.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			r.tick()
		}
	}
}

// tick is the per-iteration body of Start. It reaps once, swallowing
// any error so a transient filesystem hiccup does not stop the
// goroutine; failures surface via the observer.
func (r *OrphanReaper) tick() {
	_, _ = r.ReapOnce(r.cfg.Now())
}

// ReapOnce scans the sandbox root and removes every job-* directory
// whose age (now - mtime) exceeds the configured TTL. Returns the
// count of directories removed and the first scan-level error
// encountered, if any. Per-entry remove errors do not abort the scan;
// they are surfaced via Observer.OnReapFailure.
func (r *OrphanReaper) ReapOnce(now time.Time) (int, error) {
	entries, err := r.cfg.FS.ReadDir(r.cfg.Root)
	if err != nil {
		// A missing root is not an error: the operator may not have
		// created it yet. Anything else is surfaced.
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("runner: reaper read %s: %w", r.cfg.Root, err)
	}

	reaped := 0
	for _, entry := range entries {
		// Skip files; reaper only touches directories.
		if !entry.IsDir() {
			continue
		}
		// Skip entries that are not job-* — architecture spec
		// requires the reaper avoid deleting unrelated directories.
		name := entry.Name()
		if !strings.HasPrefix(name, jobPrefix) {
			continue
		}
		full := filepath.Join(r.cfg.Root, name)
		info, statErr := r.cfg.FS.Stat(full)
		if statErr != nil {
			r.cfg.Observer.OnReapFailure(full, statErr)
			continue
		}
		age := now.Sub(info.ModTime())
		if age <= r.cfg.TTL {
			continue
		}
		if rmErr := r.cfg.FS.RemoveAll(full); rmErr != nil {
			r.cfg.Observer.OnReapFailure(full, rmErr)
			continue
		}
		r.cfg.Observer.OnReap(full, age)
		reaped++
	}
	return reaped, nil
}

// osReaperFS is the production ReaperFS backed by the standard library.
type osReaperFS struct{}

func (osReaperFS) ReadDir(root string) ([]fs.DirEntry, error) { return os.ReadDir(root) }
func (osReaperFS) Stat(path string) (fs.FileInfo, error)      { return os.Stat(path) }
func (osReaperFS) RemoveAll(path string) error                { return os.RemoveAll(path) }
