//go:build windows

package lifecycle

import (
	"errors"
	"os"
)

func osGetenv(key string) string { return os.Getenv(key) }

func killProcess(_ int, _ bool) error { return errors.ErrUnsupported }

func reapNonblocking(_ int) error { return nil }

// processAlive on Windows is a stub: Phase 2 only targets Unix-like systems
// where the wbt CLI is actually used. A real Windows implementation would
// call OpenProcess with PROCESS_QUERY_LIMITED_INFORMATION and check the exit
// code. Until that lands, return ErrUnsupported so callers can fall back to
// "assume alive" or surface a clear error.
func processAlive(pid int) (bool, error) {
	return false, errors.ErrUnsupported
}
