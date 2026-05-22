package lifecycle_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/lifecycle"
)

// TestAcquireSetupLock_Happy acquires the lock once, confirms the lock file
// exists with mode 0600, and that release() succeeds without panic.
func TestAcquireSetupLock_Happy(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("flock not implemented on Windows")
	}
	stateDir := t.TempDir()

	release, err := lifecycle.AcquireSetupLock(stateDir)
	if err != nil {
		t.Fatalf("AcquireSetupLock: %v", err)
	}
	defer release()

	lockPath := filepath.Join(stateDir, "wayneblacktea", "setup.lock")
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("lock file mode = %04o; want 0600", perm)
	}
}

// TestAcquireSetupLock_ErrLockHeld acquires the lock and then tries to acquire
// it again from the same process on a different fd. The second call must
// return ErrLockHeld without blocking (LOCK_NB).
//
// Note: flock(2) on Linux is per-open-file-description; opening the same path
// a second time yields a distinct file description, so LOCK_EX|LOCK_NB will
// return EWOULDBLOCK — mapping to ErrLockHeld.
func TestAcquireSetupLock_ErrLockHeld(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("flock not implemented on Windows")
	}
	stateDir := t.TempDir()

	release1, err := lifecycle.AcquireSetupLock(stateDir)
	if err != nil {
		t.Fatalf("first AcquireSetupLock: %v", err)
	}
	defer release1()

	_, err = lifecycle.AcquireSetupLock(stateDir)
	if !errors.Is(err, lifecycle.ErrLockHeld) {
		t.Errorf("second AcquireSetupLock err = %v; want ErrLockHeld", err)
	}
}

// TestAcquireSetupLock_ReleaseAllowsReAcquire verifies that after calling the
// release func a subsequent AcquireSetupLock succeeds.
func TestAcquireSetupLock_ReleaseAllowsReAcquire(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("flock not implemented on Windows")
	}
	stateDir := t.TempDir()

	release1, err := lifecycle.AcquireSetupLock(stateDir)
	if err != nil {
		t.Fatalf("first AcquireSetupLock: %v", err)
	}
	release1() // explicitly release before second attempt

	release2, err := lifecycle.AcquireSetupLock(stateDir)
	if err != nil {
		t.Fatalf("second AcquireSetupLock after release: %v", err)
	}
	defer release2()
}

// TestAcquireSetupLock_UnwritableDir verifies that an error is returned (not
// panic) when the state directory cannot be created or written.
func TestAcquireSetupLock_UnwritableDir(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("flock not implemented on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere; chmod test not meaningful")
	}
	// Create a read-only parent directory so MkdirAll fails.
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		//nolint:gosec // G302: restoring dir perms to 0700 for cleanup, not creating credentials
		_ = os.Chmod(parent, 0o700)
	})

	_, err := lifecycle.AcquireSetupLock(parent)
	if err == nil {
		t.Error("expected error for unwritable stateDir; got nil")
	}
	if errors.Is(err, lifecycle.ErrLockHeld) {
		t.Error("got ErrLockHeld for unwritable dir; want OS error")
	}
}
