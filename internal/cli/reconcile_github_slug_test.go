package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/cli"
)

// TestRunReconcile_UsesGitHubSlug pins [F0925-31]: reconcile queries gh with
// repos.github_slug, never repos.name, and skips a repo without one with a
// stderr line naming it. Before the fix the CLI passed name "a" to gh.
func TestRunReconcile_UsesGitHubSlug(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX shell-script stub for gh")
	}
	apiKey := "test-key-slug" //nolint:gosec // G101: test fixture, not a real credential
	t.Setenv("API_KEY", apiKey)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != apiKey {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/workspace/repos":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"name": "a", "github_slug": "Wayne997035/a"},
				{"name": "b"},
			})
		case "/api/tasks/reconcile-merged-prs":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	stubDir := t.TempDir()
	markerPath := filepath.Join(stubDir, "gh-invocations.log")
	stubScript := "#!/bin/sh\n" +
		"case \"$1\" in\n  auth) exit 0 ;;\nesac\n" +
		"printf '%s\\n' \"$*\" >> " + markerPath + "\n" +
		"echo '[]'\n"
	if err := os.WriteFile(filepath.Join(stubDir, "gh"), []byte(stubScript), 0o755); err != nil { //nolint:gosec // test fixture, intentional exec perm
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	origStderr := os.Stderr
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = wPipe
	t.Cleanup(func() { os.Stderr = origStderr })

	runErr := cli.RunReconcile([]string{"--server", srv.URL, "--since", "2025-01-01"})
	_ = wPipe.Close()
	stderrBytes, _ := io.ReadAll(rPipe)
	if runErr != nil {
		t.Fatalf("RunReconcile: %v", runErr)
	}
	data, err := os.ReadFile(markerPath) //nolint:gosec // G304: path is t.TempDir()-scoped
	if err != nil {
		t.Fatalf("gh was never invoked: %v", err)
	}
	invocations := string(data)
	if !strings.Contains(invocations, "-R Wayne997035/a") {
		t.Errorf("gh must be queried with the github_slug; invocations:\n%s", invocations)
	}
	if strings.Contains(invocations, "-R a ") || strings.Contains(invocations, "-R b") {
		t.Errorf("gh must never be queried with repos.name; invocations:\n%s", invocations)
	}
	if !strings.Contains(string(stderrBytes), "skip b") {
		t.Errorf("stderr must name the skipped repo; got:\n%s", stderrBytes)
	}
}
