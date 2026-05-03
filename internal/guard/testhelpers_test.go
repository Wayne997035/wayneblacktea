package guard

import (
	"runtime"
	"testing"
)

// goosWindows is the runtime.GOOS value emitted on Windows builds.
// Pulled into a constant to satisfy goconst across test files that all need
// to skip on Windows for POSIX-only fixtures (file modes, symlinks, etc.).
const goosWindows = "windows"

// skipOnWindows skips the calling test with the given reason when run on
// Windows. Used by symlink and POSIX-permission tests.
func skipOnWindows(t *testing.T, reason string) {
	t.Helper()
	if runtime.GOOS == goosWindows {
		t.Skip(reason)
	}
}
