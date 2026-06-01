package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// helpFlag is the long help flag shared by the cli subcommands.
const helpFlag = "--help"

const installGitHookUsage = `wbt install-git-hook — install a global git post-merge hook that auto-closes GTD tasks

Usage:
  wbt install-git-hook [--force]

Sets git's global core.hooksPath to a wbt-owned directory and writes a
post-merge hook there. After any merge in any of your repos, the hook calls
` + "`wbt post-merge-local`" + ` which auto-closes the GTD task tied to the merged PR.

The hook is fail-open: it never blocks a merge and exits 0 even if the server
is down or wbt is uninstalled.

Conflict guard: if core.hooksPath is already set to a different directory, the
command refuses to overwrite it unless --force is given (the previous value is
printed first so you can restore per-repo hooks if needed).

To uninstall: git config --global --unset core.hooksPath

Flags:
  --force   override an existing core.hooksPath (prints the old value first)
  --help    show this help
`

// postMergeHookScript is the shim written into the wbt hooks dir. It delegates
// to `wbt post-merge-local`, swallows all output, and always exits 0 so a merge
// is never blocked — even if wbt is not on PATH.
const postMergeHookScript = `#!/bin/sh
# wbt post-merge hook — installed by ` + "`wbt install-git-hook`" + `.
# Fail-open: always exits 0, never blocks a merge.
wbt post-merge-local >/dev/null 2>&1 || true
`

// RunInstallGitHook implements `wbt install-git-hook`.
func RunInstallGitHook(args []string) error {
	force, showHelp, err := parseInstallGitHookArgs(args)
	if err != nil {
		return err
	}
	if showHelp {
		_, _ = fmt.Fprint(os.Stdout, installGitHookUsage)
		return nil
	}

	hooksDir, err := wbtHooksDir()
	if err != nil {
		return err
	}

	existing := currentGlobalHooksPath()
	if existing != "" && existing != hooksDir && !force {
		fmt.Fprintf(os.Stderr,
			"wbt install-git-hook: git core.hooksPath is already set to %q.\n"+
				"Refusing to overwrite. Re-run with --force to override (the old value\n"+
				"will be printed first), or install the hook per-repo manually.\n",
			existing)
		return nil
	}
	if existing != "" && existing != hooksDir && force {
		fmt.Fprintf(os.Stdout, "wbt: previous core.hooksPath was %q\n", existing)
		fmt.Fprintf(os.Stderr,
			"WARNING: git hooks in %q will no longer run automatically after this change.\n", existing)
	}

	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return fmt.Errorf("install-git-hook: create %s: %w", hooksDir, err)
	}
	hookPath := filepath.Join(hooksDir, "post-merge")
	//nolint:gosec // G306: a git hook script must be executable (0755)
	if err := os.WriteFile(hookPath, []byte(postMergeHookScript), 0o755); err != nil {
		return fmt.Errorf("install-git-hook: write %s: %w", hookPath, err)
	}
	if err := setGlobalHooksPath(hooksDir); err != nil {
		return fmt.Errorf("install-git-hook: set core.hooksPath: %w", err)
	}

	fmt.Fprintf(os.Stdout,
		"wbt: installed global post-merge hook at %s\n"+
			"Merged PRs in any repo will now auto-close their matching GTD task.\n"+
			"To uninstall: git config --global --unset core.hooksPath\n",
		hookPath)
	return nil
}

// parseInstallGitHookArgs returns (force, showHelp, err). showHelp=true means
// the caller should print usage and return early.
func parseInstallGitHookArgs(args []string) (force, showHelp bool, err error) {
	for _, a := range args {
		switch a {
		case helpFlag, "-h":
			return false, true, nil
		case "--force":
			force = true
		default:
			return false, false, fmt.Errorf("install-git-hook: unknown argument %q", a)
		}
	}
	return force, false, nil
}

// wbtHooksDir returns $XDG_DATA_HOME/wayneblacktea/git-hooks (defaults to
// ~/.local/share/...). Consistent with the XDG layout setup uses elsewhere.
func wbtHooksDir() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locating home dir: %w", err)
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "wayneblacktea", "git-hooks"), nil
}

// currentGlobalHooksPath returns the current global core.hooksPath, or "" when
// unset (git exits non-zero for an unset key).
func currentGlobalHooksPath() string {
	out, err := gitGlobalConfig("--get", "core.hooksPath")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// setGlobalHooksPath sets the global core.hooksPath to dir.
func setGlobalHooksPath(dir string) error {
	_, err := gitGlobalConfig("core.hooksPath", dir)
	return err
}

// gitGlobalConfig runs `git config --global <args...>` and returns stdout.
func gitGlobalConfig(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	full := append([]string{"config", "--global"}, args...)
	// G204 rationale: fixed `config --global` argv plus a wbt-owned directory
	// path; no shell, strict argv, no attacker-controlled tokens.
	//nolint:gosec // G204: fixed git argv, no shell
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.Output()
	return string(out), err
}
