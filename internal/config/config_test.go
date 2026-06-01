package config_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesouldev/goboxd/internal/config"
)

// envFrom builds a config.Lookup over a static map.
func envFrom(m map[string]string) config.Lookup {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

// TestLoadDefaultsApplied confirms every field falls back to its
// architecture-spec default when no env var is set.
func TestLoadDefaultsApplied(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(envFrom(nil))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.APIBindAddr != "0.0.0.0:8080" {
		t.Errorf("APIBindAddr = %q, want 0.0.0.0:8080", cfg.APIBindAddr)
	}
	if cfg.LogLevel != "INFO" {
		t.Errorf("LogLevel = %q, want INFO", cfg.LogLevel)
	}
	if cfg.MaxRequestBodyBytes != 1<<20 {
		t.Errorf("MaxRequestBodyBytes = %d, want 1048576", cfg.MaxRequestBodyBytes)
	}
	if cfg.WorkerPoolSize != 4 {
		t.Errorf("WorkerPoolSize = %d, want 4", cfg.WorkerPoolSize)
	}
	if cfg.WorkerPoolQueueLen != 64 {
		t.Errorf("WorkerPoolQueueLen = %d, want 64", cfg.WorkerPoolQueueLen)
	}
	if cfg.WorkerPoolDrainTimeoutS != 30 {
		t.Errorf("WorkerPoolDrainTimeoutS = %d, want 30", cfg.WorkerPoolDrainTimeoutS)
	}
	if cfg.LanguageRegistryPath != "/etc/goboxd/language_registry.yaml" {
		t.Errorf("LanguageRegistryPath = %q, want /etc/goboxd/language_registry.yaml", cfg.LanguageRegistryPath)
	}
	if cfg.ServiceName != "" {
		t.Errorf("ServiceName = %q, want empty (callers fall back to version.ServiceName)", cfg.ServiceName)
	}
	if cfg.SandboxRootDirOwnerUID != nil {
		t.Errorf("SandboxRootDirOwnerUID = %v, want nil", cfg.SandboxRootDirOwnerUID)
	}
	if cfg.SandboxRootDirPermsMax != 0775 {
		t.Errorf("SandboxRootDirPermsMax = %o, want 0775", cfg.SandboxRootDirPermsMax)
	}
}

// TestLoadEnvOverrides confirms env vars override the defaults.
func TestLoadEnvOverrides(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(envFrom(map[string]string{
		"API_BIND_ADDR":               "127.0.0.1:9090",
		"LOG_LEVEL":                   "DEBUG",
		"SERVICE_NAME":                "custom-name",
		"BUILD_VERSION":               "v1.2.3",
		"WORKER_POOL_SIZE":            "16",
		"WORKER_POOL_QUEUE_LEN":       "256",
		"WORKER_POOL_DRAIN_TIMEOUT_S": "60",
		"MAX_REQUEST_BODY_BYTES":      "2097152",
		"LANGUAGE_REGISTRY_PATH":      "/srv/registry.yaml",
		"SANDBOX_ROOT_DIR_OWNER_UID":  "12345",
		"SANDBOX_ROOT_DIR_PERMS_MAX":  "0700",
	}))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.APIBindAddr != "127.0.0.1:9090" {
		t.Errorf("APIBindAddr override missed: %q", cfg.APIBindAddr)
	}
	if cfg.WorkerPoolSize != 16 || cfg.WorkerPoolQueueLen != 256 || cfg.WorkerPoolDrainTimeoutS != 60 {
		t.Errorf("worker pool overrides missed: %+v", cfg)
	}
	if cfg.MaxRequestBodyBytes != 2097152 {
		t.Errorf("MaxRequestBodyBytes override missed: %d", cfg.MaxRequestBodyBytes)
	}
	if cfg.LanguageRegistryPath != "/srv/registry.yaml" {
		t.Errorf("LanguageRegistryPath override missed: %q", cfg.LanguageRegistryPath)
	}
	if cfg.ServiceName != "custom-name" || cfg.BuildVersion != "v1.2.3" {
		t.Errorf("name/version overrides missed: %+v", cfg)
	}
	if cfg.SandboxRootDirOwnerUID == nil || *cfg.SandboxRootDirOwnerUID != 12345 {
		t.Errorf("SandboxRootDirOwnerUID override missed: %v", cfg.SandboxRootDirOwnerUID)
	}
	if cfg.SandboxRootDirPermsMax != 0700 {
		t.Errorf("SandboxRootDirPermsMax override missed: %o", cfg.SandboxRootDirPermsMax)
	}
}

// TestLoadInvalidValues exercises every range/parse failure mode.
//
// Each row asserts that the returned error is *InvalidKeyError and that
// its Key field names the offending env var (REQ A-13.6, Property 10).
func TestLoadInvalidValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"log level unknown", map[string]string{"LOG_LEVEL": "screaming"}, "LOG_LEVEL"},
		{"worker pool size below min", map[string]string{"WORKER_POOL_SIZE": "0"}, "WORKER_POOL_SIZE"},
		{"worker pool size above max", map[string]string{"WORKER_POOL_SIZE": "9999"}, "WORKER_POOL_SIZE"},
		{"worker pool size not int", map[string]string{"WORKER_POOL_SIZE": "lots"}, "WORKER_POOL_SIZE"},
		{"queue len above max", map[string]string{"WORKER_POOL_QUEUE_LEN": "999999"}, "WORKER_POOL_QUEUE_LEN"},
		{"drain timeout below min", map[string]string{"WORKER_POOL_DRAIN_TIMEOUT_S": "-1"}, "WORKER_POOL_DRAIN_TIMEOUT_S"},
		{"drain timeout above max", map[string]string{"WORKER_POOL_DRAIN_TIMEOUT_S": "999999"}, "WORKER_POOL_DRAIN_TIMEOUT_S"},
		{"body bytes below min", map[string]string{"MAX_REQUEST_BODY_BYTES": "0"}, "MAX_REQUEST_BODY_BYTES"},
		{"body bytes above max", map[string]string{"MAX_REQUEST_BODY_BYTES": "999999999"}, "MAX_REQUEST_BODY_BYTES"},
		{"max source bytes below min", map[string]string{"MAX_SOURCE_SIZE_BYTES": "0"}, "MAX_SOURCE_SIZE_BYTES"},
		{"max stdin bytes above max", map[string]string{"MAX_STDIN_SIZE_BYTES": "999999999"}, "MAX_STDIN_SIZE_BYTES"},
		{"stdout capture below min", map[string]string{"STDOUT_CAPTURE_LIMIT_BYTES": "0"}, "STDOUT_CAPTURE_LIMIT_BYTES"},
		{"stderr capture above max", map[string]string{"STDERR_CAPTURE_LIMIT_BYTES": "999999999"}, "STDERR_CAPTURE_LIMIT_BYTES"},
		{"wall ceiling below min", map[string]string{"LANG_DEFAULT_WALL_TIME_S_MAX": "0"}, "LANG_DEFAULT_WALL_TIME_S_MAX"},
		{"cpu ceiling above max", map[string]string{"LANG_DEFAULT_CPU_TIME_S_MAX": "9999"}, "LANG_DEFAULT_CPU_TIME_S_MAX"},
		{"memory ceiling below floor", map[string]string{"LANG_DEFAULT_MEMORY_MB_MAX": "1"}, "LANG_DEFAULT_MEMORY_MB_MAX"},
		{"process ceiling zero", map[string]string{"LANG_DEFAULT_PROCESS_COUNT_MAX": "0"}, "LANG_DEFAULT_PROCESS_COUNT_MAX"},
		{"output ceiling above max", map[string]string{"LANG_DEFAULT_OUTPUT_SIZE_MB_MAX": "999"}, "LANG_DEFAULT_OUTPUT_SIZE_MB_MAX"},
		{"orphan ttl below min", map[string]string{"SANDBOX_ORPHAN_TTL_S": "59"}, "SANDBOX_ORPHAN_TTL_S"},
		{"orphan ttl above max", map[string]string{"SANDBOX_ORPHAN_TTL_S": "86401"}, "SANDBOX_ORPHAN_TTL_S"},
		{"ro mounts invalid format", map[string]string{"SANDBOX_RO_MOUNTS": "/usr"}, "SANDBOX_RO_MOUNTS"},
		{"owner uid negative", map[string]string{"SANDBOX_ROOT_DIR_OWNER_UID": "-1"}, "SANDBOX_ROOT_DIR_OWNER_UID"},
		{"owner uid not int", map[string]string{"SANDBOX_ROOT_DIR_OWNER_UID": "root"}, "SANDBOX_ROOT_DIR_OWNER_UID"},
		{"owner uid too large", map[string]string{"SANDBOX_ROOT_DIR_OWNER_UID": "4294967296"}, "SANDBOX_ROOT_DIR_OWNER_UID"},
		{"perms max invalid octal", map[string]string{"SANDBOX_ROOT_DIR_PERMS_MAX": "0999"}, "SANDBOX_ROOT_DIR_PERMS_MAX"},
		{"perms max world-writable 0777", map[string]string{"SANDBOX_ROOT_DIR_PERMS_MAX": "0777"}, "SANDBOX_ROOT_DIR_PERMS_MAX"},
		{"perms max world-writable 0002", map[string]string{"SANDBOX_ROOT_DIR_PERMS_MAX": "0002"}, "SANDBOX_ROOT_DIR_PERMS_MAX"},
		{"perms max above 0777", map[string]string{"SANDBOX_ROOT_DIR_PERMS_MAX": "01000"}, "SANDBOX_ROOT_DIR_PERMS_MAX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Load(envFrom(tc.env))
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			var ike *config.InvalidKeyError
			if !errors.As(err, &ike) {
				t.Fatalf("error is not *InvalidKeyError: %v", err)
			}
			if ike.Key != tc.key {
				t.Errorf("InvalidKeyError.Key = %q, want %q", ike.Key, tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error message %q does not mention key %q", err, tc.key)
			}
		})
	}
}

// TestLoadDefaultLookup asserts the production code path (env=nil) does
// not panic and returns a populated *Config. We don't assert specific
// values because the running process environment is uncontrolled.
func TestLoadDefaultLookup(t *testing.T) {
	cfg, err := config.Load(nil)
	if err != nil && cfg != nil {
		t.Fatalf("Load(nil) returned error AND non-nil cfg: %v", err)
	}
}

// TestCeilingsFor confirms the lookup helper exposes the resolved
// ceilings. Phase 2 will extend with per-language overrides; Wave A
// only needs the global default path.
func TestCeilingsFor(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(envFrom(map[string]string{
		"LANG_DEFAULT_WALL_TIME_S_MAX":    "30",
		"LANG_DEFAULT_CPU_TIME_S_MAX":     "30",
		"LANG_DEFAULT_MEMORY_MB_MAX":      "512",
		"LANG_DEFAULT_PROCESS_COUNT_MAX":  "32",
		"LANG_DEFAULT_OUTPUT_SIZE_MB_MAX": "8",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.CeilingsFor("py3")
	want := config.ResourceCeilings{
		WallTimeS: 30, CPUTimeS: 30, MemoryMB: 512, ProcessCount: 32, OutputSizeMB: 8,
	}
	if got != want {
		t.Errorf("CeilingsFor = %+v, want %+v", got, want)
	}
	// Unknown language id falls back to the same defaults (Wave A behaviour).
	if cfg.CeilingsFor("unknown") != want {
		t.Errorf("unknown language did not fall back to defaults")
	}
}

// TestLoadDefaultsForNewKeys asserts the architecture-spec defaults for
// the size and per-language ceiling keys.
func TestLoadDefaultsForNewKeys(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(envFrom(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MaxSourceSizeBytes != 1<<20 {
		t.Errorf("MaxSourceSizeBytes default = %d, want 1048576", cfg.MaxSourceSizeBytes)
	}
	if cfg.MaxStdinSizeBytes != 1<<20 {
		t.Errorf("MaxStdinSizeBytes default = %d, want 1048576", cfg.MaxStdinSizeBytes)
	}
	if cfg.StdoutCaptureLimitBytes != 1<<20 {
		t.Errorf("StdoutCaptureLimitBytes default = %d, want 1048576", cfg.StdoutCaptureLimitBytes)
	}
	if cfg.StderrCaptureLimitBytes != 1<<20 {
		t.Errorf("StderrCaptureLimitBytes default = %d, want 1048576", cfg.StderrCaptureLimitBytes)
	}
	want := config.ResourceCeilings{WallTimeS: 60, CPUTimeS: 60, MemoryMB: 1024, ProcessCount: 64, OutputSizeMB: 64}
	if cfg.DefaultCeilings != want {
		t.Errorf("DefaultCeilings = %+v, want %+v", cfg.DefaultCeilings, want)
	}
	if cfg.NsJailPath != "/usr/local/bin/nsjail" {
		t.Errorf("NsJailPath default = %q", cfg.NsJailPath)
	}
	if cfg.SandboxRoot != "/var/lib/goboxd/sandbox" {
		t.Errorf("SandboxRoot default = %q", cfg.SandboxRoot)
	}
	if cfg.SeccompPolicy != "default" {
		t.Errorf("SeccompPolicy default = %q", cfg.SeccompPolicy)
	}
	if cfg.SandboxOrphanTTL != 600*time.Second {
		t.Errorf("SandboxOrphanTTL default = %v", cfg.SandboxOrphanTTL)
	}
	if len(cfg.SandboxRoMounts) != 5 {
		t.Errorf("SandboxRoMounts length = %d, want 5", len(cfg.SandboxRoMounts))
	}
}
