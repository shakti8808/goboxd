// Package config loads and validates GoboxD's runtime configuration.
//
// Configuration is sourced from environment variables with the documented
// defaults from the architecture spec §15. Invalid or out-of-range values
// cause Load to return an *InvalidKeyError naming the offending key
// (REQ A-13.6); callers report the error and exit non-zero before opening
// the HTTP listener (REQ A-25.5, REQ A-25.6).
//
// Phase 1 covers the keys required by /healthz, /readyz, /info, and the
// graceful-shutdown path. Sandbox, registry, and metrics keys are added in
// later phases; their absence here is intentional and the implementation
// design (`Package Implementation Order`) records the schedule.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Defaults documented in the architecture spec §15.
const (
	defaultAPIBindAddr             = "0.0.0.0:8080"
	defaultMaxRequestBodyBytes     = 1 << 20 // 1 MiB
	defaultLogLevel                = "INFO"
	defaultWorkerPoolSize          = 4
	defaultWorkerPoolQueueLen      = 64
	defaultWorkerPoolDrainTimeoutS = 30
	defaultLanguageRegistryPath    = "/etc/goboxd/language_registry.yaml"
)

// Documented ranges from architecture spec §9 and §15.
const (
	minWorkerPoolSize          = 1
	maxWorkerPoolSize          = 1024
	minWorkerPoolQueueLen      = 0
	maxWorkerPoolQueueLen      = 10000
	minWorkerPoolDrainTimeoutS = 0
	maxWorkerPoolDrainTimeoutS = 3600
	minMaxRequestBodyBytes     = 1
	maxMaxRequestBodyBytes     = 10 * (1 << 20) // 10 MiB
)

// Config is an immutable snapshot of resolved configuration.
//
// Once Load returns successfully the struct is treated as constant for the
// lifetime of the process. Callers MUST NOT mutate fields.
type Config struct {
	// APIBindAddr is the TCP address the HTTP listener binds to.
	APIBindAddr string

	// MaxRequestBodyBytes is the upper bound on POST /run request bodies.
	// Phase 1 records the value but does not enforce it; the cap is wired
	// into the run pipeline in Phase 2.
	MaxRequestBodyBytes int

	// LogLevel is the validated log level string ("DEBUG", "INFO", "WARN",
	// or "ERROR"). Translating to slog.Level happens in main.
	LogLevel string

	// ServiceName overrides the compile-time service name when set.
	// Empty means "fall back to version.ServiceName".
	ServiceName string

	// BuildVersion overrides the compile-time build version when set.
	// Empty means "fall back to version.Version".
	BuildVersion string

	// WorkerPoolSize is the maximum concurrent jobs the pool will run.
	WorkerPoolSize int

	// WorkerPoolQueueLen is the bounded FIFO queue length.
	WorkerPoolQueueLen int

	// WorkerPoolDrainTimeoutS is the graceful-shutdown drain timeout in
	// seconds (0 means "cancel in-flight immediately").
	WorkerPoolDrainTimeoutS int

	// LanguageRegistryPath is the filesystem path to the YAML language
	// registry. Default per architecture spec §15.
	LanguageRegistryPath string
}

// Lookup mirrors os.LookupEnv: it returns the value and a presence flag.
//
// Tests inject a custom Lookup to drive Load deterministically; production
// code passes nil to use the real process environment.
type Lookup func(key string) (string, bool)

// InvalidKeyError reports a configuration value that failed validation.
type InvalidKeyError struct {
	// Key is the environment variable name responsible for the failure.
	Key string

	// Reason is a short human-readable description of why the value was
	// rejected (e.g. "not an integer", "value 9999 outside permitted
	// range [1, 1024]").
	Reason string
}

// Error implements the error interface.
func (e *InvalidKeyError) Error() string {
	return fmt.Sprintf("config: invalid value for %s: %s", e.Key, e.Reason)
}

// Load resolves the configuration snapshot from environment variables.
//
// Pass nil to read os.Environ; pass a custom Lookup for tests. On any
// validation failure Load returns an *InvalidKeyError naming the offending
// key.
func Load(env Lookup) (*Config, error) {
	if env == nil {
		env = osLookup
	}

	c := &Config{
		APIBindAddr:          stringOr(env, "API_BIND_ADDR", defaultAPIBindAddr),
		LogLevel:             stringOr(env, "LOG_LEVEL", defaultLogLevel),
		ServiceName:          stringOr(env, "SERVICE_NAME", ""),
		BuildVersion:         stringOr(env, "BUILD_VERSION", ""),
		LanguageRegistryPath: stringOr(env, "LANGUAGE_REGISTRY_PATH", defaultLanguageRegistryPath),
	}

	if err := validateLogLevel(c.LogLevel); err != nil {
		return nil, err
	}

	var err error
	c.MaxRequestBodyBytes, err = intInRange(env,
		"MAX_REQUEST_BODY_BYTES", defaultMaxRequestBodyBytes,
		minMaxRequestBodyBytes, maxMaxRequestBodyBytes)
	if err != nil {
		return nil, err
	}

	c.WorkerPoolSize, err = intInRange(env,
		"WORKER_POOL_SIZE", defaultWorkerPoolSize,
		minWorkerPoolSize, maxWorkerPoolSize)
	if err != nil {
		return nil, err
	}

	c.WorkerPoolQueueLen, err = intInRange(env,
		"WORKER_POOL_QUEUE_LEN", defaultWorkerPoolQueueLen,
		minWorkerPoolQueueLen, maxWorkerPoolQueueLen)
	if err != nil {
		return nil, err
	}

	c.WorkerPoolDrainTimeoutS, err = intInRange(env,
		"WORKER_POOL_DRAIN_TIMEOUT_S", defaultWorkerPoolDrainTimeoutS,
		minWorkerPoolDrainTimeoutS, maxWorkerPoolDrainTimeoutS)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// osLookup is the production Lookup, reading os.LookupEnv.
func osLookup(key string) (string, bool) { return os.LookupEnv(key) }

// stringOr reads a string env var or returns def when absent or empty.
func stringOr(env Lookup, key, def string) string {
	if v, ok := env(key); ok && v != "" {
		return v
	}
	return def
}

// intInRange parses an integer env var and validates [min, max] inclusive.
func intInRange(env Lookup, key string, def, min, max int) (int, error) {
	raw, ok := env(key)
	if !ok || raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, &InvalidKeyError{Key: key, Reason: fmt.Sprintf("not an integer: %v", err)}
	}
	if n < min || n > max {
		return 0, &InvalidKeyError{Key: key, Reason: fmt.Sprintf("value %d outside permitted range [%d, %d]", n, min, max)}
	}
	return n, nil
}

// validateLogLevel enforces the closed set {DEBUG, INFO, WARN, ERROR}.
//
// The check is duplicated here (vs. delegating to internal/log.ParseLevel)
// to keep the dependency graph in the architecture spec §5 acyclic:
// internal/config has no internal/ imports.
func validateLogLevel(s string) error {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG", "INFO", "WARN", "WARNING", "ERROR":
		return nil
	default:
		return &InvalidKeyError{Key: "LOG_LEVEL", Reason: fmt.Sprintf("unknown level %q (want DEBUG, INFO, WARN, or ERROR)", s)}
	}
}
