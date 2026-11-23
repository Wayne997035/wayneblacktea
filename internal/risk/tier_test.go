package risk

import "testing"

// [F1123-20] T0-T7 tier cases, expectations taken from guard's TestClassifyBash.
// [F1123-21] the wrapper_blocklist case pins sudo to exactly T6.
func TestRiskClassify_Tiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		command    string
		wantTier   Tier
		wantReason string // checked only when non-empty
	}{
		{name: "T0", command: "git status", wantTier: T0},
		{name: "T1", command: "go build ./...", wantTier: T1},
		{name: "T2", command: "python3 script.py", wantTier: T2},
		{name: "T3", command: "git add .", wantTier: T3},
		{name: "T4", command: "git rebase -i HEAD~3", wantTier: T4},
		{name: "T5", command: "rm -rf ./tmp", wantTier: T5},
		{name: "T6", command: "git push --force origin main", wantTier: T6},
		{name: "T7", command: "", wantTier: T7, wantReason: "empty command"},
		{name: "wrapper_blocklist", command: "sudo rm -rf /etc", wantTier: T6},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, reason := ClassifyBash(tc.command)
			if got != tc.wantTier {
				t.Errorf("ClassifyBash(%q) tier = got T%d want T%d (reason %q)", tc.command, got, tc.wantTier, reason)
			}
			if tc.wantReason != "" && reason != tc.wantReason {
				t.Errorf("ClassifyBash(%q) reason = got %q want %q", tc.command, reason, tc.wantReason)
			}
		})
	}
}
