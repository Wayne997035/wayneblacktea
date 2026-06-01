package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

func TestExtractPRNumber(t *testing.T) {
	cases := []struct{ in, want string }{
		{"feat: add foo (#123)", "123"},
		{"chore: no number", ""},
		{"(#abc)", ""},
		{"fix: bar (#7)", "7"},
		{"", ""},
	}
	for _, c := range cases {
		if got := extractPRNumber(c.in); got != c.want {
			t.Errorf("extractPRNumber(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeriveRepoSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://github.com/owner/repo.git", "owner/repo"},
		{"https://github.com/owner/repo", "owner/repo"},
		{"git@github.com:owner/repo.git", "owner/repo"},
		{"git@github.com:owner/repo", "owner/repo"},
		{"ssh://git@github.com/owner/repo.git", "owner/repo"},
		{"https://gitlab.com/owner/repo.git", ""},
		{"", ""},
		{"not a url", ""},
	}
	for _, c := range cases {
		if got := deriveRepoSlug(c.in); got != c.want {
			t.Errorf("deriveRepoSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildGitHubPRURL(t *testing.T) {
	if got := buildGitHubPRURL("owner/repo", "42"); got != "https://github.com/owner/repo/pull/42" {
		t.Errorf("unexpected url: %q", got)
	}
	if got := buildGitHubPRURL("", "42"); got != "" {
		t.Errorf("expected empty for empty slug, got %q", got)
	}
	if got := buildGitHubPRURL("owner/repo", ""); got != "" {
		t.Errorf("expected empty for empty pr number, got %q", got)
	}
}

// newTestRepo creates a temp git repo with one (empty) commit carrying the
// given subject and an origin remote. Returns the repo dir.
func newTestRepo(t *testing.T, subject, originURL string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		//nolint:gosec // G204: test helper; fixed git subcommands, no untrusted input
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if originURL != "" {
		run("remote", "add", "origin", originURL)
	}
	run("commit", "--allow-empty", "-q", "-m", subject)
	return dir
}

func TestRunPostMergeLocal_HappyPath(t *testing.T) {
	dir := newTestRepo(t, "feat: thing (#42)", "https://github.com/owner/repo.git")
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("API_KEY", "test-key-1234567890")

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-key-1234567890" {
			t.Errorf("missing/wrong X-API-Key: %q", r.Header.Get("X-API-Key"))
		}
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"applied":1,"no_match":0,"candidate_writes":0}`)
	}))
	defer srv.Close()

	if err := RunPostMergeLocal([]string{"--server", srv.URL}); err != nil {
		t.Fatalf("RunPostMergeLocal: %v", err)
	}

	var parsed struct {
		MergedPRs []reconcilePR `json:"merged_prs"`
	}
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("server received non-JSON body: %v (%s)", err, gotBody)
	}
	if len(parsed.MergedPRs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(parsed.MergedPRs))
	}
	pr := parsed.MergedPRs[0]
	if pr.URL != "https://github.com/owner/repo/pull/42" {
		t.Errorf("url = %q", pr.URL)
	}
	if pr.Repo != "owner/repo" {
		t.Errorf("repo = %q", pr.Repo)
	}
	if pr.HeadRef == "" {
		t.Errorf("head_ref must be non-empty (endpoint requires it)")
	}
}

func TestRunPostMergeLocal_NoPRNumber_SkipsPOST(t *testing.T) {
	dir := newTestRepo(t, "chore: no pr marker", "https://github.com/owner/repo.git")
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("API_KEY", "test-key-1234567890")

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	if err := RunPostMergeLocal([]string{"--server", srv.URL}); err != nil {
		t.Fatalf("expected nil (fail-open), got %v", err)
	}
	if called {
		t.Errorf("expected no POST when HEAD commit has no (#N) marker")
	}
}

func TestRunPostMergeLocal_NonGitHubRemote_SkipsPOST(t *testing.T) {
	dir := newTestRepo(t, "feat: thing (#42)", "https://gitlab.com/owner/repo.git")
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("API_KEY", "test-key-1234567890")

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	if err := RunPostMergeLocal([]string{"--server", srv.URL}); err != nil {
		t.Fatalf("expected nil (fail-open), got %v", err)
	}
	if called {
		t.Errorf("non-GitHub remote must not POST (no API_KEY exfil to a derived host)")
	}
}

func TestRunPostMergeLocal_DryRun_NoPOST(t *testing.T) {
	dir := newTestRepo(t, "feat: x (#5)", "https://github.com/owner/repo.git")
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("API_KEY", "test-key-1234567890")

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	if err := RunPostMergeLocal([]string{"--dry-run", "--server", srv.URL}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if called {
		t.Errorf("--dry-run must not POST")
	}
}

func TestRunPostMergeLocal_ServerDown_FailOpen(t *testing.T) {
	dir := newTestRepo(t, "feat: x (#9)", "https://github.com/owner/repo.git")
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("API_KEY", "test-key-1234567890")

	// Port 1 is never listening → connection refused → must fail open (nil).
	if err := RunPostMergeLocal([]string{"--server", "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("server-down must be fail-open (nil), got %v", err)
	}
}

func TestRunPostMergeLocal_NoAPIKey_Skips(t *testing.T) {
	dir := newTestRepo(t, "feat: x (#3)", "https://github.com/owner/repo.git")
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir()) // empty config dir → LoadGlobalConfig no-ops
	t.Setenv("API_KEY", "")

	if err := RunPostMergeLocal(nil); err != nil {
		t.Fatalf("missing API_KEY must be fail-open (nil), got %v", err)
	}
}
