package handler_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
)

// repoNameLineRe matches the REPO_NAME env entry of the reconcile workflow step.
var repoNameLineRe = regexp.MustCompile(`(?m)^\s*REPO_NAME:\s*(.+?)\s*$`)

// [F0925-35] The gtd-reconcile workflow posts REPO_NAME to the reconcile
// endpoint, which rejects anything that is not an owner/repo slug
// (validator.RepoSlugRe, reconcile_handler.go). github.repository is
// "owner/repo"; github.event.repository.name is the bare repo name and would
// make every reconcile call fail with 400. A workflow edit is not compiled or
// tested anywhere else, so this test pins the value.
func TestReconcileWorkflow_SendsOwnerRepoSlug(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../.github/workflows/gtd-reconcile.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}

	matches := repoNameLineRe.FindAllStringSubmatch(string(raw), -1)
	if len(matches) != 1 {
		t.Fatalf("want exactly one REPO_NAME entry in gtd-reconcile.yml, got %d", len(matches))
	}
	if got, want := matches[0][1], "${{ github.repository }}"; got != want {
		t.Fatalf("REPO_NAME = %q, want %q (the reconcile endpoint only accepts owner/repo)", got, want)
	}

	// Why the value matters: what each context field expands to on GitHub.
	if !validator.RepoSlugRe.MatchString("Wayne997035/wayneblacktea") {
		t.Fatal("an owner/repo slug (github.repository) must pass RepoSlugRe")
	}
	if validator.RepoSlugRe.MatchString("wayneblacktea") {
		t.Fatal("a bare repo name (github.event.repository.name) must fail RepoSlugRe")
	}
}
