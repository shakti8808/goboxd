//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

var goboxdBinPath string

func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Printf("failed to get working dir: %v\n", err)
		os.Exit(1)
	}

	binName := "goboxd_test_bin"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(wd, binName)

	// Build the goboxd binary. cmd/goboxd is located relative to tests/integration/ as ../../cmd/goboxd.
	cmd := exec.Command("go", "build", "-o", binPath, "../../cmd/goboxd")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("failed to build goboxd binary: %v\nOutput:\n%s\n", err, string(out))
		os.Exit(1)
	}

	goboxdBinPath = binPath

	code := m.Run()

	// Clean up the binary
	_ = os.Remove(goboxdBinPath)

	os.Exit(code)
}
