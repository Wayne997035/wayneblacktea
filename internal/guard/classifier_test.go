package guard

import (
	"testing"
)

// TestClassifyBash covers all 8 tiers plus pipeline/chaining and edge cases.
// This is the primary table-driven test for the Bash classifier.
func TestClassifyBash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		command  string
		wantTier RiskTier
	}{
		// T0: read-only
		{name: "ls", command: "ls -la", wantTier: T0},
		{name: "cat file", command: "cat /etc/hosts", wantTier: T0},
		{name: "pwd", command: "pwd", wantTier: T0},
		{name: "git status", command: "git status", wantTier: T0},
		{name: "git log", command: "git log --oneline -10", wantTier: T0},
		{name: "git diff", command: "git diff HEAD", wantTier: T0},
		{name: "grep pattern", command: "grep -r pattern ./src", wantTier: T0},
		{name: "find files", command: "find . -name '*.go'", wantTier: T0},

		// T1: build/test
		{name: "go build", command: "go build ./...", wantTier: T1},
		{name: "go test", command: "go test ./...", wantTier: T1},
		{name: "task check", command: "task check", wantTier: T1},
		{name: "npm test", command: "npm test", wantTier: T1},
		{name: "golangci-lint", command: "golangci-lint run ./...", wantTier: T1},

		// T2: local run
		{name: "go run", command: "go run main.go", wantTier: T2},
		{name: "python script", command: "python3 script.py", wantTier: T2},
		{name: "node script", command: "node index.js", wantTier: T2},

		// T3: safe git mutation
		{name: "git add", command: "git add .", wantTier: T3},
		{name: "git commit", command: "git commit -m 'feat: add feature'", wantTier: T3},
		{name: "git checkout new branch", command: "git checkout -b feature/new", wantTier: T3},
		{name: "git stash", command: "git stash push", wantTier: T3},

		// T4: risky git mutation
		{name: "git checkout existing", command: "git checkout main", wantTier: T4},
		{name: "git rebase", command: "git rebase -i HEAD~3", wantTier: T4},
		{name: "git merge", command: "git merge feature/branch", wantTier: T4},
		{name: "git reset soft", command: "git reset --soft HEAD~1", wantTier: T4},
		{name: "git pull", command: "git pull origin main", wantTier: T4},

		// T5: destructive local
		{name: "rm file", command: "rm somefile.txt", wantTier: T5},
		{name: "rm -rf", command: "rm -rf ./tmp", wantTier: T5},
		{name: "git reset hard", command: "git reset --hard HEAD", wantTier: T5},
		{name: "git clean", command: "git clean -fd", wantTier: T5},

		// T6: destructive remote
		{name: "git push", command: "git push origin main", wantTier: T6},
		{name: "git push force", command: "git push --force origin main", wantTier: T6},
		{name: "gh pr merge", command: "gh pr merge 42 --squash", wantTier: T6},
		{name: "ssh", command: "ssh user@host 'ls'", wantTier: T6},

		// T7: unknown
		{name: "empty command", command: "", wantTier: T7},
		{name: "unknown command", command: "xyzzy --help", wantTier: T7},

		// Pipeline: highest tier in pipeline wins
		{name: "pipeline: rm inside &&", command: "go build ./... && rm -rf ./tmp", wantTier: T5},
		{name: "pipeline: push after commit", command: "git commit -m 'msg' && git push", wantTier: T6},
		{name: "pipeline: safe chain", command: "go build ./... && go test ./...", wantTier: T1},
		{name: "pipeline: force push in ||", command: "git diff || git push --force", wantTier: T6},
		{name: "semicolon chain", command: "ls -la; rm badfile", wantTier: T5},

		// Edge cases
		{name: "whitespace only", command: "   ", wantTier: T7},
		{name: "env prefix + go test", command: "GOFLAGS=-race go test ./...", wantTier: T1},
		{name: "git checkout -B", command: "git checkout -B feature/x", wantTier: T3},
		{name: "git push -f shorthand", command: "git push -f origin main", wantTier: T6},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _ := ClassifyBash(tc.command)
			if got != tc.wantTier {
				t.Errorf("ClassifyBash(%q) tier = %d, want %d", tc.command, got, tc.wantTier)
			}
		})
	}
}

// TestClassifyBash_ReasonNonEmpty verifies that every classified command
// returns a non-empty reason string.
func TestClassifyBash_ReasonNonEmpty(t *testing.T) {
	t.Parallel()
	commands := []string{
		"ls -la",
		"git push --force",
		"rm -rf /",
		"go test ./...",
		"unknown_cmd --flag",
		"",
	}
	for _, cmd := range commands {
		_, reason := ClassifyBash(cmd)
		if reason == "" {
			t.Errorf("ClassifyBash(%q) returned empty reason", cmd)
		}
	}
}

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
