// Package shim provides the shared wbt-subcommand shim implementation used by
// the legacy hook binaries (wbt-context, wbt-hook, wbt-doctor) so that
// existing ~/.claude/settings.json absolute-path entries keep working after
// Phase 2.3 collapsed the hook logic into `wbt` subcommands.
//
// Each shim is a 3-line main.go that calls ExecWbt with its canonical
// subcommand name; the heavy lifting (PATH lookup, sibling fallback, exit
// code propagation, stdio wiring) lives here so the three binaries don't
// duplicate code (which would also trip the `dupl` lint rule).
package shim

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ExecWbt finds the wbt binary, runs `wbt <subcmd> <args...>` with stdio
// wired to the current process, and returns the child's exit code. Returns 1
// when wbt cannot be located or fails for a non-exit reason.
//
// Callers typically do: os.Exit(shim.ExecWbt("context", os.Args[1:])).
func ExecWbt(subcmd string, args []string) int {
	wbt, err := ResolveWbt()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(os.Args[0]), err)
		return 1
	}
	full := append([]string{subcmd}, args...)
	// The shim's lifetime is bound to the OS process itself; the parent
	// (Claude Code hook runner) signals termination via SIGTERM / process
	// exit, which propagates to the child through normal exec. A Background
	// context is the correct lifetime here — there is no enclosing request
	// to cancel against. noctx is silenced because the alternative
	// (synthesising a cancellable ctx) would only complicate without adding
	// safety.
	//nolint:gosec // G204: wbt is resolved via exec.LookPath or sibling-dir lookup, not user input
	cmd := exec.CommandContext(context.Background(), wbt, full...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(os.Args[0]), err)
		return 1
	}
	return 0
}

// ResolveWbt locates the wbt binary, preferring PATH and falling back to the
// directory containing the calling process executable (matches how
// scripts/install.sh drops shims and the real binary into the same prefix).
func ResolveWbt() (string, error) {
	if p, err := exec.LookPath("wbt"); err == nil {
		return p, nil
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "wbt")
		if info, statErr := os.Stat(sibling); statErr == nil && !info.IsDir() {
			return sibling, nil
		}
	}
	return "", fmt.Errorf("wbt not found in PATH or next to %s", filepath.Base(os.Args[0]))
}
