package lifecycle

import (
	"testing"
	"time"
)

// SetPortProbeTimeoutForTest overrides portProbeTimeout for the duration of
// t and restores the previous value via t.Cleanup. F0925-08.
//
// Only sequential tests (i.e. tests that already call t.Setenv, which bars
// t.Parallel per https://pkg.go.dev/testing#T.Parallel) may call this: the
// override mutates package-level state, and a parallel test reading
// portProbeTimeout concurrently must never observe it change mid-run.
func SetPortProbeTimeoutForTest(t testing.TB, d time.Duration) {
	t.Helper()
	old := portProbeTimeout
	portProbeTimeout = d
	t.Cleanup(func() { portProbeTimeout = old })
}
