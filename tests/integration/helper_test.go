//go:build integration

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// serverInstance represents a running GoboxD server for integration testing.
type serverInstance struct {
	cmd    *exec.Cmd
	URL    string
	cancel context.CancelFunc
	stdout *bytes.Buffer
}

// startServer starts goboxdBinPath in a background process with custom environment variables,
// dynamically binds it to 127.0.0.1:0, parses the listener port from stdout, and returns the instance.
func startServer(t *testing.T, env map[string]string) *serverInstance {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, goboxdBinPath)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "API_BIND_ADDR=127.0.0.1:0") // dynamic port allocation

	// If not Linux, mock NsJail and SANDBOX_RO_MOUNTS to allow the server to start.
	if runtime.GOOS != "linux" {
		dummyNsJail := filepath.Join(t.TempDir(), "nsjail")
		// The mock nsjail script will sleep if NSJAIL_SLEEP_S is set, enabling queue/timeout testing.
		script := "#!/bin/sh\nif [ -n \"$NSJAIL_SLEEP_S\" ]; then\n  sleep \"$NSJAIL_SLEEP_S\"\nfi\nexit 0\n"
		if err := os.WriteFile(dummyNsJail, []byte(script), 0755); err != nil {
			cancel()
			t.Fatal(err)
		}
		if _, ok := env["NSJAIL_PATH"]; !ok {
			cmd.Env = append(cmd.Env, "NSJAIL_PATH="+dummyNsJail)
		}
		if _, ok := env["SANDBOX_RO_MOUNTS"]; !ok {
			cmd.Env = append(cmd.Env, "SANDBOX_RO_MOUNTS=/bin:/bin")
		}
	}

	if _, ok := env["LANGUAGE_REGISTRY_PATH"]; !ok {
		absPath, err := filepath.Abs("../../configs/language_registry.yaml")
		if err == nil {
			cmd.Env = append(cmd.Env, "LANGUAGE_REGISTRY_PATH="+absPath)
		}
	}

	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/lib64"); os.IsNotExist(err) {
			if _, ok := env["SANDBOX_RO_MOUNTS"]; !ok {
				cmd.Env = append(cmd.Env, "SANDBOX_RO_MOUNTS=/usr:/usr,/lib:/lib,/bin:/bin,/etc/alternatives:/etc/alternatives")
			}
		}
	}

	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("failed to create stdout pipe: %v", err)
	}

	var stdoutBuf bytes.Buffer
	cmd.Stderr = os.Stderr // pipe stderr directly for debug clarity

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("failed to start goboxd process: %v", err)
	}

	// Read stdout to find listener_open address
	portChan := make(chan string, 1)
	errChan := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			stdoutBuf.WriteString(line + "\n")
			// Look for listener_open event
			if strings.Contains(line, `"event":"listener_open"`) {
				// Parse JSON to extract the addr
				var logLine struct {
					Addr string `json:"addr"`
				}
				if err := json.Unmarshal([]byte(line), &logLine); err == nil && logLine.Addr != "" {
					portChan <- logLine.Addr
				}
			}
		}
		if err := scanner.Err(); err != nil {
			errChan <- err
		}
	}()

	select {
	case addr := <-portChan:
		return &serverInstance{
			cmd:    cmd,
			URL:    "http://" + addr,
			cancel: cancel,
			stdout: &stdoutBuf,
		}
	case err := <-errChan:
		cancel()
		t.Fatalf("scanner error while starting server: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatalf("timeout waiting for server to start. Output so far:\n%s", stdoutBuf.String())
	}
	return nil
}

func (s *serverInstance) Close() {
	s.cancel()
	_ = s.cmd.Wait()
}

// runServerExpectFailure runs GoboxD process and waits for it to exit, expecting a non-zero exit code.
// Returns the exit code, stdout, and stderr.
func runServerExpectFailure(t *testing.T, env map[string]string) (int, string, string) {
	t.Helper()

	cmd := exec.Command(goboxdBinPath)
	cmd.Env = os.Environ()

	// If not Linux, mock NsJail and SANDBOX_RO_MOUNTS to allow the server to proceed to validation.
	if runtime.GOOS != "linux" {
		dummyNsJail := filepath.Join(t.TempDir(), "nsjail")
		if err := os.WriteFile(dummyNsJail, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
		if _, ok := env["NSJAIL_PATH"]; !ok {
			cmd.Env = append(cmd.Env, "NSJAIL_PATH="+dummyNsJail)
		}
		if _, ok := env["SANDBOX_RO_MOUNTS"]; !ok {
			cmd.Env = append(cmd.Env, "SANDBOX_RO_MOUNTS=/bin:/bin")
		}
	}

	if _, ok := env["LANGUAGE_REGISTRY_PATH"]; !ok {
		absPath, err := filepath.Abs("../../configs/language_registry.yaml")
		if err == nil {
			cmd.Env = append(cmd.Env, "LANGUAGE_REGISTRY_PATH="+absPath)
		}
	}

	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/lib64"); os.IsNotExist(err) {
			if _, ok := env["SANDBOX_RO_MOUNTS"]; !ok {
				cmd.Env = append(cmd.Env, "SANDBOX_RO_MOUNTS=/usr:/usr,/lib:/lib,/bin:/bin,/etc/alternatives:/etc/alternatives")
			}
		}
	}

	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected server to exit with non-zero code, but it exited successfully. stdout:\n%s", stdoutBuf.String())
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("failed to run server: %v", err)
	}

	return exitErr.ExitCode(), stdoutBuf.String(), stderrBuf.String()
}
