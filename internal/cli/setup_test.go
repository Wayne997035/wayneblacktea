package cli_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/cli"
	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
)

// buildFakeServer compiles internal/cli/testdata/fakeserver as a side test
// fixture and returns its absolute path. Each test that needs it should
// call this exactly once via t.Helper.
func buildFakeServer(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "fakeserver")
	// Build at package level — go test cwd is the package dir.
	//nolint:gosec // G204: go binary from PATH; fixed args; testdata path under repo
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", out, "./testdata/fakeserver")
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building fakeserver: %v\nout:\n%s", err, out)
	}
	return out
}

// buildFakeClaude compiles a tiny stub that records arguments to a file
// then exits 0. Returns the path to the binary and the path to the file
// that will contain one line per invocation.
func buildFakeClaude(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	logPath := filepath.Join(dir, "calls.log")
	stub := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	f, _ := os.OpenFile(%q, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if f != nil {
		fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
		_ = f.Close()
	}
	os.Exit(0)
}
`, logPath)
	if err := os.WriteFile(src, []byte(stub), 0o600); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	bin := filepath.Join(dir, "fake-claude")
	//nolint:gosec // G204: go binary from PATH; fixed flags; src under t.TempDir()
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", bin, src)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building fake-claude: %v\nout:\n%s", err, out)
	}
	return bin, logPath
}

// setupTestEnv overrides all XDG vars + HOME so the test does not touch
// the real user directories. Also clears WBT_PORT so the test controls
// the port via --port.
func setupTestEnv(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "state"))
	t.Setenv("WBT_PORT", "")
	t.Setenv("WBT_DB_PATH", "")
	t.Setenv("API_KEY", "")
	t.Setenv("PORT", "")
}

// freeTestPort grabs a kernel-assigned free port, releases it, returns
// the number. Same TOCTOU caveat as lifecycle tests.
func freeTestPort(t *testing.T) int {
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

// stopRunningServer reads the PID file at lifecycle.PIDPath("") and stops
// the underlying process so the test does not leak background workers.
// Best-effort: missing PID file is fine.
func stopRunningServer(t *testing.T) {
	t.Helper()
	pidPath := lifecycle.PIDPath("")
	pid, err := lifecycle.ReadPID(pidPath)
	if errors.Is(err, lifecycle.ErrNoPIDFile) {
		return
	}
	if err != nil {
		return
	}
	sup := lifecycle.NewNohupSupervisor()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = sup.Stop(ctx, pid)
	_ = lifecycle.RemovePID(pidPath)
}

// TestRunSetup_PortFree happy path with no port occupier and a stub claude
// binary. Verifies the server spawns, /health responds, MCP register is
// invoked.
func TestRunSetup_PortFree(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("setup is Unix-only in Phase 2")
	}
	setupTestEnv(t)
	fakeServer := buildFakeServer(t)
	fakeClaude, claudeLog := buildFakeClaude(t)
	t.Setenv("CLAUDE_BIN", fakeClaude)
	port := freeTestPort(t)
	t.Cleanup(func() { stopRunningServer(t) })

	if err := cli.RunSetup([]string{
		"--port", strconv.Itoa(port),
		"--server-bin", fakeServer,
		"--mcp-name", "wayneblacktea-test",
	}); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}

	// PID file must exist and point at a running process.
	pidPath := lifecycle.PIDPath("")
	pid, err := lifecycle.ReadPID(pidPath)
	if err != nil {
		t.Fatalf("ReadPID after setup: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("PID file empty after setup")
	}

	// /health must be 200.
	if !waitTCPHealth(port, 2*time.Second) {
		t.Errorf("/health did not return 200 on port %d", port)
	}

	// fake claude must have been invoked with `mcp add wayneblacktea-test ...`.
	b, err := os.ReadFile(claudeLog) //nolint:gosec // G304: log path is under t.TempDir()
	if err != nil {
		t.Fatalf("reading fake-claude log: %v", err)
	}
	if string(b) == "" {
		t.Error("fake-claude was not invoked")
	}
}

// TestRunSetup_PortOccupied_Killable spawns python3 http.server on the
// chosen port, then runs setup. Setup must reclaim the port (SIGTERM)
// and bring its own server up.
func TestRunSetup_PortOccupied_Killable(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("setup is Unix-only in Phase 2")
	}
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not in PATH: %v", err)
	}
	setupTestEnv(t)
	fakeServer := buildFakeServer(t)
	fakeClaude, _ := buildFakeClaude(t)
	t.Setenv("CLAUDE_BIN", fakeClaude)
	port := freeTestPort(t)

	//nolint:gosec // G204: pyBin from exec.LookPath; numeric port validated
	occ := exec.CommandContext(context.Background(), pyBin, "-m", "http.server", strconv.Itoa(port), "--bind", "127.0.0.1")
	if err := occ.Start(); err != nil {
		t.Fatalf("starting occupier: %v", err)
	}
	t.Cleanup(func() {
		_ = occ.Process.Kill()
		_, _ = occ.Process.Wait()
		stopRunningServer(t)
	})
	if !waitTCPListening(port, 3*time.Second) {
		t.Fatalf("occupier did not bind port %d", port)
	}

	if err := cli.RunSetup([]string{
		"--port", strconv.Itoa(port),
		"--server-bin", fakeServer,
		"--no-mcp",
	}); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if !waitTCPHealth(port, 3*time.Second) {
		t.Errorf("/health did not return 200 after port reclaim")
	}
}

// TestRunSetup_PortOccupied_Stubborn occupies the chosen port with a
// process that ignores SIGTERM, forcing the SIGKILL escalation.
func TestRunSetup_PortOccupied_Stubborn(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("setup is Unix-only in Phase 2")
	}
	bashBin, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not in PATH: %v", err)
	}
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 not in PATH: %v", err)
	}
	setupTestEnv(t)
	fakeServer := buildFakeServer(t)
	fakeClaude, _ := buildFakeClaude(t)
	t.Setenv("CLAUDE_BIN", fakeClaude)
	port := freeTestPort(t)
	// trap '' TERM → ignore SIGTERM forever; exec python3 → bind port.
	script := fmt.Sprintf(`trap '' TERM; exec %q -m http.server %d --bind 127.0.0.1`, pyBin, port)
	//nolint:gosec // G204: bashBin from exec.LookPath, script is test-controlled
	occ := exec.CommandContext(context.Background(), bashBin, "-c", script)
	if err := occ.Start(); err != nil {
		t.Fatalf("starting stubborn occupier: %v", err)
	}
	t.Cleanup(func() {
		_ = occ.Process.Kill()
		_, _ = occ.Process.Wait()
		stopRunningServer(t)
	})
	if !waitTCPListening(port, 5*time.Second) {
		t.Fatalf("stubborn occupier did not bind port %d", port)
	}

	if err := cli.RunSetup([]string{
		"--port", strconv.Itoa(port),
		"--server-bin", fakeServer,
		"--no-mcp",
	}); err != nil {
		t.Fatalf("RunSetup with stubborn occupier: %v", err)
	}
	if !waitTCPHealth(port, 3*time.Second) {
		t.Errorf("/health did not return 200 after SIGKILL reclaim")
	}
}

// TestRunSetup_ReuseRunning starts the fake server manually with a fresh
// PID file, then asks RunSetup to handle the same port. Expect reuse:
// no kill, no respawn.
func TestRunSetup_ReuseRunning(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("setup is Unix-only in Phase 2")
	}
	setupTestEnv(t)
	fakeServer := buildFakeServer(t)
	fakeClaude, _ := buildFakeClaude(t)
	t.Setenv("CLAUDE_BIN", fakeClaude)
	port := freeTestPort(t)
	t.Cleanup(func() { stopRunningServer(t) })

	// Spawn the fake server directly via the lifecycle supervisor so PID
	// file is consistent with what RunSetup would write.
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
		t.Fatalf("fakeServer did not become healthy")
	}
	originalPID := h.PID

	if err := cli.RunSetup([]string{
		"--port", strconv.Itoa(port),
		"--server-bin", fakeServer,
		"--no-mcp",
	}); err != nil {
		t.Fatalf("RunSetup reuse: %v", err)
	}
	// PID file should be unchanged.
	got, err := lifecycle.ReadPID(pidPath)
	if err != nil {
		t.Fatalf("ReadPID after reuse: %v", err)
	}
	if got != originalPID {
		t.Errorf("PID changed after reuse: got %d, want %d", got, originalPID)
	}
}

// TestRunSetup_StalePID writes a PID file referencing a dead PID and
// verifies setup overwrites it with a fresh spawn.
func TestRunSetup_StalePID(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("setup is Unix-only in Phase 2")
	}
	setupTestEnv(t)
	fakeServer := buildFakeServer(t)
	fakeClaude, _ := buildFakeClaude(t)
	t.Setenv("CLAUDE_BIN", fakeClaude)
	port := freeTestPort(t)
	t.Cleanup(func() { stopRunningServer(t) })

	pidPath := lifecycle.PIDPath("")
	// Compute a likely-dead PID by spawning + killing a short process.
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep binary not in PATH: %v", err)
	}
	//nolint:gosec // G204: sleepBin from exec.LookPath
	dead := exec.CommandContext(context.Background(), sleepBin, "0.01")
	if err := dead.Run(); err != nil {
		t.Fatalf("starting throwaway sleep: %v", err)
	}
	stalePID := dead.Process.Pid
	if err := lifecycle.WritePID(pidPath, stalePID); err != nil {
		t.Fatalf("WritePID stale: %v", err)
	}

	if err := cli.RunSetup([]string{
		"--port", strconv.Itoa(port),
		"--server-bin", fakeServer,
		"--no-mcp",
	}); err != nil {
		t.Fatalf("RunSetup with stale PID: %v", err)
	}
	got, err := lifecycle.ReadPID(pidPath)
	if err != nil {
		t.Fatalf("ReadPID after stale setup: %v", err)
	}
	if got == stalePID {
		t.Errorf("PID file still references stale pid %d", stalePID)
	}
}

// TestRunSetup_NoClaude verifies the graceful copy-paste fallback when
// CLAUDE_BIN does not resolve. Setup must still complete.
//
// We must keep lsof in PATH (Mac /usr/sbin/lsof, Linux /usr/bin/lsof) so
// the port-reclaim step does not error; only `claude` should be missing.
// To achieve that we create a sandbox PATH that includes the system
// tools we depend on but excludes claude entirely.
func TestRunSetup_NoClaude(t *testing.T) {
	if runtime.GOOS == osWindowsName {
		t.Skip("setup is Unix-only in Phase 2")
	}
	setupTestEnv(t)
	fakeServer := buildFakeServer(t)

	// Build a PATH that contains lsof/ss but NOT claude.
	sandboxBin := t.TempDir()
	for _, tool := range []string{"lsof", "ss", "sh", "sleep"} {
		if src, err := exec.LookPath(tool); err == nil {
			link := filepath.Join(sandboxBin, tool)
			if err := os.Symlink(src, link); err != nil {
				t.Fatalf("symlink %s: %v", tool, err)
			}
		}
	}
	t.Setenv("CLAUDE_BIN", "")
	t.Setenv("PATH", sandboxBin)
	port := freeTestPort(t)
	t.Cleanup(func() { stopRunningServer(t) })

	if err := cli.RunSetup([]string{
		"--port", strconv.Itoa(port),
		"--server-bin", fakeServer,
	}); err != nil {
		t.Fatalf("RunSetup without claude: %v", err)
	}
	if !waitTCPHealth(port, 3*time.Second) {
		t.Error("server did not become healthy without claude CLI")
	}
}

// waitTCPHealth polls /health on the given port until success or timeout.
func waitTCPHealth(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cli.HealthOK(port) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// waitTCPListening polls a raw TCP connection attempt.
func waitTCPListening(port int, timeout time.Duration) bool {
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
