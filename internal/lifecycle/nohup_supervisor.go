//go:build unix

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// NohupSupervisor spawns a binary detached from the parent terminal so it
// survives the wbt CLI exiting. The detachment is achieved with
// syscall.SysProcAttr{Setsid: true}: the child becomes its own session
// leader, ignoring the controlling terminal's SIGHUP.
//
// stdout + stderr are redirected to a log file with mode 0600. The log
// file's parent directory is created with mode 0700.
type NohupSupervisor struct {
	// TermGracePeriod is how long Stop() waits for the process to exit
	// after SIGTERM before escalating to SIGKILL. Defaults to 5s when zero.
	TermGracePeriod time.Duration
}

// NewNohupSupervisor returns a NohupSupervisor with sensible defaults.
func NewNohupSupervisor() *NohupSupervisor {
	return &NohupSupervisor{TermGracePeriod: 5 * time.Second}
}

// Start launches opts.BinaryPath in the background. On success the PID
// file is written atomically and a Handle is returned.
func (s *NohupSupervisor) Start(ctx context.Context, opts StartOptions) (*Handle, error) {
	if err := validateStartOptions(opts); err != nil {
		return nil, err
	}
	logFile, err := openLogFile(opts.LogPath)
	if err != nil {
		return nil, err
	}
	// We do NOT defer logFile.Close(): the child inherits it as fd 1+2 and
	// continues using it. Closing here would break the redirection.
	// The fd in the parent process is released on parent exit anyway.

	// ctx is intentionally only used to abort the spawn step itself. Once
	// the child is started we Release() it: cancellation of the parent
	// context must NOT kill the detached server. exec.CommandContext with
	// the parent ctx would tie process lifetime to ctx, which is the
	// opposite of what we want. We therefore pass context.Background() to
	// satisfy noctx while honouring the spec ("ctx applies to spawn, not
	// to child lifetime") explicitly.
	if err := ctx.Err(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("Start cancelled before spawn: %w", err)
	}
	//nolint:gosec,contextcheck // G204: BinaryPath via exec.LookPath; ctx must NOT be inherited (detached server must outlive caller)
	cmd := exec.CommandContext(context.Background(), opts.BinaryPath, opts.Args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	} else {
		cmd.Env = os.Environ()
	}
	// Setsid detaches the child from the controlling terminal: it becomes
	// its own process group + session leader, so SIGHUP/SIGINT directed at
	// the parent's terminal does not reach it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("starting %s: %w", opts.BinaryPath, err)
	}
	// Snapshot the PID before Release() — os.Process.Release() resets the
	// internal Pid to -1 once it's called.
	pid := cmd.Process.Pid

	// Close our copy of the log file fd; the child has its own dup.
	if err := logFile.Close(); err != nil {
		// Non-fatal: the child still owns the dup. Continue.
		// (We deliberately do not wrap+return — child is already running.)
		_ = err
	}

	// Release the child so we don't leave a zombie when wbt exits.
	if err := cmd.Process.Release(); err != nil {
		// Best-effort: zombie reaping is the OS init's job once wbt exits.
		_ = err
	}

	if err := WritePID(opts.PIDPath, pid); err != nil {
		// PID file write failed; do not kill the child (caller may still
		// want it). Return the error so caller can decide.
		return nil, err
	}
	return &Handle{
		PID:       pid,
		LogPath:   opts.LogPath,
		StartedAt: time.Now().UTC(),
	}, nil
}

// Stop sends SIGTERM, waits up to TermGracePeriod, then SIGKILL.
//
// Implementation note: after each signal we attempt a non-blocking wait4
// reap. In production wbt-stop is not the parent of the server process
// (the server is detached via setsid, then orphaned to init/launchd when
// the wbt CLI that spawned it exits) so wait4 is a no-op. But if Stop is
// called from the same process that did the Start — common in tests and
// in the future "restart in-place" flow — wait4 prevents a zombie.
func (s *NohupSupervisor) Stop(ctx context.Context, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	grace := s.TermGracePeriod
	if grace <= 0 {
		grace = 5 * time.Second
	}
	// SIGTERM first.
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // already dead
		}
		return fmt.Errorf("SIGTERM pid %d: %w", pid, err)
	}
	// Wait for it to die, polling every 100ms up to grace period.
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if dead := nonblockingReapAndCheck(pid); dead {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("Stop cancelled while waiting for SIGTERM: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Escalate to SIGKILL.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("SIGKILL pid %d: %w", pid, err)
	}
	// Brief wait for kernel to reap; not fatal if still showing alive.
	for i := 0; i < 10; i++ {
		if dead := nonblockingReapAndCheck(pid); dead {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

// nonblockingReapAndCheck does a WNOHANG wait4 (in case we're the parent),
// then a kill-0 existence check. Returns true when the process is gone.
// On systems where the process is a zombie that we're not the parent of,
// this gracefully degrades to the kill-0 check.
func nonblockingReapAndCheck(pid int) bool {
	// Best-effort reap; ignore errors (most likely ECHILD when we're not
	// the parent, which is the expected production case).
	_ = reapNonblocking(pid)
	alive, err := processAlive(pid)
	if err != nil {
		return false
	}
	return !alive
}

// Status reports liveness via the platform processAlive helper.
func (s *NohupSupervisor) Status(pid int) (Status, error) {
	if pid <= 0 {
		return Status{}, fmt.Errorf("invalid pid %d", pid)
	}
	alive, err := processAlive(pid)
	if err != nil {
		return Status{}, err
	}
	return Status{Running: alive, PID: pid}, nil
}

// validateStartOptions enforces required fields before spawn.
func validateStartOptions(opts StartOptions) error {
	if opts.BinaryPath == "" {
		return errors.New("StartOptions.BinaryPath is required")
	}
	if opts.LogPath == "" {
		return errors.New("StartOptions.LogPath is required")
	}
	if opts.PIDPath == "" {
		return errors.New("StartOptions.PIDPath is required")
	}
	if !filepath.IsAbs(opts.BinaryPath) {
		return fmt.Errorf("BinaryPath must be absolute: %q", opts.BinaryPath)
	}
	return nil
}

// openLogFile opens or creates the log file in append mode with 0600 perms.
// Parent directory is created with 0700 if absent.
func openLogFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating log dir %s: %w", dir, err)
	}
	//nolint:gosec // G304: path comes from caller (XDG-derived StartOptions.LogPath), not user input
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening log file %s: %w", path, err)
	}
	return f, nil
}
