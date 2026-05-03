package guard

import (
	"encoding/json"
	"testing"
)

// TestMatch_Bash verifies the Bash matcher delegates to the classifier correctly.
func TestMatch_Bash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantTier RiskTier
		wantName string
	}{
		{
			name:     "read-only git status",
			input:    `{"command":"git status"}`,
			wantTier: T0,
			wantName: "bash",
		},
		{
			name:     "destructive rm -rf",
			input:    `{"command":"rm -rf ./dist"}`,
			wantTier: T5,
			wantName: "bash",
		},
		{
			name:     "force push",
			input:    `{"command":"git push --force origin main"}`,
			wantTier: T6,
			wantName: "bash",
		},
		{
			name:     "malformed JSON",
			input:    `{"command": not-json`,
			wantTier: T7,
			wantName: "bash",
		},
		{
			name:     "empty command field",
			input:    `{"command":""}`,
			wantTier: T7,
			wantName: "bash",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := Match("Bash", json.RawMessage(tc.input), "/repo")
			if result.Tier != tc.wantTier {
				t.Errorf("Match(Bash, %q) tier = %d, want %d", tc.input, result.Tier, tc.wantTier)
			}
			if result.MatcherName != tc.wantName {
				t.Errorf("Match(Bash, %q) matcher = %q, want %q", tc.input, result.MatcherName, tc.wantName)
			}
		})
	}
}

// TestMatch_Edit verifies the Edit matcher classifies file paths correctly.
func TestMatch_Edit(t *testing.T) {
	t.Parallel()

	const cwd = "/home/user/myrepo"

	tests := []struct {
		name     string
		input    string
		wantTier RiskTier
	}{
		{
			name:     "in-repo file",
			input:    `{"file_path":"/home/user/myrepo/internal/foo.go"}`,
			wantTier: T1,
		},
		{
			name:     "relative in-repo file",
			input:    `{"file_path":"internal/foo.go"}`,
			wantTier: T1,
		},
		{
			name:     "path traversal outside repo",
			input:    `{"file_path":"/etc/passwd"}`,
			wantTier: T5,
		},
		{
			name:     "dotdot traversal",
			input:    `{"file_path":"../../etc/passwd"}`,
			wantTier: T5,
		},
		{
			name:     "malformed JSON",
			input:    `{"file_path": not-json`,
			wantTier: T7,
		},
		{
			name:     "empty file_path",
			input:    `{"file_path":""}`,
			wantTier: T7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := Match("Edit", json.RawMessage(tc.input), cwd)
			if result.Tier != tc.wantTier {
				t.Errorf("Match(Edit, %q) tier = %d, want %d", tc.input, result.Tier, tc.wantTier)
			}
			if result.MatcherName != "edit" {
				t.Errorf("Match(Edit, %q) matcher = %q, want %q", tc.input, result.MatcherName, "edit")
			}
		})
	}
}

// TestMatch_Write verifies the Write matcher behaves like Edit.
func TestMatch_Write(t *testing.T) {
	t.Parallel()

	const cwd = "/home/user/myrepo"

	tests := []struct {
		name     string
		input    string
		wantTier RiskTier
	}{
		{
			name:     "in-repo write",
			input:    `{"file_path":"/home/user/myrepo/README.md"}`,
			wantTier: T1,
		},
		{
			name:     "outside repo write",
			input:    `{"file_path":"/var/log/app.log"}`,
			wantTier: T5,
		},
		{
			name:     "malformed JSON",
			input:    `not json`,
			wantTier: T7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := Match("Write", json.RawMessage(tc.input), cwd)
			if result.Tier != tc.wantTier {
				t.Errorf("Match(Write, %q) tier = %d, want %d", tc.input, result.Tier, tc.wantTier)
			}
			if result.MatcherName != "write" {
				t.Errorf("Match(Write, %q) matcher = %q, want %q", tc.input, result.MatcherName, "write")
			}
		})
	}
}

// TestMatch_MultiEdit verifies the MultiEdit matcher picks the max tier across edits.
func TestMatch_MultiEdit(t *testing.T) {
	t.Parallel()

	const cwd = "/home/user/myrepo"

	tests := []struct {
		name     string
		input    string
		wantTier RiskTier
	}{
		{
			name:     "all in-repo edits",
			input:    `{"edits":[{"file_path":"/home/user/myrepo/a.go"},{"file_path":"/home/user/myrepo/b.go"}]}`,
			wantTier: T1,
		},
		{
			name:     "one outside-repo edit",
			input:    `{"edits":[{"file_path":"/home/user/myrepo/a.go"},{"file_path":"/etc/passwd"}]}`,
			wantTier: T5,
		},
		{
			name:     "empty edits list",
			input:    `{"edits":[]}`,
			wantTier: T0,
		},
		{
			name:     "malformed JSON",
			input:    `{"edits": not-json`,
			wantTier: T7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := Match("MultiEdit", json.RawMessage(tc.input), cwd)
			if result.Tier != tc.wantTier {
				t.Errorf("Match(MultiEdit, %q) tier = %d, want %d", tc.input, result.Tier, tc.wantTier)
			}
			if result.MatcherName != "multiedit" {
				t.Errorf("Match(MultiEdit, %q) matcher = %q, want %q", tc.input, result.MatcherName, "multiedit")
			}
		})
	}
}

// TestMatch_Task verifies the Task (sub-agent) matcher always returns T4.
func TestMatch_Task(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "codex sub-agent",
			input: `{"subagent_type":"codex:rescue","prompt":"Fix the failing tests"}`,
		},
		{
			name:  "claude engineer agent",
			input: `{"subagent_type":"engineer","prompt":"Implement the feature"}`,
		},
		{
			name:  "malformed input still T4",
			input: `not json at all`,
		},
		{
			name:  "empty prompt",
			input: `{"subagent_type":"reviewer","prompt":""}`,
		},
		{
			name:  "nested sub-agent dispatch",
			input: `{"subagent_type":"codex:rescue","prompt":"wbt guard test nested"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := Match("Task", json.RawMessage(tc.input), "/repo")
			if result.Tier != T4 {
				t.Errorf("Match(Task, %q) tier = %d, want T4(%d)", tc.input, result.Tier, T4)
			}
			if result.MatcherName != "task" {
				t.Errorf("Match(Task, %q) matcher = %q, want %q", tc.input, result.MatcherName, "task")
			}
		})
	}
}

// TestMatch_UnknownTool verifies unknown tools return T4.
func TestMatch_UnknownTool(t *testing.T) {
	t.Parallel()
	result := Match("UnknownFutureTool", json.RawMessage(`{}`), "/repo")
	if result.Tier != T4 {
		t.Errorf("Match(UnknownFutureTool) tier = %d, want T4(%d)", result.Tier, T4)
	}
}

// TestClassifyFilePath_EdgeCases covers path traversal and boundary conditions.
func TestClassifyFilePath_EdgeCases(t *testing.T) {
	t.Parallel()

	const cwd = "/home/user/repo"

	tests := []struct {
		name     string
		filePath string
		wantTier RiskTier
	}{
		{
			name:     "exact cwd path",
			filePath: "/home/user/repo",
			wantTier: T1, // equal to cwd is fine
		},
		{
			name:     "sub-path",
			filePath: "/home/user/repo/internal/foo.go",
			wantTier: T1,
		},
		{
			name:     "sibling directory",
			filePath: "/home/user/other-repo/evil.go",
			wantTier: T5,
		},
		{
			name:     "prefix but not sub-path",
			filePath: "/home/user/repohijack/evil.go",
			wantTier: T5,
		},
		{
			name:     "absolute path outside home",
			filePath: "/etc/passwd",
			wantTier: T5,
		},
		{
			name:     "relative dotdot resolves outside",
			filePath: "../../etc/passwd",
			wantTier: T5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tier, _ := classifyFilePath(tc.filePath, cwd, "Test")
			if tier != tc.wantTier {
				t.Errorf("classifyFilePath(%q, %q) = T%d, want T%d", tc.filePath, cwd, tier, tc.wantTier)
			}
		})
	}
}
