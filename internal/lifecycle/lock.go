//go:build !windows

package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrLockHeld is returned by AcquireSetupLock when another wbt setup is
// already running and holds the exclusive lock.
var ErrLockHeld = errors.New("another wbt setup is already running")

// AcquireSetupLock acquires an exclusive non-blocking flock on
// $stateDir/wayneblacktea/setup.lock. The kernel releases the lock
// automatically if the process is killed (SIGKILL, crash, etc.), so no
// cleanup is needed on abnormal exit.
//
// On success it returns a release func that unlocks + closes the file.
// Callers MUST defer the returned func when err == nil.
//
// Returns ErrLockHeld if another process holds the lock.
// Returns any other OS error (wrapped) so the caller can decide whether to
// proceed without a lock (e.g. errors.ErrUnsupported from the Windows stub).
func AcquireSetupLock(stateDir string) (release func(), err error) {
	dir := filepath.Join(stateDir, "wayneblacktea")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return func() {}, fmt.Errorf("setup lock dir: %w", err)
	}
	path := filepath.Join(dir, "setup.lock")
	//nolint:gosec // G304: path derived from XDG stateDir (caller-controlled), not user input
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, fmt.Errorf("setup lock file: %w", err)
	}
	//nolint:gosec // G115: fd fits in int on all supported Unix platforms (64-bit and 32-bit)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return func() {}, ErrLockHeld
		}
		return func() {}, fmt.Errorf("flock setup.lock: %w", err)
	}
	return func() {
		//nolint:gosec // G115: fd fits in int on all supported Unix platforms (64-bit and 32-bit)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
