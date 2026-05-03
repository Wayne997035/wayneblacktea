// Package guard implements the wbt-guard PreToolUse hook for observing and
// classifying Claude Code tool invocations.
//
// # Marker file
//
// The marker file (.wayneblacktea/config.json) is per-repo and MUST be
// gitignored. It enables wbt-guard for the repo via {"observe":true}.
//
// # Database URL: env-only
//
// As of Round 2 (2026-05-03), the marker file's `db_url` field is
// **deprecated**. The canonical source for the Postgres DSN is the
// `DATABASE_URL` environment variable. The deprecated field is still parsed
// for backward compatibility, but its presence triggers an `slog.Warn` and
// it is IGNORED at resolve time — pass DATABASE_URL via your shell / env
// loader / Railway env / Docker env / .env file instead.
//
// Reasoning: storing the DSN in a file mode 0644 / 0640 — common defaults
// for files in a repo subdirectory — leaves `db_url` group/world readable.
// Forcing operators to use the env path eliminates the on-disk credential
// surface entirely.
//
// # Permission check
//
// LoadConfig stat's the marker file before parse and emits an `slog.Warn`
// if it is group- or world-readable (mode & 0o077 != 0). This does not
// fail-close — guard is observe-only — but the warning gives the operator
// a chance to chmod 0600 the file.
package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// markerRelPath is the relative path from the repo root (cwd) to the marker
// file that enables wbt-guard for that repo.
const markerRelPath = ".wayneblacktea/config.json"

// Config is the schema for the per-repo marker file.
//
// Example:
//
//	{
//	  "version": 1,
//	  "observe": true
//	}
//
// Fields:
//   - Version: schema version; currently only 1 is valid.
//   - Observe: when false (or absent), wbt-guard exits immediately without
//     logging. Set to true to enable observe mode (P0a-β).
//   - DBURL: DEPRECATED. Parsed for backward compatibility but ignored at
//     resolve time. Use the DATABASE_URL env var instead. A warning is
//     logged via slog when this field is non-empty.
type Config struct {
	Version int  `json:"version"`
	Observe bool `json:"observe"`
	// Deprecated: storing the DSN in a per-repo file leaks credentials into
	// any backup / sync / search-indexer that can read the file. Use the
	// DATABASE_URL env var instead. Field is parsed but ignored.
	DBURL string `json:"db_url,omitempty"`
}

// ErrMarkerAbsent is returned when the marker file does not exist.
// The caller should treat this as a noop — guard is not enabled for this repo.
var ErrMarkerAbsent = errors.New("guard marker absent")

// LoadConfig reads the marker file from <cwd>/.wayneblacktea/config.json.
// Returns ErrMarkerAbsent when the file does not exist.
// Any other OS error or JSON parse error is returned as-is.
//
// As a side effect, LoadConfig:
//  1. Logs a warning via slog if the file mode is group- or world-readable
//     (mode.Perm() & 0o077 != 0). Observe-only contract is preserved — the
//     warn is informational, the load still succeeds.
//  2. Logs a warning if the deprecated `db_url` field is present so the
//     operator migrates to the DATABASE_URL env var.
func LoadConfig(cwd string) (Config, error) {
	path := filepath.Join(cwd, markerRelPath)

	// Permission check before parse: file might legitimately contain
	// credentials (legacy db_url in old configs); group/world read =>
	// emit a warning. Stat'ing first is cheap and keeps the warn outside
	// the parse-error path.
	if info, statErr := os.Stat(path); statErr == nil {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			slog.Warn(
				"guard: marker file is group/world-readable; chmod 0600 to prevent leak",
				"path", path,
				"mode", fmt.Sprintf("%#o", perm),
			)
		}
	}

	data, err := os.ReadFile(path) //nolint:gosec // path built from operator-controlled cwd + constant relative path
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, ErrMarkerAbsent
		}
		return Config{}, fmt.Errorf("reading guard config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing guard config %s: %w", path, err)
	}
	if cfg.DBURL != "" {
		slog.Warn(
			"guard: db_url field in marker file is DEPRECATED and IGNORED; set DATABASE_URL env var instead",
			"path", path,
		)
	}
	return cfg, nil
}

// ResolveDBURL returns the Postgres DSN to use for guard_events writes.
//
// Source precedence:
//  1. DATABASE_URL environment variable — the only supported mechanism.
//
// The Config.DBURL field is DEPRECATED and intentionally ignored here. See
// the package documentation. We keep the function signature stable so call
// sites in cmd/wbt-guard and cmd/wbt continue to compile, but the marker-
// file value never wins.
func ResolveDBURL(_ Config) string {
	return os.Getenv("DATABASE_URL")
}
