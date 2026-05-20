package lifecycle_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
)

// TestPIDPath_XDGStateHome verifies the resolver honours XDG_STATE_HOME and
// falls back to $HOME/.local/state when XDG_STATE_HOME is unset.
func TestPIDPath_XDGStateHome(t *testing.T) {
	t.Run("XDG_STATE_HOME set", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_STATE_HOME", tmp)
		got := lifecycle.PIDPath("")
		want := filepath.Join(tmp, "wayneblacktea", "server.pid")
		if got != want {
			t.Errorf("PIDPath() = %q; want %q", got, want)
		}
	})

	t.Run("falls back to $HOME/.local/state", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", tmp)
		got := lifecycle.PIDPath("")
		want := filepath.Join(tmp, ".local", "state", "wayneblacktea", "server.pid")
		if got != want {
			t.Errorf("PIDPath() = %q; want %q", got, want)
		}
	})

	t.Run("workspace suffix sanitised", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("XDG_STATE_HOME", tmp)
		// Path-traversal attempt + control chars must be stripped.
		got := lifecycle.PIDPath("../evil/ws-1")
		want := filepath.Join(tmp, "wayneblacktea", "server.evilws-1.pid")
		if got != want {
			t.Errorf("PIDPath(%q) = %q; want %q", "../evil/ws-1", got, want)
		}
	})
}

// TestWriteReadPID_RoundTrip verifies basic write/read symmetry and that the
// resulting file is exactly mode 0600 with a trailing newline.
func TestWriteReadPID_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.pid")

	if err := lifecycle.WritePID(path, 12345); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("pid file mode = %o; want 0600", perm)
	}
	got, err := lifecycle.ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if got != 12345 {
		t.Errorf("ReadPID() = %d; want 12345", got)
	}
}

// TestReadPID_Errors covers every parse/IO failure mode.
func TestReadPID_Errors(t *testing.T) {
	tmp := t.TempDir()

	t.Run("missing file yields ErrNoPIDFile", func(t *testing.T) {
		_, err := lifecycle.ReadPID(filepath.Join(tmp, "absent.pid"))
		if !errors.Is(err, lifecycle.ErrNoPIDFile) {
			t.Errorf("expected ErrNoPIDFile; got %v", err)
		}
	})

	t.Run("empty file is rejected", func(t *testing.T) {
		p := filepath.Join(tmp, "empty.pid")
		if err := os.WriteFile(p, []byte("   \n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := lifecycle.ReadPID(p)
		if err == nil {
			t.Fatal("expected error reading empty pid file; got nil")
		}
	})

	t.Run("non-numeric file is rejected", func(t *testing.T) {
		p := filepath.Join(tmp, "junk.pid")
		if err := os.WriteFile(p, []byte("not-a-number"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := lifecycle.ReadPID(p)
		if err == nil {
			t.Fatal("expected error reading non-numeric pid file; got nil")
		}
	})

	t.Run("zero pid is rejected", func(t *testing.T) {
		p := filepath.Join(tmp, "zero.pid")
		if err := os.WriteFile(p, []byte("0"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		_, err := lifecycle.ReadPID(p)
		if err == nil {
			t.Fatal("expected error reading pid=0; got nil")
		}
	})
}

// TestIsStale_DeadProcess spawns `sleep 30`, kills it, and verifies the PID
// file is reported stale via signal-0 detection.
func TestIsStale_DeadProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("processAlive is a stub on Windows")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.pid")

	// Spawn a child we control.
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("sleep binary not in PATH: %v", err)
	}
	cmd := exec.CommandContext(context.Background(), sleepBin, "30") //nolint:gosec // G204: sleepBin from exec.LookPath, fixed arg
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	pid := cmd.Process.Pid
	if err := lifecycle.WritePID(path, pid); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	stale, err := lifecycle.IsStale(path)
	if err != nil {
		t.Fatalf("IsStale (alive): %v", err)
	}
	if stale {
		t.Errorf("IsStale on live pid %d = true; want false", pid)
	}

	// Now kill the child and verify staleness flips.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_, _ = cmd.Process.Wait() // reap

	// Process exits asynchronously; poll up to 1s.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		stale, err = lifecycle.IsStale(path)
		if err != nil {
			t.Fatalf("IsStale (dead): %v", err)
		}
		if stale {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !stale {
		t.Errorf("IsStale on dead pid %d = false; want true", pid)
	}
}

// TestIsStale_MissingFile reports stale when the file does not exist.
func TestIsStale_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	stale, err := lifecycle.IsStale(filepath.Join(tmp, "absent.pid"))
	if err != nil {
		t.Fatalf("IsStale on missing file: %v", err)
	}
	if !stale {
		t.Error("IsStale on missing file = false; want true")
	}
}

// TestRemovePID_Idempotent verifies multiple removes do not error.
func TestRemovePID_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.pid")

	if err := lifecycle.WritePID(path, 12345); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	if err := lifecycle.RemovePID(path); err != nil {
		t.Fatalf("first RemovePID: %v", err)
	}
	// Second remove on absent file must succeed (idempotent).
	if err := lifecycle.RemovePID(path); err != nil {
		t.Fatalf("second RemovePID: %v", err)
	}
}

// TestWritePID_RejectsNonPositive verifies pid validation.
func TestWritePID_RejectsNonPositive(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "x.pid")
	if err := lifecycle.WritePID(path, 0); err == nil {
		t.Error("WritePID(0) accepted; want error")
	}
	if err := lifecycle.WritePID(path, -1); err == nil {
		t.Error("WritePID(-1) accepted; want error")
	}
}

// TestWritePID_AtomicTrailingNewline verifies the on-disk format.
func TestWritePID_AtomicTrailingNewline(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "server.pid")
	if err := lifecycle.WritePID(path, 999); err != nil {
		t.Fatalf("WritePID: %v", err)
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: path is under t.TempDir()
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasSuffix(string(b), "\n") {
		t.Errorf("pid file %q does not end with newline", string(b))
	}
	// Round-trip via strconv to confirm format.
	parsed, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || parsed != 999 {
		t.Errorf("parsed pid = %d (err %v); want 999", parsed, err)
	}
}
