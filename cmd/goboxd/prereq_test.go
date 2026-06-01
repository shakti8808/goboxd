package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/thesouldev/goboxd/internal/config"
	goboxlog "github.com/thesouldev/goboxd/internal/log"
	"github.com/thesouldev/goboxd/internal/metrics"
)

// gather collects metrics from the registry for assertion.
func gatherMetrics(t *testing.T, mc *metrics.Collector) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := mc.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		out[f.GetName()] = f
	}
	return out
}

func findMetric(family *dto.MetricFamily, labels map[string]string) *dto.Metric {
	for _, m := range family.GetMetric() {
		match := true
		for _, lp := range m.GetLabel() {
			if want, ok := labels[lp.GetName()]; ok && lp.GetValue() != want {
				match = false
				break
			}
		}
		if match {
			return m
		}
	}
	return nil
}

func TestValidatePrereqs_HappyPath(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()

	// Sandbox root
	sandboxRoot := filepath.Join(tmp, "sandbox")
	if err := os.Mkdir(sandboxRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sandboxRoot, 0755); err != nil {
		t.Fatal(err)
	}

	// NsJail
	nsjailPath := filepath.Join(tmp, "nsjail")
	if err := os.WriteFile(nsjailPath, []byte("dummy-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	// RO Mounts
	roMount := filepath.Join(tmp, "romount")
	if err := os.Mkdir(roMount, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		SandboxRoot:            sandboxRoot,
		NsJailPath:             nsjailPath,
		SandboxRootDirPermsMax: 0775,
		SandboxRoMounts: []config.BindMount{
			{HostPath: roMount, GuestPath: "/romount"},
		},
	}

	mc := metrics.New("goboxd-test", "dev")
	var logBuf bytes.Buffer
	logger := goboxlog.New(&logBuf, slog.LevelInfo)

	err := validatePrereqs(cfg, mc, logger)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidatePrereqs_SandboxRoot(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		tmp := t.TempDir()
		cfg := &config.Config{
			SandboxRoot:            filepath.Join(tmp, "non-existent"),
			NsJailPath:             filepath.Join(tmp, "nsjail"),
			SandboxRootDirPermsMax: 0775,
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}

		// Verify metric
		families := gatherMetrics(t, mc)
		sp, ok := families["goboxd_startup_prereq_failures_total"]
		if !ok {
			t.Fatal("metric missing")
		}
		m := findMetric(sp, map[string]string{"prereq": "sandbox_root"})
		if m == nil || m.GetCounter().GetValue() != 1 {
			t.Errorf("expected counter value 1, got %v", m)
		}

		// Verify log
		if !bytes.Contains(logBuf.Bytes(), []byte("startup_prereq_failed")) {
			t.Errorf("expected log to contain event, got: %s", logBuf.String())
		}
	})

	t.Run("is_file_not_dir", func(t *testing.T) {
		tmp := t.TempDir()
		filePath := filepath.Join(tmp, "root-file")
		if err := os.WriteFile(filePath, []byte("hi"), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := &config.Config{
			SandboxRoot:            filePath,
			NsJailPath:             filepath.Join(tmp, "nsjail"),
			SandboxRootDirPermsMax: 0775,
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("not a directory")) {
			t.Errorf("expected error not a directory, got: %v", err)
		}
	})

	t.Run("owner_mismatch", func(t *testing.T) {
		tmp := t.TempDir()
		sandboxRoot := filepath.Join(tmp, "sandbox")
		if err := os.Mkdir(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}

		wrongUID := uint32(os.Geteuid() + 1)
		cfg := &config.Config{
			SandboxRoot:            sandboxRoot,
			SandboxRootDirOwnerUID: &wrongUID,
			NsJailPath:             filepath.Join(tmp, "nsjail"),
			SandboxRootDirPermsMax: 0775,
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("incorrect owner")) {
			t.Errorf("expected error incorrect owner, got: %v", err)
		}
	})

	t.Run("world_writable", func(t *testing.T) {
		tmp := t.TempDir()
		sandboxRoot := filepath.Join(tmp, "sandbox")
		if err := os.Mkdir(sandboxRoot, 0777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sandboxRoot, 0777); err != nil { // bypass umask
			t.Fatal(err)
		}

		nsjailPath := filepath.Join(tmp, "nsjail")
		if err := os.WriteFile(nsjailPath, []byte("dummy"), 0755); err != nil {
			t.Fatal(err)
		}

		cfg := &config.Config{
			SandboxRoot:            sandboxRoot,
			NsJailPath:             nsjailPath,
			SandboxRootDirPermsMax: 0777,
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("world-writable")) {
			t.Errorf("expected error world-writable, got: %v", err)
		}
	})

	t.Run("permissions_exceed_max", func(t *testing.T) {
		tmp := t.TempDir()
		sandboxRoot := filepath.Join(tmp, "sandbox")
		if err := os.Mkdir(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}

		nsjailPath := filepath.Join(tmp, "nsjail")
		if err := os.WriteFile(nsjailPath, []byte("dummy"), 0755); err != nil {
			t.Fatal(err)
		}

		cfg := &config.Config{
			SandboxRoot:            sandboxRoot,
			NsJailPath:             nsjailPath,
			SandboxRootDirPermsMax: 0700, // 0755 exceeds 0700 max
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("exceed maximum allowed permissions")) {
			t.Errorf("expected error exceeding permissions, got: %v", err)
		}
	})
}

func TestValidatePrereqs_RoMounts(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		tmp := t.TempDir()
		sandboxRoot := filepath.Join(tmp, "sandbox")
		if err := os.Mkdir(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}

		cfg := &config.Config{
			SandboxRoot:            sandboxRoot,
			SandboxRootDirPermsMax: 0775,
			NsJailPath:             filepath.Join(tmp, "nsjail"),
			SandboxRoMounts: []config.BindMount{
				{HostPath: filepath.Join(tmp, "non-existent"), GuestPath: "/ro"},
			},
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}

		// Verify metric
		families := gatherMetrics(t, mc)
		sp, ok := families["goboxd_startup_prereq_failures_total"]
		if !ok {
			t.Fatal("metric missing")
		}
		m := findMetric(sp, map[string]string{"prereq": "ro_mount"})
		if m == nil || m.GetCounter().GetValue() != 1 {
			t.Errorf("expected counter value 1, got %v", m)
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		// Skipping on root users since chmod 0000 is still readable by root.
		if os.Geteuid() == 0 {
			t.Skip("skipping on root")
		}

		tmp := t.TempDir()
		sandboxRoot := filepath.Join(tmp, "sandbox")
		if err := os.Mkdir(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}

		unreadableFile := filepath.Join(tmp, "unreadable")
		if err := os.WriteFile(unreadableFile, []byte("secrets"), 0000); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(unreadableFile, 0000); err != nil {
			t.Fatal(err)
		}

		cfg := &config.Config{
			SandboxRoot:            sandboxRoot,
			SandboxRootDirPermsMax: 0775,
			NsJailPath:             filepath.Join(tmp, "nsjail"),
			SandboxRoMounts: []config.BindMount{
				{HostPath: unreadableFile, GuestPath: "/ro"},
			},
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}

		// Verify metric
		families := gatherMetrics(t, mc)
		sp, ok := families["goboxd_startup_prereq_failures_total"]
		if !ok {
			t.Fatal("metric missing")
		}
		m := findMetric(sp, map[string]string{"prereq": "ro_mount"})
		if m == nil || m.GetCounter().GetValue() != 1 {
			t.Errorf("expected counter value 1, got %v", m)
		}
	})
}

func TestValidatePrereqs_NsJail(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		tmp := t.TempDir()
		sandboxRoot := filepath.Join(tmp, "sandbox")
		if err := os.Mkdir(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}

		cfg := &config.Config{
			SandboxRoot:            sandboxRoot,
			SandboxRootDirPermsMax: 0775,
			NsJailPath:             filepath.Join(tmp, "non-existent-nsjail"),
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}

		// Verify metric
		families := gatherMetrics(t, mc)
		sp, ok := families["goboxd_startup_prereq_failures_total"]
		if !ok {
			t.Fatal("metric missing")
		}
		m := findMetric(sp, map[string]string{"prereq": "nsjail_binary"})
		if m == nil || m.GetCounter().GetValue() != 1 {
			t.Errorf("expected counter value 1, got %v", m)
		}
	})

	t.Run("is_dir_not_file", func(t *testing.T) {
		tmp := t.TempDir()
		sandboxRoot := filepath.Join(tmp, "sandbox")
		if err := os.Mkdir(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}

		nsjailDir := filepath.Join(tmp, "nsjaildir")
		if err := os.Mkdir(nsjailDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(nsjailDir, 0755); err != nil {
			t.Fatal(err)
		}

		cfg := &config.Config{
			SandboxRoot:            sandboxRoot,
			SandboxRootDirPermsMax: 0775,
			NsJailPath:             nsjailDir,
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("not a regular file")) {
			t.Errorf("expected error not a regular file, got: %v", err)
		}
	})

	t.Run("non_executable", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("skipping on Windows")
		}

		tmp := t.TempDir()
		sandboxRoot := filepath.Join(tmp, "sandbox")
		if err := os.Mkdir(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sandboxRoot, 0755); err != nil {
			t.Fatal(err)
		}

		nsjailPath := filepath.Join(tmp, "nsjail")
		if err := os.WriteFile(nsjailPath, []byte("non-executable"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(nsjailPath, 0600); err != nil {
			t.Fatal(err)
		}

		cfg := &config.Config{
			SandboxRoot:            sandboxRoot,
			SandboxRootDirPermsMax: 0775,
			NsJailPath:             nsjailPath,
		}

		mc := metrics.New("goboxd-test", "dev")
		var logBuf bytes.Buffer
		logger := goboxlog.New(&logBuf, slog.LevelInfo)

		err := validatePrereqs(cfg, mc, logger)
		if err == nil {
			t.Fatal("expected error")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("not executable")) {
			t.Errorf("expected error not executable, got: %v", err)
		}
	})
}
