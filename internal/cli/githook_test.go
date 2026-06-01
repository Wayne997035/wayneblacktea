package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolatedGitHome points HOME, the global/system git config, and XDG_DATA_HOME
// at temp dirs so install-git-hook tests never touch the real git config.
// Returns the temp XDG_DATA_HOME.
func isolatedGitHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	return data
}

func gitGlobalGet(t *testing.T, key string) string {
	t.Helper()
	//nolint:gosec // G204: test helper; fixed git config args
	cmd := exec.CommandContext(t.Context(), "git", "config", "--global", "--get", key)
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

func gitGlobalSet(t *testing.T, key, val string) {
	t.Helper()
	//nolint:gosec // G204: test helper; fixed git config args
	if out, err := exec.CommandContext(t.Context(), "git", "config", "--global", key, val).CombinedOutput(); err != nil {
		t.Fatalf("git config --global %s %s: %v\n%s", key, val, err, out)
	}
}

func TestInstallGitHook_FreshInstall(t *testing.T) {
	data := isolatedGitHome(t)
	if err := RunInstallGitHook(nil); err != nil {
		t.Fatalf("RunInstallGitHook: %v", err)
	}
	wantDir := filepath.Join(data, "wayneblacktea", "git-hooks")
	hookPath := filepath.Join(wantDir, "post-merge")
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("hook not written: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("hook mode = %o, want 0755", info.Mode().Perm())
	}
	if got := gitGlobalGet(t, "core.hooksPath"); got != wantDir {
		t.Errorf("core.hooksPath = %q, want %q", got, wantDir)
	}
}

func TestInstallGitHook_Idempotent(t *testing.T) {
	isolatedGitHome(t)
	if err := RunInstallGitHook(nil); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := RunInstallGitHook(nil); err != nil {
		t.Fatalf("second install (must be idempotent): %v", err)
	}
}

func TestInstallGitHook_ConflictGuard(t *testing.T) {
	data := isolatedGitHome(t)
	other := t.TempDir()
	gitGlobalSet(t, "core.hooksPath", other)

	if err := RunInstallGitHook(nil); err != nil {
		t.Fatalf("conflict guard must return nil, got %v", err)
	}
	if got := gitGlobalGet(t, "core.hooksPath"); got != other {
		t.Errorf("conflict guard changed hooksPath to %q, want unchanged %q", got, other)
	}
	if _, err := os.Stat(filepath.Join(data, "wayneblacktea", "git-hooks", "post-merge")); !os.IsNotExist(err) {
		t.Errorf("hook must not be written when the conflict guard refuses")
	}
}

func TestInstallGitHook_Force(t *testing.T) {
	data := isolatedGitHome(t)
	other := t.TempDir()
	gitGlobalSet(t, "core.hooksPath", other)

	if err := RunInstallGitHook([]string{"--force"}); err != nil {
		t.Fatalf("--force: %v", err)
	}
	wantDir := filepath.Join(data, "wayneblacktea", "git-hooks")
	if got := gitGlobalGet(t, "core.hooksPath"); got != wantDir {
		t.Errorf("--force did not override core.hooksPath: got %q, want %q", got, wantDir)
	}
}

func TestInstallGitHook_UnknownArg(t *testing.T) {
	if err := RunInstallGitHook([]string{"--bogus"}); err == nil {
		t.Errorf("expected error for unknown argument")
	}
}
