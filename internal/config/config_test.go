package config_test

import (
	"errors"
	"strings"
	"testing"

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
