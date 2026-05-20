package shim

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// goosWindows centralises the runtime.GOOS comparison string so goconst doesn't
// flag the three test cases that gate-skip Windows-incompatible fixtures.
const goosWindows = "windows"

// writeExecutable writes a script file at 0o600 then sets the user
// execute bit so the test process can run it. The file lives in
// t.TempDir() (mode 0o700) so the world cannot see it even though
// the file itself has the user execute bit set.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fake binary at %s: %v", path, err)
	}
	// 0o700 is required to actually run the script under exec.LookPath /
	// exec.Command. The path is inside t.TempDir() which itself is mode
	// 0o700 + owned by the test user, so world cannot see or execute it.
	//nolint:gosec // G302: executable bit is required for the test fixture script; tempdir already mode 0700
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod fake binary at %s: %v", path, err)
	}
}

// TestResolveWbt_PathLookup verifies that ResolveWbt picks up `wbt` from PATH.
func TestResolveWbt_PathLookup(t *testing.T) {
	dir := t.TempDir()
	binName := "wbt"
	if runtime.GOOS == goosWindows {
		binName = "wbt.exe"
	}
	wbtPath := filepath.Join(dir, binName)
	writeExecutable(t, wbtPath, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir)

	got, err := ResolveWbt()
	if err != nil {
		t.Fatalf("ResolveWbt() error = %v", err)
	}
	// LookPath may resolve symlinks (macOS /var → /private/var); tolerate
	// either the exact path or its EvalSymlinks form.
	if got != wbtPath {
		real, _ := filepath.EvalSymlinks(wbtPath)
		if got != real {
			t.Errorf("ResolveWbt() = %q, want %q (or %q)", got, wbtPath, real)
		}
	}
}

// TestResolveWbt_NotFound verifies that an empty PATH + no sibling binary
// yields an error rather than panicking.
func TestResolveWbt_NotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir) // empty dir → exec.LookPath fails

	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable failed: %v", err)
	}
	sibling := filepath.Join(filepath.Dir(exe), "wbt")
	if runtime.GOOS == goosWindows {
		sibling += ".exe"
	}
	if _, statErr := os.Stat(sibling); statErr == nil {
		t.Skip("test binary directory contains a sibling 'wbt' — skip negative test")
	}

	if _, err := ResolveWbt(); err == nil {
		t.Fatal("ResolveWbt() returned no error when wbt is absent")
	}
}

// TestExecWbt_PropagatesExitCode verifies that the shim returns the child's
// non-zero exit code rather than collapsing to 0 or 1.
func TestExecWbt_PropagatesExitCode(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("POSIX shell helper required for this test")
	}
	dir := t.TempDir()
	wbtScript := filepath.Join(dir, "wbt")
	// Exit with the numeric arg the parent passed in ($2 since we prepend
	// the subcommand). Using ${2:-0} so the script doesn't blow up if invoked
	// with no extra arg.
	writeExecutable(t, wbtScript, "#!/bin/sh\nexit ${2:-0}\n")
	t.Setenv("PATH", dir)

	cases := []struct {
		name string
		code string
		want int
	}{
		{name: "exit 0 success", code: "0", want: 0},
		{name: "exit 1 generic failure", code: "1", want: 1},
		{name: "exit 42 propagated", code: "42", want: 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExecWbt("context", []string{tc.code}); got != tc.want {
				t.Errorf("ExecWbt(context, %q) = %d, want %d", tc.code, got, tc.want)
			}
		})
	}
}

// TestExecWbt_NoBinaryReturns1 verifies that ResolveWbt failures surface as
// exit code 1 rather than panicking.
func TestExecWbt_NoBinaryReturns1(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable failed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(exe), "wbt")); statErr == nil {
		t.Skip("test binary directory contains a sibling 'wbt' — skip negative test")
	}

	if got := ExecWbt("context", []string{"--help"}); got != 1 {
		t.Errorf("ExecWbt with missing wbt = %d, want 1", got)
	}
}
