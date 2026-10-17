package scheduler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// squashSpace collapses every run of whitespace to a single space so a clause
// compares equal wherever a copy wraps or indents it.
func squashSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestPendingProposalsPruneExclusion_MatchesTaskfileAndSQLite [F204-05] pins the
// permanent-retention clause for 'scheduler:decision_outcome_review' task rows
// across its three copies. The scheduler's DELETE uses
// pendingProposalsPruneKeepReviewTasks directly; the manual
// `pending-proposals-prune` Taskfile target is a psql string and the SQLite prune
// is raw SQL in another package, neither of which Go can reference, so this test
// reads their source text and requires the constant (whitespace-normalised) to
// appear in it. A copy that loses COALESCE, or turns AND NOT into OR NOT, no
// longer contains the constant and fails here — and so does the constant itself
// drifting away from the copies.
func TestPendingProposalsPruneExclusion_MatchesTaskfileAndSQLite(t *testing.T) {
	want := squashSpace(pendingProposalsPruneKeepReviewTasks)
	if want == "" {
		t.Fatal("pendingProposalsPruneKeepReviewTasks is empty; the comparison below would pass for any file")
	}

	tests := []struct {
		name string
		path string
	}{
		{name: "Taskfile pending-proposals-prune", path: "../../build/Taskfile.yml"},
		{name: "sqlite MarkAndDeleteStaleProposals", path: "../storage/sqlite/proposal.go"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Clean(tc.path))
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			if !strings.Contains(squashSpace(string(raw)), want) {
				t.Errorf("%s no longer contains the exclusion clause %q; "+
					"the manual and scheduled prune would delete different rows", tc.path, want)
			}
		})
	}
}
