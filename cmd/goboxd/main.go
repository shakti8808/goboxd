// Command goboxd serves the GoboxD HTTP API.
//
// Phase 1 wiring: load configuration, initialise the structured logger,
// initialise the Prometheus metrics registry, load the YAML language
// registry, build the API server with /healthz, /readyz, /info, /metrics
// handlers and a /run stub returning HTTP 503 not_implemented, install
// SIGINT/SIGTERM handlers, and run until shutdown. Worker pool, security
// validator, sandbox runner, and the run-pipeline portion of the metrics
// catalog are added in Phase 2 and Phase 3.
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
	"github.com/thesouldev/goboxd/internal/version"
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

	// Readiness flags. Phase 1 wires three; later phases extend the set
	// with worker_pool_ready and nsjail_present_and_executable.
	var (
		startupCompleted       atomic.Bool
		languageRegistryLoaded atomic.Bool
		shutdownInProgress     atomic.Bool
	)

	reg, err := registry.Load(cfg.LanguageRegistryPath)
	if err != nil {
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

	healthH := handlers.NewHealthHandler(&startupCompleted, &languageRegistryLoaded, &shutdownInProgress)
	infoH := handlers.NewInfoHandler(serviceName, buildVersion, cfg.WorkerPoolSize, languages)
	runH := handlers.NewRunHandler()

	routes := []api.Route{
		{Path: "/healthz", Method: http.MethodGet, Handler: http.HandlerFunc(healthH.Live)},
		{Path: "/readyz", Method: http.MethodGet, Handler: http.HandlerFunc(healthH.Ready)},
		{Path: "/info", Method: http.MethodGet, Handler: http.HandlerFunc(infoH.Info)},
		{Path: "/run", Method: http.MethodPost, Handler: http.HandlerFunc(runH.Run)},
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

	select {
	case sig := <-sigCh:
		log.Info("shutdown_begin",
			"event", "shutdown_begin",
			"signal", sig.String(),
		)
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
