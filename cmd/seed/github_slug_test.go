package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeGit puts a `git` on PATH that prints url for `remote get-url origin`.
func fakeGit(t *testing.T, url string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho '" + url + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil { //nolint:gosec // test fixture, intentional exec perm
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestResolveGitHubSlug pins [F0925-31]: seed fills github_slug only for a
// github.com remote whose owner is on the allow-list (case-insensitive), and
// leaves it nil (never overwriting a stored value) otherwise.
func TestResolveGitHubSlug(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell-script stub for git")
	}
	owners := parseGitHubOwners("Wayne997035,goflare-io,Flare-Go")
	for _, tc := range []struct {
		url  string
		want string
	}{
		{"https://github.com/Wayne997035/x.git", "Wayne997035/x"},
		{"git@github.com:wayne997035/y.git", "wayne997035/y"},
		{"https://github.com/xai-org/x-algorithm", ""},
		{"https://gitea.example.com/Wayne997035/x.git", ""},
	} {
		fakeGit(t, tc.url)
		got := resolveGitHubSlug(context.Background(), t.TempDir(), owners)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%s: want nil, got %q", tc.url, *got)
		case tc.want != "" && (got == nil || *got != tc.want):
			t.Errorf("%s: want %q, got %v", tc.url, tc.want, got)
		}
	}
}
