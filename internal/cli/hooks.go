package cli

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// InitHookSlog redirects slog to a 0600 file in os.TempDir so a hook never
// writes to stderr (Claude Code surfaces stderr as terminal warnings). Falls
// back to io.Discard if the log file cannot be opened. The name parameter is
// the basename of the hook (e.g. "wbt-context", "wbt-hook", "wbt-doctor") and
// becomes the prefix of the log file under os.TempDir.
//
// Shared by the wbt context|hook|doctor subcommands (formerly the standalone
// wbt-context / wbt-hook / wbt-doctor binaries, now thin shims that exec
// `wbt <subcmd>`).
func InitHookSlog(name string) {
	logPath := filepath.Join(os.TempDir(), name+".log")
	//nolint:gosec // G304: logPath is os.TempDir() + constant suffix; not derived from user input
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelWarn})))
}

// WorkspaceFromEnv reads WORKSPACE_ID from the environment and returns a
// *uuid.UUID, or nil if the env var is missing/invalid. Used by the
// context and doctor hooks to scope DB queries.
func WorkspaceFromEnv() *uuid.UUID {
	raw := strings.TrimSpace(os.Getenv("WORKSPACE_ID"))
	if raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

// DSNFromFallback reads DATABASE_URL from one of the user-config fallback
// locations:
//   - ~/.wayneblacktea/.env.local
//   - ~/.wayneblacktea/.env
//
// Returns "" when no file is found or no DATABASE_URL line is present.
func DSNFromFallback() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	for _, p := range fallbackEnvCandidates(home) {
		warnIfInsecureEnvFile(p)
		b, err := os.ReadFile(p) //nolint:gosec // G304: p is built from os.UserHomeDir plus fixed .wayneblacktea filenames
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DATABASE_URL=") {
				return strings.TrimPrefix(line, "DATABASE_URL=")
			}
		}
	}
	return ""
}

// fallbackEnvCandidates returns the paths consulted by DSNFromFallback in
// priority order.
func fallbackEnvCandidates(home string) []string {
	configDir := filepath.Join(home, ".wayneblacktea")
	return []string{
		filepath.Join(configDir, ".env.local"),
		filepath.Join(configDir, ".env"),
	}
}

// warnIfInsecureEnvFile emits a slog warning when the fallback env file has
// group/world-readable permissions. Credential-bearing files MUST be 0600.
// (backend-security-design.md §4.1)
func warnIfInsecureEnvFile(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Mode().Perm()&0o077 != 0 {
		slog.Warn("env fallback file is group/world readable; chmod 600 is recommended", "path", path)
	}
}
