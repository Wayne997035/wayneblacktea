package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/cli"
	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
)

// osWindowsName is reused across test skip guards.
const osWindowsName = "windows"

// captureStdout swaps os.Stdout for a pipe, runs fn, and returns whatever
// fn wrote. Tests that exercise CLI output paths use this.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fnErr := fn()
	_ = w.Close()
	var out strings.Builder
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			out.Write(chunk[:n])
		}
		if err != nil {
			break
		}
	}
	return out.String(), fnErr
}

// TestRunStatus_NotRunning verifies plain output and exit-error sentinel
// when no PID file exists.
func TestRunStatus_NotRunning(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("Unix-only in Phase 2")
	}
	setupTestEnv(t)

	out, err := captureStdout(t, func() error {
		return cli.RunStatus([]string{"--format", "plain"})
	})
	if err == nil {
		t.Error("expected non-nil error for not-running state")
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("plain output missing 'not running': %q", out)
	}
}

// TestRunStatus_JSONFormat verifies the JSON shape includes the expected fields
// when the server is healthy.
func TestRunStatus_JSONFormat(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("Unix-only in Phase 2")
	}
	setupTestEnv(t)
	fakeServer := buildFakeServer(t)
	port := freeTestPort(t)
	t.Cleanup(func() { stopRunningServer(t) })

	// Spawn the fake server directly so we don't depend on RunSetup.
	sup := lifecycle.NewNohupSupervisor()
	pidPath := lifecycle.PIDPath("")
	logPath := filepath.Join(t.TempDir(), "server.log")
	env := append(os.Environ(), "PORT="+strconv.Itoa(port))
	h, err := sup.Start(context.Background(), lifecycle.StartOptions{
		BinaryPath: fakeServer,
		PIDPath:    pidPath,
		LogPath:    logPath,
		Env:        env,
	})
	if err != nil {
		t.Fatalf("Start fakeServer: %v", err)
	}
	// Wait for /health.
	if !waitTCPHealth(port, 3*time.Second) {
		t.Fatalf("fakeServer did not respond to /health")
	}
	// status reads PORT to figure out port; set it explicitly.
	t.Setenv("PORT", strconv.Itoa(port))

	out, err := captureStdout(t, func() error {
		return cli.RunStatus([]string{"--format", "json"})
	})
	if err != nil {
		t.Fatalf("RunStatus: %v", err)
	}
	var report cli.StatusReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parsing JSON output %q: %v", out, err)
	}
	if report.PID != h.PID {
		t.Errorf("report.PID = %d; want %d", report.PID, h.PID)
	}
	if report.Port != port {
		t.Errorf("report.Port = %d; want %d", report.Port, port)
	}
	if !report.Healthy {
		t.Errorf("report.Healthy = false; want true")
	}
}

// TestRunStatus_InvalidFormat rejects unknown --format values.
func TestRunStatus_InvalidFormat(t *testing.T) {
	setupTestEnv(t)
	err := cli.RunStatus([]string{"--format", "xml"})
	if err == nil {
		t.Error("expected error for --format xml; got nil")
	}
}

// TestRunStop_NoPIDFile is the idempotent path: stop on a fresh system.
func TestRunStop_NoPIDFile(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("Unix-only in Phase 2")
	}
	setupTestEnv(t)
	out, err := captureStdout(t, func() error {
		return cli.RunStop(nil)
	})
	if err != nil {
		t.Errorf("RunStop on fresh system: %v", err)
	}
	if !strings.Contains(out, "not running") {
		t.Errorf("plain output missing 'not running': %q", out)
	}
}

// TestRunStop_StalePIDFile removes a PID file referencing a dead process.
func TestRunStop_StalePIDFile(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("Unix-only in Phase 2")
	}
	setupTestEnv(t)
	pidPath := lifecycle.PIDPath("")
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep binary not in PATH: %v", err)
	}
	//nolint:gosec // G204: sleepBin from exec.LookPath
	dead := exec.CommandContext(context.Background(), sleepBin, "0.01")
	if err := dead.Run(); err != nil {
		t.Fatalf("starting throwaway sleep: %v", err)
	}
	if err := lifecycle.WritePID(pidPath, dead.Process.Pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := cli.RunStop(nil); err != nil {
		t.Errorf("RunStop on stale PID: %v", err)
	}
	if _, err := lifecycle.ReadPID(pidPath); err == nil {
		t.Error("PID file not removed after stale stop")
	}
}

// TestRunStop_LivePIDFile launches the fake server then stops it.
func TestRunStop_LivePIDFile(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("Unix-only in Phase 2")
	}
	setupTestEnv(t)
	fakeServer := buildFakeServer(t)
	port := freeTestPort(t)

	sup := lifecycle.NewNohupSupervisor()
	pidPath := lifecycle.PIDPath("")
	logPath := filepath.Join(t.TempDir(), "server.log")
	env := append(os.Environ(), "PORT="+strconv.Itoa(port))
	h, err := sup.Start(context.Background(), lifecycle.StartOptions{
		BinaryPath: fakeServer,
		PIDPath:    pidPath,
		LogPath:    logPath,
		Env:        env,
	})
	if err != nil {
		t.Fatalf("Start fakeServer: %v", err)
	}
	if !waitTCPHealth(port, 3*time.Second) {
		t.Fatalf("fakeServer did not respond to /health")
	}

	if err := cli.RunStop(nil); err != nil {
		t.Fatalf("RunStop on live server: %v", err)
	}
	if _, err := lifecycle.ReadPID(pidPath); err == nil {
		t.Error("PID file not removed after live stop")
	}
	// Server must be dead.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st, _ := sup.Status(h.PID)
		if !st.Running {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("process %d still running after RunStop", h.PID)
}

// TestRunStop_RejectsPositionalArgs covers the args validation branch.
func TestRunStop_RejectsPositionalArgs(t *testing.T) {
	if err := cli.RunStop([]string{"extra"}); err == nil {
		t.Error("expected error for positional argument")
	}
}
