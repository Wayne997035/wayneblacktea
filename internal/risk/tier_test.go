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

// [F1124-21] TestSplitPipeline and TestTokenize moved verbatim from internal/guard
// (WBT3 slice 2b) so the helper edge cases keep a direct assertion.

// TestSplitPipeline verifies the pipeline splitter handles quoted strings.
func TestSplitPipeline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		wantLen int
	}{
		{"ls", 1},
		{"ls && pwd", 2},
		{"ls || echo 'foo && bar'", 2}, // quoted && should not split
		{"a && b && c", 3},
		{"a; b; c", 3},
		{"a | b | c", 3},
		{"a && b || c; d", 4},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := splitPipeline(tc.input)
			if len(got) != tc.wantLen {
				t.Errorf("splitPipeline(%q) = %d segments, want %d: %v", tc.input, len(got), tc.wantLen, got)
			}
		})
	}
}

// TestTokenize verifies basic tokenization behaviour.
func TestTokenize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		wantToks []string
	}{
		{"git add .", []string{"git", "add", "."}},
		{"git commit -m 'feat: add'", []string{"git", "commit", "-m", "feat: add"}},
		{`echo "hello world"`, []string{"echo", "hello world"}},
		{"FOO=bar go test", []string{"FOO=bar", "go", "test"}},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := tokenize(tc.input)
			if len(got) != len(tc.wantToks) {
				t.Fatalf("tokenize(%q) = %v (len=%d), want %v (len=%d)",
					tc.input, got, len(got), tc.wantToks, len(tc.wantToks))
			}
			for i, tok := range got {
				if tok != tc.wantToks[i] {
					t.Errorf("tokenize(%q)[%d] = %q, want %q", tc.input, i, tok, tc.wantToks[i])
				}
			}
		})
	}
}
