package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
)

const osWindows = "windows"

// freePort returns a port that was just free at the moment of call. There is
// always a TOCTOU race, but for tests that immediately spawn a listener it
// is effectively reliable.
func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr type %T", l.Addr())
	}
	return addr.Port
}

// TestLSofTCPListen_NoOccupier verifies a known-free port returns ErrNoOccupier.
func TestLSofTCPListen_NoOccupier(t *testing.T) {
	port := freePort(t)
	_, err := lifecycle.LSofTCPListen(context.Background(), port)
	if !errors.Is(err, lifecycle.ErrNoOccupier) {
		t.Errorf("expected ErrNoOccupier on free port %d; got %v", port, err)
	}
}

// TestLSofTCPListen_InvalidPort covers boundary input.
func TestLSofTCPListen_InvalidPort(t *testing.T) {
	for _, p := range []int{0, -1, 70000} {
		_, err := lifecycle.LSofTCPListen(context.Background(), p)
		if err == nil {
			t.Errorf("port %d accepted; want error", p)
		}
	}
}

// TestLSofTCPListen_RealListener spawns python3 http.server on a free port
// and verifies LSofTCPListen returns its PID.
func TestLSofTCPListen_RealListener(t *testing.T) {
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not in PATH: %v", err)
	}
	port := freePort(t)
	//nolint:gosec // G204: pyBin from exec.LookPath, fixed args, validated port
	cmd := exec.CommandContext(context.Background(), pyBin, "-m", "http.server", strconv.Itoa(port), "--bind", "127.0.0.1")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		t.Fatalf("start python3: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	// Wait for the listener to be ready.
	if !waitListening(port, 3*time.Second) {
		t.Fatalf("python3 http.server did not bind port %d", port)
	}

	occ, err := lifecycle.LSofTCPListen(context.Background(), port)
	if err != nil {
		t.Fatalf("LSofTCPListen: %v", err)
	}
	if occ.PID != cmd.Process.Pid {
		t.Errorf("Occupier.PID = %d; want %d", occ.PID, cmd.Process.Pid)
	}
	if occ.Command == "" {
		t.Error("Occupier.Command is empty; expected non-empty process name")
	}
}

// TestKillOccupier_TermPath spawns a python3 listener and verifies SIGTERM
// reclaim works end-to-end.
func TestKillOccupier_TermPath(t *testing.T) {
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not in PATH: %v", err)
	}
	port := freePort(t)
	//nolint:gosec // G204: pyBin from exec.LookPath, fixed args, validated port
	cmd := exec.CommandContext(context.Background(), pyBin, "-m", "http.server", strconv.Itoa(port), "--bind", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start python3: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	if !waitListening(port, 3*time.Second) {
		t.Fatalf("python3 http.server did not bind port %d", port)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := lifecycle.KillOccupier(ctx, port, nil); err != nil {
		t.Fatalf("KillOccupier: %v", err)
	}
	_, _ = cmd.Process.Wait()

	// Port must be reclaimable.
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port %d still occupied after KillOccupier: %v", port, err)
	}
	_ = l.Close()
}

// TestKillOccupier_StubbornProcess uses a shell that traps SIGTERM, forcing
// the escalation path to SIGKILL.
func TestKillOccupier_StubbornProcess(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("signal escalation test is Unix-only")
	}
	bashBin, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not in PATH: %v", err)
	}
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not in PATH: %v", err)
	}
	port := freePort(t)
	// trap '' TERM → ignore SIGTERM; exec python3 → bind the port.
	script := fmt.Sprintf(`trap '' TERM; exec %q -m http.server %d --bind 127.0.0.1`, pyBin, port)
	//nolint:gosec // G204: bashBin from exec.LookPath, script is test-controlled fixture
	cmd := exec.CommandContext(context.Background(), bashBin, "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stubborn: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	if !waitListening(port, 5*time.Second) {
		t.Fatalf("stubborn http.server did not bind port %d", port)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := lifecycle.KillOccupier(ctx, port, nil); err != nil {
		t.Fatalf("KillOccupier: %v", err)
	}
	_, _ = cmd.Process.Wait()

	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port %d still occupied after SIGKILL: %v", port, err)
	}
	_ = l.Close()
}

// TestKillOccupier_AlreadyFree is a no-op happy path.
func TestKillOccupier_AlreadyFree(t *testing.T) {
	port := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := lifecycle.KillOccupier(ctx, port, nil); err != nil {
		t.Errorf("KillOccupier on free port: %v", err)
	}
}

// TestLSofTCPListen_BinaryOverride uses LSOF_BIN to point at a fake script
// returning a controlled -F response, exercising the parser without
// depending on the host's lsof behaviour.
func TestLSofTCPListen_BinaryOverride(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("shell fixture requires Unix")
	}
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "fake-lsof.sh")
	script := "#!/bin/sh\ncat <<'EOF'\np42\ncfakecmd\nn127.0.0.1:8080\nEOF\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil { //nolint:gosec // 0700 needed for shell exec in test
		t.Fatalf("WriteFile fake-lsof: %v", err)
	}
	// Ensure executable bit survives umask.
	if err := os.Chmod(fake, fs.FileMode(0o700)); err != nil {
		t.Fatalf("Chmod fake-lsof: %v", err)
	}
	t.Setenv("LSOF_BIN", fake)
	occ, err := lifecycle.LSofTCPListen(context.Background(), 18080)
	if err != nil {
		t.Fatalf("LSofTCPListen with fake-lsof: %v", err)
	}
	if occ.PID != 42 {
		t.Errorf("occ.PID = %d; want 42", occ.PID)
	}
	if occ.Command != "fakecmd" {
		t.Errorf("occ.Command = %q; want fakecmd", occ.Command)
	}
}

// waitListening polls until 127.0.0.1:port accepts a Dial, then closes.
func waitListening(port int, timeout time.Duration) bool {
	d := net.Dialer{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
		cancel()
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
