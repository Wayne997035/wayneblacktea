//go:build unix

package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// osGetenv is wired here (Unix) and overridden as needed; lifecycle has its
// own package-local indirection so tests can swap behaviour without
// touching os.Getenv globally.
func osGetenv(key string) string { return os.Getenv(key) }

// reapNonblocking attempts a WNOHANG wait4 for pid. Returns nil whether or
// not the child was reaped — this is a best-effort routine. ECHILD means
// pid is not our child (the common production case for a detached server)
// and is silently swallowed.
func reapNonblocking(pid int) error {
	var status syscall.WaitStatus
	_, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.ECHILD) {
		return nil
	}
	return fmt.Errorf("wait4(%d, WNOHANG): %w", pid, err)
}

// killProcess sends SIGTERM (or SIGKILL when force=true) to pid. ESRCH
// (already dead) is swallowed so callers can re-issue safely.
func killProcess(pid int, force bool) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("kill(%d, %v): %w", pid, sig, err)
	}
	return nil
}

// processAlive reports whether a process with the given pid exists by
// sending signal 0 (no actual signal; only existence check). Returns:
//
//   - (true, nil)   process exists
//   - (false, nil)  process does not exist (ESRCH) or we caught a stale entry
//   - (false, err)  unexpected error (permission denied is wrapped, not consumed)
//
// Per kill(2): "Sig=0 — error checking is performed, but no signal is sent."
// ESRCH = no such process. EPERM = process exists but we lack permission to
// signal it, which still confirms it is alive.
func processAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("invalid pid %d", pid)
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if errors.Is(err, syscall.EPERM) {
		// Process exists; we just can't signal it.
		return true, nil
	}
	return false, fmt.Errorf("kill(%d, 0): %w", pid, err)
}
