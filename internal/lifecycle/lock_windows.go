//go:build windows

package lifecycle

import "errors"

// ErrLockHeld is returned by AcquireSetupLock when another wbt setup is
// already running. On Windows the flock-based implementation is unavailable,
// so AcquireSetupLock always returns errors.ErrUnsupported instead; callers
// SHOULD continue without a lock and document the assumption that concurrent
// setup invocations are the user's responsibility on Windows.
var ErrLockHeld = errors.New("another wbt setup is already running")

// AcquireSetupLock is a no-op stub on Windows. It always returns
// errors.ErrUnsupported. Callers that check errors.Is(err,
// errors.ErrUnsupported) may choose to proceed without mutual exclusion.
//
// The wbt CLI is primarily intended for Unix-like systems in Phase 2; a
// Windows job-object implementation can be added in a future release.
func AcquireSetupLock(_ string) (release func(), err error) {
	return func() {}, errors.ErrUnsupported
}
