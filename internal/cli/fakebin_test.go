package cli_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// buildEnv is captured once, at package init — before any test has had a
// chance to call t.Setenv("HOME", ...) via setupTestEnv. goBuildFake uses
// this snapshot instead of the live os.Environ() so that fake-binary builds
// keep resolving GOCACHE / GOPATH / the go1.26.4 toolchain from the
// original developer environment, not from a test's freshly-minted temp
// HOME (F0925-07).
var buildEnv = os.Environ()

// goBuildFake is the only function in this package that shells out to
// `go build`. It always runs with buildEnv so cache/toolchain resolution
// is unaffected by any test's HOME override, and it increments builds — a
// caller-owned counter — once per invocation, so tests can assert a
// sync.Once guard actually prevented duplicate builds (F0925-07).
func goBuildFake(out, target string, builds *atomic.Int64) error {
	builds.Add(1)
	//nolint:gosec // G204: go from PATH; fixed args; target is a testdata path or generated stub under MkdirTemp (F0925-07)
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", out, target)
	cmd.Env = buildEnv
	combined, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build %s: %w\n%s", target, err, combined)
	}
	return nil
}

var (
	fakeServerOnce   sync.Once
	fakeServerPath   string
	fakeServerErr    error
	fakeServerBuilds atomic.Int64
)

// buildFakeServer compiles internal/cli/testdata/fakeserver exactly once
// for the whole test binary and returns its absolute path. The build
// output lives under an os.MkdirTemp directory (not t.TempDir()) that
// intentionally outlives any single test — the OS reclaims it when the
// test process exits, and per-test cleanup would defeat the point of the
// sync.Once build-once guarantee this function exists to provide
// (F0925-07).
func buildFakeServer(t *testing.T) string {
	t.Helper()
	fakeServerOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wbt-fakeserver-*")
		if err != nil {
			fakeServerErr = err
			return
		}
		out := filepath.Join(dir, "fakeserver")
		if buildErr := goBuildFake(out, "./testdata/fakeserver", &fakeServerBuilds); buildErr != nil {
			fakeServerErr = buildErr
			return
		}
		fakeServerPath = out
	})
	if fakeServerErr != nil {
		t.Fatalf("building fakeserver: %v", fakeServerErr)
	}
	return fakeServerPath
}

// fakeClaudeLogEnv names the env var fakeClaudeStub reads at runtime to
// find its call-log path. Using an env var (rather than baking the path
// into the compiled source) lets one shared binary serve every test with
// its own isolated log file (F0925-07).
const fakeClaudeLogEnv = "WBT_FAKE_CLAUDE_LOG"

// fakeClaudeStub records its own argv (one line per invocation) to the
// path named by WBT_FAKE_CLAUDE_LOG, then exits 0. A missing env var is a
// silent no-op by design — buildFakeClaude always sets it before use.
const fakeClaudeStub = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	logPath := os.Getenv("` + fakeClaudeLogEnv + `")
	if logPath == "" {
		os.Exit(0)
	}
	f, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if f != nil {
		fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
		_ = f.Close()
	}
	os.Exit(0)
}
`

var (
	fakeClaudeOnce   sync.Once
	fakeClaudeBin    string
	fakeClaudeErr    error
	fakeClaudeBuilds atomic.Int64
)

// buildFakeClaude compiles the fake-claude stub exactly once for the whole
// test binary (sync.Once + goBuildFake, same rationale as buildFakeServer)
// and returns (bin, logPath). Every call — not just the first — gets a
// fresh logPath under t.TempDir() and points the one shared binary at it
// via t.Setenv(WBT_FAKE_CLAUDE_LOG, ...), so callers each observe only
// their own invocations even though the binary itself is built once
// (F0925-07).
func buildFakeClaude(t *testing.T) (string, string) {
	t.Helper()
	fakeClaudeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wbt-fakeclaude-*")
		if err != nil {
			fakeClaudeErr = err
			return
		}
		src := filepath.Join(dir, "main.go")
		if err := os.WriteFile(src, []byte(fakeClaudeStub), 0o600); err != nil {
			fakeClaudeErr = err
			return
		}
		bin := filepath.Join(dir, "fake-claude")
		if buildErr := goBuildFake(bin, src, &fakeClaudeBuilds); buildErr != nil {
			fakeClaudeErr = buildErr
			return
		}
		fakeClaudeBin = bin
	})
	if fakeClaudeErr != nil {
		t.Fatalf("building fake-claude: %v", fakeClaudeErr)
	}
	logPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv(fakeClaudeLogEnv, logPath)
	return fakeClaudeBin, logPath
}

// TestFakeBinaries_BuiltOncePerTestBinary pins the sync.Once guarantee:
// calling each helper twice must return the same path both times and must
// only have actually invoked goBuildFake once per binary (F0925-07).
func TestFakeBinaries_BuiltOncePerTestBinary(t *testing.T) {
	first := buildFakeServer(t)
	second := buildFakeServer(t)
	if first != second {
		t.Errorf("buildFakeServer paths differ across calls: %q vs %q", first, second)
	}
	if got := fakeServerBuilds.Load(); got != 1 {
		t.Errorf("fakeServerBuilds = %d; want exactly 1", got)
	}

	firstBin, _ := buildFakeClaude(t)
	secondBin, _ := buildFakeClaude(t)
	if firstBin != secondBin {
		t.Errorf("buildFakeClaude binary paths differ across calls: %q vs %q", firstBin, secondBin)
	}
	if got := fakeClaudeBuilds.Load(); got != 1 {
		t.Errorf("fakeClaudeBuilds = %d; want exactly 1", got)
	}
}

// TestFakeBinaryBuild_DoesNotWriteUnderTestHOME pins the buildEnv snapshot
// behaviour end-to-end: even when the calling test has already redirected
// HOME/XDG_CACHE_HOME/GOCACHE to a scratch directory, goBuildFake must not
// write anything under that directory — it must still be using the
// original (pre-test) environment captured at package init (F0925-07).
func TestFakeBinaryBuild_DoesNotWriteUnderTestHOME(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(func() {
		// A positive-control run (buildEnv swapped for the live
		// os.Environ()) makes `go build` download the go1.26.4 toolchain
		// into home; those files land read-only and would otherwise make
		// t.TempDir()'s own cleanup fail.
		_ = filepath.WalkDir(home, func(path string, _ fs.DirEntry, walkErr error) error {
			if walkErr == nil {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("GOCACHE", "")

	var localBuilds atomic.Int64
	out := filepath.Join(t.TempDir(), "fakeserver")
	if err := goBuildFake(out, "./testdata/fakeserver", &localBuilds); err != nil {
		t.Fatalf("goBuildFake: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat build output: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("build output %s is not executable: mode=%v", out, info.Mode())
	}
	if got := localBuilds.Load(); got != 1 {
		t.Errorf("local build counter = %d; want 1", got)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("ReadDir(home): %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("build wrote under test HOME %s: %v", home, names)
	}
}
