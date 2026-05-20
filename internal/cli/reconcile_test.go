package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/cli"
)

// TestResolveSince covers the --since flag and the 90-day default. These
// touch only the unexported helper indirectly via RunReconcile so we test
// the surface behaviour via end-to-end paths in TestRunReconcile_*.

// TestRunReconcile_NoAPIKey verifies the early-exit when API_KEY is unset.
func TestRunReconcile_NoAPIKey(t *testing.T) {
	t.Setenv("API_KEY", "")
	t.Setenv("PORT", "65535") // unreachable
	// Make sure no .env in cwd auto-loads API_KEY back in. tempdir cwd.
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	err := cli.RunReconcile(nil)
	if err == nil || !strings.Contains(err.Error(), "API_KEY") {
		t.Errorf("err = %v, want one mentioning API_KEY", err)
	}
}

// TestRunReconcile_HappyPath spins up an httptest server impersonating the
// wayneblacktea-server (GET /api/workspace/repos + POST
// /api/tasks/reconcile-merged-prs) and a tiny stub `gh` binary placed first
// on $PATH so reconcile resolves it instead of the real one.
//
// Only runs on Unix where shell-script stubs work and gh is conventionally a
// CLI binary.
func TestRunReconcile_HappyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell-script stub for gh")
	}

	apiKey := "test-key-happy"
	t.Setenv("API_KEY", apiKey)

	// httptest server impersonates the wayneblacktea-server endpoints.
	var (
		posted         bool
		postedPRsCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != apiKey {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/workspace/repos":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"name": "owner/repo"},
			})
		case "/api/tasks/reconcile-merged-prs":
			posted = true
			body, _ := io.ReadAll(r.Body)
			var p struct {
				MergedPRs []any `json:"merged_prs"`
			}
			_ = json.Unmarshal(body, &p)
			postedPRsCount = len(p.MergedPRs)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"applied":          1,
				"no_match":         0,
				"candidate_writes": 0,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	// Build a stub gh shell script that returns one merged PR. Place it in
	// a tempdir and prepend that dir to PATH so it shadows the real gh.
	stubDir := t.TempDir()
	// gh stub: argv handler that returns []byte JSON identical to
	// `gh pr list --json url,headRefName,mergedAt,title,body`. Split into
	// two physical lines to stay inside the 140-char `lll` budget.
	prJSON := `[{"url":"https://github.com/owner/repo/pull/1",` +
		`"headRefName":"feature/x","mergedAt":"2026-05-19T10:00:00Z",` +
		`"title":"feat: x","body":"closes #1"}]`
	stubScript := "#!/bin/sh\n" +
		"case \"$1\" in\n  auth) exit 0 ;;\nesac\n" +
		"echo '" + prJSON + "'\n"
	stubPath := filepath.Join(stubDir, "gh")
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil { //nolint:gosec // test fixture, intentional exec perm
		t.Fatalf("write stub: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+origPath)

	if err := cli.RunReconcile([]string{"--server", srv.URL, "--since", "2025-01-01"}); err != nil {
		t.Fatalf("RunReconcile: %v", err)
	}
	if !posted {
		t.Error("expected POST to /api/tasks/reconcile-merged-prs but none received")
	}
	if postedPRsCount != 1 {
		t.Errorf("posted PR count = %d, want 1", postedPRsCount)
	}
}

// TestRunReconcile_RejectsHostileSlug verifies the M-1 round-2 hardening:
// when fetchActiveRepos returns a slug that does not match validator.RepoSlugRe,
// the CLI must (a) NOT invoke gh with that slug as an argv segment, and
// (b) emit a stderr warning containing "invalid repo slug" so the operator
// sees what was filtered. The per-repo failure is swallowed (existing
// behaviour — one bad slug shouldn't kill the whole batch) so the run
// finishes nil; the assertions confirm the validator did its job.
func TestRunReconcile_RejectsHostileSlug(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell-script stub for gh")
	}

	apiKey := "test-key-hostile"
	t.Setenv("API_KEY", apiKey)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != apiKey {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/workspace/repos":
			// Hostile-slug fixture: shell-meta, path traversal, newline,
			// missing-slash. The fix-under-test rejects all of these
			// before they reach gh argv.
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"name": "evil;rm/repo"},
				{"name": "../etc/passwd"},
				{"name": "owner\nrepo"},
				{"name": "no-slash-here"},
			})
		case "/api/tasks/reconcile-merged-prs":
			// Any POST means the validator failed.
			t.Errorf("unexpected POST: hostile slugs must not produce a PR batch")
			http.Error(w, "should not be called", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	// gh stub records its argv to a marker file. The test then asserts that
	// no hostile slug ever appeared in any -R arg.
	stubDir := t.TempDir()
	markerPath := filepath.Join(stubDir, "gh-invocations.log")
	stubScript := "#!/bin/sh\n" +
		"case \"$1\" in\n  auth) exit 0 ;;\nesac\n" +
		"printf '%s\\n' \"$*\" >> " + markerPath + "\n" +
		"echo '[]'\n"
	stubPath := filepath.Join(stubDir, "gh")
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil { //nolint:gosec // test fixture, intentional exec perm
		t.Fatalf("write stub: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+origPath)

	// Capture stderr to assert the "invalid repo slug" warning is emitted.
	origStderr := os.Stderr
	rPipe, wPipe, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe: %v", pipeErr)
	}
	os.Stderr = wPipe
	t.Cleanup(func() { os.Stderr = origStderr })

	runErr := cli.RunReconcile([]string{"--server", srv.URL, "--since", "2025-01-01"})
	_ = wPipe.Close()
	stderrBytes, _ := io.ReadAll(rPipe)
	stderrOut := string(stderrBytes)

	if runErr != nil {
		t.Errorf("RunReconcile returned err=%v; per-repo failures should be swallowed", runErr)
	}
	if !strings.Contains(stderrOut, "invalid repo slug") {
		t.Errorf("stderr did not contain 'invalid repo slug'; got:\n%s", stderrOut)
	}
	// Verify gh was NOT invoked with any hostile slug. The marker file
	// either doesn't exist (no gh runs) or contains no hostile substring.
	if data, err := os.ReadFile(markerPath); err == nil { //nolint:gosec // G304: path is t.TempDir()-scoped
		invocations := string(data)
		hostileFragments := []string{"evil;", "../etc", "no-slash-here"}
		// owner\nrepo is hard to grep cleanly through a shell stub; the regex
		// rejection alone is the canonical assertion for that case.
		for _, frag := range hostileFragments {
			if strings.Contains(invocations, frag) {
				t.Errorf("gh was invoked with hostile slug fragment %q; invocations:\n%s",
					frag, invocations)
			}
		}
	}
}

// TestRunReconcile_GhMissing_ExitsZero verifies the soft-fail when gh isn't
// installed. PATH is overridden to a tempdir containing no `gh`.
func TestRunReconcile_GhMissing_ExitsZero(t *testing.T) {
	apiKey := "test-key-gh-missing" //nolint:gosec // G101: test fixture, not a real credential
	t.Setenv("API_KEY", apiKey)

	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	// Confirm gh is not resolvable.
	if _, err := exec.LookPath("gh"); err == nil {
		t.Skip("gh still resolvable through OS-level fallback PATH; skipping")
	}

	err := cli.RunReconcile([]string{"--server", "http://localhost:65534"})
	if err != nil {
		t.Errorf("expected exit 0 when gh is missing, got err = %v", err)
	}
}

// TestRunReconcile_InvalidSince covers --since rejection.
func TestRunReconcile_InvalidSince(t *testing.T) {
	t.Setenv("API_KEY", "x")
	err := cli.RunReconcile([]string{"--since", "not-a-date"})
	if err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("err = %v, want YYYY-MM-DD complaint", err)
	}
}

// TestRunReconcile_Help prints usage and exits 0.
func TestRunReconcile_Help(t *testing.T) {
	if err := cli.RunReconcile([]string{"--help"}); err != nil {
		t.Errorf("--help err = %v, want nil", err)
	}
}
