// Package lifecycle provides process supervision primitives for the wbt CLI:
// spawn a server process in the background, write a PID file, query status,
// and stop it cleanly. It also offers port-occupier discovery + kill helpers
// so `wbt setup` can reclaim a bound TCP port from a stale predecessor.
//
// Two Supervisor implementations are provided:
//
//   - NohupSupervisor (default, Unix): forks the binary with `setsid` so it
//     becomes its own session leader and survives parent shutdown. Stdout +
//     stderr go to a log file with mode 0600.
//   - LaunchdSupervisor (stub): returns errors.ErrUnsupported. A real
//     implementation will arrive in a future phase that writes a LaunchAgent
//     plist and uses launchctl bootstrap/bootout.
//
// All implementations satisfy the Supervisor interface so callers
// (internal/cli/setup.go) can swap them without code changes.
package lifecycle

import (
	"context"
	"time"
)

// Supervisor manages a single long-running child process: spawning it,
// querying liveness, and stopping it. Implementations must be safe to call
// from a CLI binary (no shared mutable state across processes).
type Supervisor interface {
	// Start launches the configured binary in the background. On success it
	// writes the PID file at opts.PIDPath and returns a Handle. The returned
	// context error wrapping is implementation-defined; callers should treat
	// any non-nil error as fatal (no PID file is written on failure).
	Start(ctx context.Context, opts StartOptions) (*Handle, error)

	// Stop terminates the process identified by pid. Implementations should
	// send SIGTERM first, wait, then SIGKILL if the process is still alive.
	// Removing the PID file is the caller's responsibility (see PIDFile.Remove).
	Stop(ctx context.Context, pid int) error

	// Status reports whether a process with the given pid is alive. An error
	// indicates the status check itself failed (permission denied, etc.); a
	// dead process is reported as Running=false with a nil error.
	Status(pid int) (Status, error)
}

// StartOptions configures a Supervisor.Start call.
type StartOptions struct {
	// BinaryPath is the absolute path to the executable. The caller is
	// responsible for resolving via exec.LookPath before calling Start; the
	// supervisor does not search $PATH.
	BinaryPath string

	// LogPath is the absolute path where stdout + stderr are appended.
	// Created with mode 0600 if absent; parent dir is created with 0700.
	LogPath string

	// PIDPath is the absolute path of the PID file. Written atomically
	// (tmp file + rename) so concurrent readers never observe a partial
	// write. Parent dir is created with 0700.
	PIDPath string

	// Args are the command-line arguments (NOT including argv[0]).
	Args []string

	// Env is the explicit environment for the child process. If empty the
	// supervisor falls back to os.Environ() so test harnesses can inject
	// XDG_DATA_HOME and similar without listing every parent variable.
	Env []string
}

// Handle is returned by Supervisor.Start to identify the spawned process.
type Handle struct {
	PID       int
	LogPath   string
	StartedAt time.Time
}

// Status describes whether a previously-spawned process is still alive.
type Status struct {
	Running bool
	PID     int
}
