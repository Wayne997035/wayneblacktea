package lifecycle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNoPIDFile signals that the PID file does not exist on disk. Callers
// typically treat this as "server already stopped" rather than an error.
var ErrNoPIDFile = errors.New("pid file not found")

// PIDPath resolves the canonical path of the server PID file:
//
//	$XDG_STATE_HOME/wayneblacktea/server.pid     — if XDG_STATE_HOME is set
//	$HOME/.local/state/wayneblacktea/server.pid  — otherwise
//
// workspace is a reserved future hook (per SA spec) for namespacing the PID
// file when multiple workspaces share one wbt installation. workspace="" —
// the only currently-supported value — yields the plain server.pid.
// A non-empty workspace would map to server.<workspace>.pid; until the
// caller side accepts a workspace flag we return the same plain file so
// nothing changes today, but the resolver is hardened against accidental
// path-traversal in the workspace label.
func PIDPath(workspace string) string {
	base := stateDir()
	dir := filepath.Join(base, "wayneblacktea")
	if workspace == "" {
		return filepath.Join(dir, "server.pid")
	}
	clean := sanitizeWorkspace(workspace)
	if clean == "" {
		return filepath.Join(dir, "server.pid")
	}
	return filepath.Join(dir, "server."+clean+".pid")
}

// stateDir returns the XDG state home, with a $HOME/.local/state fallback.
func stateDir() string {
	if v := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); v != "" {
		return v
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "state")
	}
	return filepath.Join(".", ".local", "state")
}

// sanitizeWorkspace strips path separators / dots / control chars so a
// caller-supplied workspace label cannot escape the lifecycle directory.
func sanitizeWorkspace(workspace string) string {
	// Only allow [a-zA-Z0-9_-]; everything else is dropped.
	var b strings.Builder
	for _, r := range workspace {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
			b.WriteRune(r)
		default:
			// drop
		}
	}
	return b.String()
}

// WritePID writes pid to path atomically: write a sibling .tmp file, then
// rename over the destination. Parent directory is created with mode 0700
// if absent. The PID file itself is mode 0600 because it is read by the
// owning user and contains information about a long-running process.
func WritePID(path string, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating pid file dir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing tmp pid file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort cleanup: tmp file remains otherwise.
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming pid file %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// ReadPID reads and parses the PID file at path. Returns ErrNoPIDFile when
// the file does not exist; any other I/O or parse failure is returned
// wrapped so callers can decide whether to treat it as stale.
func ReadPID(path string) (int, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: path is constructed from PIDPath() (XDG-derived) by callers, not from user input
	if errors.Is(err, os.ErrNotExist) {
		return 0, ErrNoPIDFile
	}
	if err != nil {
		return 0, fmt.Errorf("reading pid file %s: %w", path, err)
	}
	raw := strings.TrimSpace(string(b))
	if raw == "" {
		return 0, fmt.Errorf("pid file %s is empty", path)
	}
	pid, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parsing pid file %s (%q): %w", path, raw, err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("pid file %s contained non-positive pid %d", path, pid)
	}
	return pid, nil
}

// RemovePID removes the PID file if present. Missing file is not an error
// (idempotent — calling Stop twice should succeed).
func RemovePID(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("removing pid file %s: %w", path, err)
}

// IsStale reports whether the PID file references a process that no longer
// exists. Returns:
//
//   - (true, nil)  if the file is missing or holds a dead PID
//   - (false, nil) if the PID is alive
//   - (false, err) if the file is corrupt and the caller must decide
//
// The liveness probe is the platform-specific processAlive helper (signal 0
// on Unix; stub on Windows).
func IsStale(path string) (bool, error) {
	pid, err := ReadPID(path)
	if errors.Is(err, ErrNoPIDFile) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	alive, err := processAlive(pid)
	if err != nil {
		return false, err
	}
	return !alive, nil
}
