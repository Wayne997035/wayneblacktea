// Package guard implements the wbt-guard PreToolUse hook for observing and
// classifying Claude Code tool invocations.
//
// The marker file (.wayneblacktea/config.json) is per-repo and MUST be
// gitignored.  It may contain a db_url field, but storing credentials in a
// file is less secure than using the DATABASE_URL environment variable.
// Prefer DATABASE_URL; use db_url only for per-project overrides on machines
// where setting environment variables is impractical.
//
// SECURITY: the marker file is gitignored (.wayneblacktea/config.json) to
// prevent db_url from leaking into version control.  Always verify the repo's
// .gitignore includes this path before adding a local override.
package guard

import (
	"encoding/json"
	"errors"
	"fmt"
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
//	  "observe": true,
//	  "db_url": "postgres://..."
//	}
//
// Fields:
//   - Version: schema version; currently only 1 is valid.
//   - Observe: when false (or absent), wbt-guard exits immediately without
//     logging.  Set to true to enable observe mode (P0a-β).
//   - DBURL: optional Postgres DSN override.  Prefer DATABASE_URL env.
//     If both are set, DBURL takes precedence.
type Config struct {
	Version int    `json:"version"`
	Observe bool   `json:"observe"`
	DBURL   string `json:"db_url"`
}

// ErrMarkerAbsent is returned when the marker file does not exist.
// The caller should treat this as a noop — guard is not enabled for this repo.
var ErrMarkerAbsent = errors.New("guard marker absent")

// LoadConfig reads the marker file from <cwd>/.wayneblacktea/config.json.
// Returns ErrMarkerAbsent when the file does not exist.
// Any other OS error or JSON parse error is returned as-is.
func LoadConfig(cwd string) (Config, error) {
	path := filepath.Join(cwd, markerRelPath)
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
	return cfg, nil
}

// ResolveDBURL returns the Postgres DSN to use for guard_events writes.
// Priority: marker db_url > DATABASE_URL env var.
// Returns empty string when neither is set.
func ResolveDBURL(cfg Config) string {
	if cfg.DBURL != "" {
		return cfg.DBURL
	}
	return os.Getenv("DATABASE_URL")
}
