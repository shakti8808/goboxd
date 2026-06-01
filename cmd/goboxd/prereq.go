package main

import (
	"fmt"
	"log/slog"
	"os"
	"syscall"

	"github.com/thesouldev/goboxd/internal/config"
	"github.com/thesouldev/goboxd/internal/metrics"
)

// validatePrereqs runs startup checks on sandbox prerequisites in order:
// (a) Sandbox root existence, ownership, and permission bounds.
// (b) Read-only mounts host path existence and readability.
// (c) NsJail path exists, is regular, and is executable.
// On failure, logs an error event, increments startup prereq failures metric, and returns an error.
func validatePrereqs(cfg *config.Config, mc *metrics.Collector, log *slog.Logger) error {
	// (a) Sandbox Root Validation
	if err := validateSandboxRoot(cfg, mc, log); err != nil {
		return err
	}

	// (b) Read-Only Mounts Validation
	if err := validateRoMounts(cfg, mc, log); err != nil {
		return err
	}

	// (c) NsJail Binary Validation
	if err := validateNsJailBinary(cfg, mc, log); err != nil {
		return err
	}

	return nil
}

func validateSandboxRoot(cfg *config.Config, mc *metrics.Collector, log *slog.Logger) error {
	info, err := os.Stat(cfg.SandboxRoot)
	if err != nil {
		mc.IncStartupPrereqFailure("sandbox_root")
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "sandbox_root",
			"path", cfg.SandboxRoot,
			"reason", err.Error(),
		)
		return fmt.Errorf("sandbox root check: %w", err)
	}

	if !info.IsDir() {
		mc.IncStartupPrereqFailure("sandbox_root")
		reason := "not a directory"
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "sandbox_root",
			"path", cfg.SandboxRoot,
			"reason", reason,
		)
		return fmt.Errorf("sandbox root check: %s: %s", cfg.SandboxRoot, reason)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		mc.IncStartupPrereqFailure("sandbox_root")
		reason := "unable to retrieve raw syscall.Stat_t"
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "sandbox_root",
			"path", cfg.SandboxRoot,
			"reason", reason,
		)
		return fmt.Errorf("sandbox root check: %s: %s", cfg.SandboxRoot, reason)
	}

	actualUID := stat.Uid
	var expectedUID uint32
	if cfg.SandboxRootDirOwnerUID != nil {
		expectedUID = *cfg.SandboxRootDirOwnerUID
	} else {
		expectedUID = uint32(os.Geteuid())
	}

	if actualUID != expectedUID {
		mc.IncStartupPrereqFailure("sandbox_root")
		reason := fmt.Sprintf("incorrect owner: expected UID %d, got %d", expectedUID, actualUID)
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "sandbox_root",
			"path", cfg.SandboxRoot,
			"reason", reason,
		)
		return fmt.Errorf("sandbox root check: %s: %s", cfg.SandboxRoot, reason)
	}

	perm := info.Mode().Perm()
	if perm&0002 != 0 {
		mc.IncStartupPrereqFailure("sandbox_root")
		reason := "directory is world-writable"
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "sandbox_root",
			"path", cfg.SandboxRoot,
			"reason", reason,
		)
		return fmt.Errorf("sandbox root check: %s: %s", cfg.SandboxRoot, reason)
	}

	if perm&^cfg.SandboxRootDirPermsMax != 0 {
		mc.IncStartupPrereqFailure("sandbox_root")
		reason := fmt.Sprintf("permissions %o exceed maximum allowed permissions %o", perm, cfg.SandboxRootDirPermsMax)
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "sandbox_root",
			"path", cfg.SandboxRoot,
			"reason", reason,
		)
		return fmt.Errorf("sandbox root check: %s: %s", cfg.SandboxRoot, reason)
	}

	return nil
}

func validateRoMounts(cfg *config.Config, mc *metrics.Collector, log *slog.Logger) error {
	for _, mount := range cfg.SandboxRoMounts {
		hostPath := mount.HostPath

		// Stat existence
		if _, err := os.Stat(hostPath); err != nil {
			mc.IncStartupPrereqFailure("ro_mount")
			log.Error("startup_prereq_failed",
				"event", "startup_prereq_failed",
				"prereq", "ro_mount",
				"path", hostPath,
				"reason", err.Error(),
			)
			return fmt.Errorf("read-only mount check: stat failed for %s: %w", hostPath, err)
		}

		// Verify readability by opening the file/directory
		f, err := os.Open(hostPath)
		if err != nil {
			mc.IncStartupPrereqFailure("ro_mount")
			log.Error("startup_prereq_failed",
				"event", "startup_prereq_failed",
				"prereq", "ro_mount",
				"path", hostPath,
				"reason", err.Error(),
			)
			return fmt.Errorf("read-only mount check: open failed for %s: %w", hostPath, err)
		}
		f.Close()
	}
	return nil
}

func validateNsJailBinary(cfg *config.Config, mc *metrics.Collector, log *slog.Logger) error {
	info, err := os.Stat(cfg.NsJailPath)
	if err != nil {
		mc.IncStartupPrereqFailure("nsjail_binary")
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "nsjail_binary",
			"path", cfg.NsJailPath,
			"reason", err.Error(),
		)
		return fmt.Errorf("nsjail binary check: %w", err)
	}

	if !info.Mode().IsRegular() {
		mc.IncStartupPrereqFailure("nsjail_binary")
		reason := "not a regular file"
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "nsjail_binary",
			"path", cfg.NsJailPath,
			"reason", reason,
		)
		return fmt.Errorf("nsjail binary check: %s: %s", cfg.NsJailPath, reason)
	}

	if info.Mode().Perm()&0111 == 0 {
		mc.IncStartupPrereqFailure("nsjail_binary")
		reason := "not executable"
		log.Error("startup_prereq_failed",
			"event", "startup_prereq_failed",
			"prereq", "nsjail_binary",
			"path", cfg.NsJailPath,
			"reason", reason,
		)
		return fmt.Errorf("nsjail binary check: %s: %s", cfg.NsJailPath, reason)
	}

	return nil
}
