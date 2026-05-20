package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Occupier describes the process currently listening on a TCP port.
type Occupier struct {
	PID     int
	Command string // best-effort process name
}

// ErrNoOccupier signals that no process is listening on the queried port.
var ErrNoOccupier = errors.New("no process listening on port")

// LSofTCPListen identifies the process listening on the given TCP port by
// shelling out to `lsof -nP -iTCP:<port> -sTCP:LISTEN -F pcn` and parsing
// the structured output. On systems without lsof (some Linux containers)
// it falls back to `ss -lntp`. The context bounds the external command
// runtime; callers without a deadline can pass context.Background() and
// rely on the internal 3s safety timeout.
//
// Returns:
//
//   - (*Occupier, nil)  on success
//   - (nil, ErrNoOccupier) when no process is listening (clean port)
//   - (nil, err)        on parse or command failure
//
// The path to the lsof binary may be overridden with the LSOF_BIN env var
// for tests only — never set from user input.
func LSofTCPListen(ctx context.Context, port int) (*Occupier, error) {
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	occ, err := tryLSof(ctx, port)
	if err == nil {
		return occ, nil
	}
	if errors.Is(err, ErrNoOccupier) {
		return nil, err
	}
	// lsof missing or failed; try ss as a fallback.
	occ, fallbackErr := tryLSS(ctx, port)
	if fallbackErr == nil {
		return occ, nil
	}
	if errors.Is(fallbackErr, ErrNoOccupier) {
		return nil, fallbackErr
	}
	// Surface the primary lsof error along with the fallback context.
	return nil, fmt.Errorf("lsof: %w (fallback ss: %v)", err, fallbackErr)
}

// tryLSof runs lsof and parses its -F output.
//
// `lsof -nP -iTCP:<port> -sTCP:LISTEN -F pcn` returns lines starting with:
//
//	p<PID>
//	c<command>
//	n<address>
//
// We accept the first PID-command pair we see.
func tryLSof(parent context.Context, port int) (*Occupier, error) {
	bin := lookupBin("LSOF_BIN", "lsof")
	if bin == "" {
		return nil, errors.New("lsof not found in PATH")
	}
	// 3s safety timeout: lsof can stall on slow filesystems if invoked under load.
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	//nolint:gosec // G204: bin is resolved via lookupBin (LSOF_BIN test override or PATH); arg is numeric port we validated
	cmd := exec.CommandContext(ctx, bin, "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-F", "pcn")
	out, err := cmd.Output()
	if err != nil {
		// lsof exit code 1 means "no matching open files" — clean port.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, ErrNoOccupier
		}
		return nil, fmt.Errorf("running lsof: %w", err)
	}
	return parseLSofOutput(string(out))
}

// parseLSofOutput walks the -F record stream and returns the first
// PID+command pair encountered. Exposed for tests.
func parseLSofOutput(s string) (*Occupier, error) {
	var pid int
	var cmd string
	for _, line := range strings.Split(s, "\n") {
		if len(line) < 2 {
			continue
		}
		key, value := line[0], line[1:]
		switch key {
		case 'p':
			p, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("parsing lsof pid %q: %w", value, err)
			}
			pid = p
		case 'c':
			cmd = value
		case 'n':
			// address line — confirms we're on a record block we want
		default:
			// ignore
		}
	}
	if pid == 0 {
		return nil, ErrNoOccupier
	}
	return &Occupier{PID: pid, Command: cmd}, nil
}

// tryLSS shells out to `ss -lntp` and parses the line for the given port.
// Used as fallback when lsof is unavailable.
func tryLSS(parent context.Context, port int) (*Occupier, error) {
	bin := lookupBin("SS_BIN", "ss")
	if bin == "" {
		return nil, errors.New("ss not found in PATH")
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	//nolint:gosec // G204: bin is resolved via lookupBin; flag args are fixed literals
	cmd := exec.CommandContext(ctx, bin, "-lntp")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running ss: %w", err)
	}
	return parseLSSOutput(string(out), port)
}

// parseLSSOutput finds the line for the given port and extracts the
// users:(("name",pid=<N>,...)) suffix.
func parseLSSOutput(s string, port int) (*Occupier, error) {
	needle := fmt.Sprintf(":%d ", port)
	for _, line := range strings.Split(s, "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		// Example: LISTEN 0 128 *:8080 *:* users:(("python3",pid=12345,fd=3))
		idx := strings.Index(line, "pid=")
		if idx == -1 {
			continue
		}
		rest := line[idx+len("pid="):]
		end := strings.IndexAny(rest, ",) \t")
		if end == -1 {
			end = len(rest)
		}
		pid, err := strconv.Atoi(rest[:end])
		if err != nil {
			return nil, fmt.Errorf("parsing ss pid %q: %w", rest[:end], err)
		}
		// Best-effort name extraction.
		name := ""
		if startQ := strings.Index(line, `users:(("`); startQ != -1 {
			tail := line[startQ+len(`users:(("`):]
			if endQ := strings.Index(tail, `"`); endQ != -1 {
				name = tail[:endQ]
			}
		}
		return &Occupier{PID: pid, Command: name}, nil
	}
	return nil, ErrNoOccupier
}

// lookupBin returns either the env-overridden binary path (test only) or
// the result of exec.LookPath on the default name. Empty string when the
// binary is not found.
func lookupBin(envKey, defaultName string) string {
	if override := strings.TrimSpace(osGetenv(envKey)); override != "" {
		return override
	}
	if path, err := exec.LookPath(defaultName); err == nil {
		return path
	}
	return ""
}

// KillOccupier performs the TERM → wait → KILL → re-verify sequence on the
// process listening on port. Logs progress + the killed PID/command to
// the provided logger (nil → slog.Default()). The function returns nil
// once the port has been freed; otherwise it returns an error describing
// why the port could not be reclaimed.
func KillOccupier(ctx context.Context, port int, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	occ, err := LSofTCPListen(ctx, port)
	if errors.Is(err, ErrNoOccupier) {
		return nil
	}
	if err != nil {
		return err
	}
	logger.Warn("port occupied, attempting to reclaim",
		"port", port, "pid", occ.PID, "command", occ.Command)

	// SIGTERM, then wait 3s.
	if killErr := killProcess(occ.PID, false); killErr != nil {
		return killErr
	}
	if waitForPortFree(ctx, port, 3*time.Second) {
		logger.Info("port freed by SIGTERM",
			"port", port, "pid", occ.PID, "command", occ.Command)
		return nil
	}
	// SIGKILL.
	logger.Warn("port still occupied after SIGTERM, sending SIGKILL",
		"port", port, "pid", occ.PID, "command", occ.Command)
	if killErr := killProcess(occ.PID, true); killErr != nil {
		return killErr
	}
	if waitForPortFree(ctx, port, 500*time.Millisecond) {
		logger.Info("port freed by SIGKILL",
			"port", port, "pid", occ.PID, "command", occ.Command)
		return nil
	}
	// One retry of the whole sequence in case a new occupier raced in.
	if _, err := LSofTCPListen(ctx, port); errors.Is(err, ErrNoOccupier) {
		return nil
	}
	return fmt.Errorf("port %d still occupied after SIGKILL", port)
}

// waitForPortFree polls TCP listen attempts until success or deadline.
// Used after kill signals to verify the port is reclaimable. The bind
// itself is on 127.0.0.1 to avoid firewall prompts on macOS.
func waitForPortFree(ctx context.Context, port int, timeout time.Duration) bool {
	var lc net.ListenConfig
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		l, err := lc.Listen(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = l.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false
}
