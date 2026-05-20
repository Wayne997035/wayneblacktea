//go:build unix

package lifecycle_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
)

// TestNohupSupervisor_StartStop spawns `sleep 30` via the supervisor, waits
// for the PID file to materialise, calls Stop, and verifies the process is
// gone.
func TestNohupSupervisor_StartStop(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep binary not in PATH: %v", err)
	}
	tmp := t.TempDir()
	pidPath := filepath.Join(tmp, "server.pid")
	logPath := filepath.Join(tmp, "server.log")

	sup := lifecycle.NewNohupSupervisor()
	sup.TermGracePeriod = 2 * time.Second
	ctx := context.Background()

	h := startSleep(t, sup, sleepBin, pidPath, logPath)
	verifyPIDFile(t, pidPath, h.PID)
	verifyLogMode(t, logPath)
	verifyRunning(t, sup, h.PID)

	if err := sup.Stop(ctx, h.PID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	verifyStopped(t, sup, h.PID)

	if err := lifecycle.RemovePID(pidPath); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}
}

func startSleep(t *testing.T, sup *lifecycle.NohupSupervisor, bin, pidPath, logPath string) *lifecycle.Handle {
	t.Helper()
	h, err := sup.Start(context.Background(), lifecycle.StartOptions{
		BinaryPath: bin,
		Args:       []string{"30"},
		PIDPath:    pidPath,
		LogPath:    logPath,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.PID <= 0 {
		t.Fatalf("Handle.PID = %d; want positive", h.PID)
	}
	return h
}

func verifyPIDFile(t *testing.T, path string, want int) {
	t.Helper()
	got, err := lifecycle.ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if got != want {
		t.Errorf("PID file %d; want %d", got, want)
	}
}

func verifyLogMode(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("log file mode = %o; want 0600", perm)
	}
}

func verifyRunning(t *testing.T, sup *lifecycle.NohupSupervisor, pid int) {
	t.Helper()
	status, err := sup.Status(pid)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Running {
		t.Errorf("Status.Running = false; want true")
	}
}

func verifyStopped(t *testing.T, sup *lifecycle.NohupSupervisor, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := sup.Status(pid)
		if err != nil {
			t.Fatalf("Status after Stop: %v", err)
		}
		if !status.Running {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("Status.Running = true after Stop")
}

// TestNohupSupervisor_RejectsBadOptions covers validateStartOptions branches.
func TestNohupSupervisor_RejectsBadOptions(t *testing.T) {
	sup := lifecycle.NewNohupSupervisor()
	ctx := context.Background()

	cases := []struct {
		name string
		opts lifecycle.StartOptions
	}{
		{
			name: "missing BinaryPath",
			opts: lifecycle.StartOptions{LogPath: "/tmp/a.log", PIDPath: "/tmp/a.pid"},
		},
		{
			name: "missing LogPath",
			opts: lifecycle.StartOptions{BinaryPath: "/bin/sleep", PIDPath: "/tmp/a.pid"},
		},
		{
			name: "missing PIDPath",
			opts: lifecycle.StartOptions{BinaryPath: "/bin/sleep", LogPath: "/tmp/a.log"},
		},
		{
			name: "relative BinaryPath",
			opts: lifecycle.StartOptions{
				BinaryPath: "./sleep", LogPath: "/tmp/a.log", PIDPath: "/tmp/a.pid",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sup.Start(ctx, tc.opts)
			if err == nil {
				t.Errorf("expected error for %s; got nil", tc.name)
			}
		})
	}
}

// TestNohupSupervisor_StopInvalidPID rejects non-positive PIDs.
func TestNohupSupervisor_StopInvalidPID(t *testing.T) {
	sup := lifecycle.NewNohupSupervisor()
	if err := sup.Stop(context.Background(), 0); err == nil {
		t.Error("Stop(0) accepted; want error")
	}
}

// TestNohupSupervisor_StatusDeadProcess returns Running=false for a known-
// dead PID without error.
func TestNohupSupervisor_StatusDeadProcess(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep binary not in PATH: %v", err)
	}
	//nolint:gosec // G204: sleepBin from exec.LookPath; fixed numeric arg
	cmd := exec.CommandContext(context.Background(), sleepBin, "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_, _ = cmd.Process.Wait()
	// Give the kernel a moment to reap.
	time.Sleep(100 * time.Millisecond)

	sup := lifecycle.NewNohupSupervisor()
	status, err := sup.Status(pid)
	if err != nil {
		t.Fatalf("Status on dead pid: %v", err)
	}
	if status.Running {
		t.Errorf("Status.Running = true for dead pid %d", pid)
	}
}

// TestLaunchdSupervisor_StubReturnsUnsupported documents the Phase 3 stub.
func TestLaunchdSupervisor_StubReturnsUnsupported(t *testing.T) {
	sup := lifecycle.NewLaunchdSupervisor()
	if _, err := sup.Start(context.Background(), lifecycle.StartOptions{}); err == nil {
		t.Error("LaunchdSupervisor.Start did not return error")
	}
	if err := sup.Stop(context.Background(), 1); err == nil {
		t.Error("LaunchdSupervisor.Stop did not return error")
	}
	if _, err := sup.Status(1); err == nil {
		t.Error("LaunchdSupervisor.Status did not return error")
	}
}
