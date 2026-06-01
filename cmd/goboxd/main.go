// Command goboxd serves the GoboxD HTTP API.
//
// Phase 2 wiring: load configuration, initialise the structured logger,
// initialise the Prometheus metrics registry, load the YAML language
// registry, build the API server with /healthz, /readyz, /info, /metrics,
// and /run handlers. Worker pool, security validator, sandbox runner, and
// orphan reaper are wired into main.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/thesouldev/goboxd/internal/api"
	"github.com/thesouldev/goboxd/internal/api/handlers"
	"github.com/thesouldev/goboxd/internal/config"
	goboxlog "github.com/thesouldev/goboxd/internal/log"
	"github.com/thesouldev/goboxd/internal/metrics"
	"github.com/thesouldev/goboxd/internal/registry"
	"github.com/thesouldev/goboxd/internal/runner"
	"github.com/thesouldev/goboxd/internal/security"
	"github.com/thesouldev/goboxd/internal/version"
	"github.com/thesouldev/goboxd/internal/worker"
)

// shutdownGrace is the extra time granted to in-flight HTTP requests
// after the configured worker-pool drain timeout elapses, so a final
// response can be flushed to disk and to the wire.
const shutdownGrace = 5 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "goboxd: fatal: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable body of main.
//
// It returns a non-nil error on startup failure or graceful-shutdown
// failure; main translates the error into an exit code.
func run() error {
	cfg, err := config.Load(nil)
	if err != nil {
		return err
	}

	level, err := goboxlog.ParseLevel(cfg.LogLevel)
	if err != nil {
		// Should not happen — config.Load already validated the value
		// against the same closed set — but guard explicitly so the
		// error message names LOG_LEVEL.
		return &config.InvalidKeyError{Key: "LOG_LEVEL", Reason: err.Error()}
	}
	log := goboxlog.New(os.Stdout, level)

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = version.ServiceName
	}
	buildVersion := cfg.BuildVersion
	if buildVersion == "" {
		buildVersion = version.Version
	}

	log.Info("startup_begin",
		"event", "startup_begin",
		"service", serviceName,
		"version", buildVersion,
		"api_bind_addr", cfg.APIBindAddr,
		"language_registry_path", cfg.LanguageRegistryPath,
		"worker_pool_size", cfg.WorkerPoolSize,
		"worker_pool_queue_len", cfg.WorkerPoolQueueLen,
		"worker_pool_drain_timeout_s", cfg.WorkerPoolDrainTimeoutS,
		"log_level", cfg.LogLevel,
	)

	// Metrics registry is created early so the build_info gauge is
	// observable from the moment /metrics is wired below.
	mc := metrics.New(serviceName, buildVersion)

	// Readiness flags.
	var (
		startupCompleted       atomic.Bool
		languageRegistryLoaded atomic.Bool
		shutdownInProgress     atomic.Bool
		workerPoolReady        atomic.Bool
		nsjailPresent          atomic.Bool
	)

	reg, err := registry.Load(cfg.LanguageRegistryPath)
	if err != nil {
		// Inspect for unsafe_filename and unknown_placeholder to log them and increment metrics (REQ-21.2, REQ-22.4)
		var le *registry.LoadError
		if errors.As(err, &le) {
			if le.Reason == "unsafe_filename" {
				mc.IncUnsafeFilename("registry_load")
				var fe *security.FilenameError
				if errors.As(le.Err, &fe) {
					val := fe.Value
					if security.HasControlBytes(val) {
						val = fmt.Sprintf("length=%d", len(fe.Value))
					}
					log.Error("unsafe_filename",
						"event", "unsafe_filename",
						"field", le.Field,
						"language_id", le.LanguageID,
						"value", val,
						"reason", fe.Reason,
					)
				}
			} else if le.Reason == "unknown_placeholder" {
				mc.IncUnknownPlaceholder("registry_load")
				var pe *security.PlaceholderError
				if errors.As(le.Err, &pe) {
					log.Error("unknown_placeholder",
						"event", "unknown_placeholder",
						"language_id", le.LanguageID,
						"args_entry", pe.ArgsEntry,
						"placeholder", pe.Placeholder,
						"reason", "unknown_placeholder",
					)
				}
			}
		}

		log.Error("registry_load_failed",
			"event", "registry_load_failed",
			"path", cfg.LanguageRegistryPath,
			"error", err.Error(),
		)
		return err
	}
	languages := reg.List()
	languageRegistryLoaded.Store(true)
	log.Info("registry_loaded",
		"event", "registry_loaded",
		"path", cfg.LanguageRegistryPath,
		"languages", languages,
	)

	// Validate startup prerequisites.
	if err := validatePrereqs(cfg, mc, log); err != nil {
		return err
	}
	nsjailPresent.Store(true)

	// Map config.BindMount to runner.BindMount
	runnerMounts := make([]runner.BindMount, len(cfg.SandboxRoMounts))
	for i, m := range cfg.SandboxRoMounts {
		runnerMounts[i] = runner.BindMount{
			HostPath:  m.HostPath,
			GuestPath: m.GuestPath,
			ReadWrite: false,
		}
	}

	// Instantiate SandboxRunner
	runCfg := runner.SandboxConfig{
		NsJailPath:              cfg.NsJailPath,
		SandboxRoot:             cfg.SandboxRoot,
		SeccompPolicy:           cfg.SeccompPolicy,
		LaunchTimeout:           5 * time.Second, // Hardcoded 5s per REQ-10.7
		BindMounts:              runnerMounts,
		EnvAllowlist:            nil,
		StdoutCaptureLimitBytes: cfg.StdoutCaptureLimitBytes,
		StderrCaptureLimitBytes: cfg.StderrCaptureLimitBytes,
		Observer:                sandboxObserver{logger: log, metrics: mc},
		WorkspaceObs:            workspaceObserver{logger: log, metrics: mc},
	}
	sandboxRunner, err := runner.New(runCfg)
	if err != nil {
		return fmt.Errorf("initialize sandbox runner: %w", err)
	}

	// Instantiate and start OrphanReaper
	reaperCfg := runner.ReaperConfig{
		Root:     cfg.SandboxRoot,
		TTL:      cfg.SandboxOrphanTTL,
		Observer: reaperObserver{logger: log, metrics: mc},
	}
	reaper, err := runner.NewOrphanReaper(reaperCfg)
	if err != nil {
		return fmt.Errorf("initialize orphan reaper: %w", err)
	}

	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	defer reaperCancel()
	go func() {
		if err := reaper.Start(reaperCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("reaper_failed", "error", err.Error())
		}
	}()

	// Instantiate and start WorkerPool
	poolCfg := worker.Config{
		Workers:      cfg.WorkerPoolSize,
		QueueLen:     cfg.WorkerPoolQueueLen,
		DrainTimeout: time.Duration(cfg.WorkerPoolDrainTimeoutS) * time.Second,
		Runner:       sandboxRunner,
		Observer:     poolObserver{metrics: mc},
	}
	pool, err := worker.New(poolCfg)
	if err != nil {
		return fmt.Errorf("initialize worker pool: %w", err)
	}
	workerPoolReady.Store(true)

	// Instantiate FullRunHandler
	runDeps := handlers.RunDeps{
		Logger:         log,
		Validator:      validatorAdapter{},
		Registry:       reg,
		Pool:           pool,
		Metrics:        mc,
		SizeBounds: security.SizeBounds{
			MaxSourceSizeBytes: cfg.MaxSourceSizeBytes,
			MaxStdinSizeBytes:  cfg.MaxStdinSizeBytes,
		},
		CeilingsLookup: validatorCeilingsAdapter{cfg: cfg},
	}
	runH, err := handlers.NewFullRunHandler(runDeps)
	if err != nil {
		return fmt.Errorf("initialize run handler: %w", err)
	}

	healthH := handlers.NewHealthHandler(
		&startupCompleted,
		&languageRegistryLoaded,
		&shutdownInProgress,
		&workerPoolReady,
		&nsjailPresent,
		cfg.NsJailPath,
	)
	infoH := handlers.NewInfoHandler(serviceName, buildVersion, cfg.WorkerPoolSize, languages)

	routes := []api.Route{
		{Path: "/healthz", Method: http.MethodGet, Handler: http.HandlerFunc(healthH.Live)},
		{Path: "/readyz", Method: http.MethodGet, Handler: http.HandlerFunc(healthH.Ready)},
		{Path: "/info", Method: http.MethodGet, Handler: http.HandlerFunc(infoH.Info)},
		{Path: "/run", Method: http.MethodPost, Handler: http.HandlerFunc(runH.ServeHTTP)},
		{Path: "/metrics", Method: http.MethodGet, Handler: mc.Handler()},
	}

	server := api.New(cfg.APIBindAddr, routes)
	if err := server.Listen(); err != nil {
		log.Error("listener_bind_failed",
			"event", "listener_bind_failed",
			"addr", cfg.APIBindAddr,
			"error", err.Error(),
		)
		return fmt.Errorf("bind %s: %w", cfg.APIBindAddr, err)
	}
	startupCompleted.Store(true)
	log.Info("listener_open",
		"event", "listener_open",
		"addr", server.Addr(),
	)

	// Run the server in a background goroutine so the main goroutine can
	// wait on either an OS signal or a server error, whichever arrives
	// first.
	errCh := make(chan error, 1)
	go func() {
		if err := server.Serve(); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var sig os.Signal
	select {
	case sig = <-sigCh:
		shutdownInProgress.Store(true)
	case err := <-errCh:
		if err != nil {
			log.Error("serve_failed",
				"event", "serve_failed",
				"error", err.Error(),
			)
			return err
		}
		// Server returned cleanly without our asking. Treat as
		// successful shutdown.
		return nil
	}

	// Graceful shutdown sequence
	reaperCancel()
	drained, cancelled := pool.Drain(time.Duration(cfg.WorkerPoolDrainTimeoutS) * time.Second)

	log.Info("shutdown_begin",
		"event", "shutdown_begin",
		"signal", sig.String(),
		"drained_count", drained,
		"cancelled_count", cancelled,
	)

	// Drain timeout from configuration plus a small grace window for
	// final response flushes.
	drain := time.Duration(cfg.WorkerPoolDrainTimeoutS)*time.Second + shutdownGrace
	ctx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error("shutdown_failed",
			"event", "shutdown_failed",
			"error", err.Error(),
		)
		// Make sure the Serve goroutine exits before main returns.
		<-errCh
		return err
	}

	// Wait for the Serve goroutine to drain.
	if serveErr := <-errCh; serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}

	log.Info("shutdown_complete",
		"event", "shutdown_complete",
	)
	return nil
}

// isNsJailExecutable checks if a file exists and is executable.
func isNsJailExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() && (info.Mode().Perm()&0111 != 0)
}
