package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// TestMatch_UnknownTool verifies unknown non-mcp tools default-deny at T5.
// Round 2 hardening (M5): better to over-flag during observe-phase than miss
// a destructive future tool.
func TestMatch_UnknownTool(t *testing.T) {
	t.Parallel()
	result := Match("UnknownFutureTool", json.RawMessage(`{}`), "/repo")
	if result.Tier != T5 {
		t.Errorf("Match(UnknownFutureTool) tier = %d, want T5(%d)", result.Tier, T5)
	}
	if result.MatcherName != "unknown" {
		t.Errorf("MatcherName = %q, want 'unknown'", result.MatcherName)
	}
}

// TestMatch_SafeToolAllowlist verifies known-safe tools classify at T0.
func TestMatch_SafeToolAllowlist(t *testing.T) {
	t.Parallel()
	tools := []string{
		"Read", "Glob", "Grep", "WebSearch", "WebFetch",
		"NotebookRead", "BashOutput", "KillShell", "ListMcpResources",
	}
	for _, name := range tools {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := Match(name, json.RawMessage(`{}`), "/repo")
			if result.Tier != T0 {
				t.Errorf("Match(%s) tier = %d, want T0", name, result.Tier)
			}
			if result.MatcherName != "allowlist" {
				t.Errorf("Match(%s) matcher = %q, want 'allowlist'", name, result.MatcherName)
			}
		})
	}
}

// TestMatch_MCPDestructiveVerbs verifies mcp__* tool names containing a
// destructive verb classify at T5.
func TestMatch_MCPDestructiveVerbs(t *testing.T) {
	t.Parallel()
	tools := []string{
		"mcp__github__delete_repo",
		"mcp__db__drop_table",
		"mcp__auth__revoke_token",
		"mcp__cluster__force_failover",
		"mcp__sandbox__destroy_workspace",
		"mcp__cache__clear_all",
		"mcp__db__reset_password",
	}
	for _, name := range tools {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := Match(name, json.RawMessage(`{}`), "/repo")
			if result.Tier != T5 {
				t.Errorf("Match(%s) tier = %d, want T5", name, result.Tier)
			}
			if result.MatcherName != "mcp" {
				t.Errorf("Match(%s) matcher = %q, want 'mcp'", name, result.MatcherName)
			}
		})
	}
}

// TestMatch_MCPMutationVerbs verifies mcp__* tool names containing a
// mutation (non-destructive) verb classify at T2.
func TestMatch_MCPMutationVerbs(t *testing.T) {
	t.Parallel()
	tools := []string{
		"mcp__gtd__add_task",
		"mcp__gtd__create_session",
		"mcp__gtd__confirm_plan",
		"mcp__gtd__set_priority",
		"mcp__gtd__update_task",
		"mcp__gtd__complete_task",
	}
	for _, name := range tools {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := Match(name, json.RawMessage(`{}`), "/repo")
			if result.Tier != T2 {
				t.Errorf("Match(%s) tier = %d, want T2", name, result.Tier)
			}
		})
	}
}

// TestMatch_MCPUnclassifiedVerb verifies mcp__* tools without a known verb
// fall back to T3 (mid-tier with would_deny=false).
func TestMatch_MCPUnclassifiedVerb(t *testing.T) {
	t.Parallel()
	tools := []string{
		"mcp__random__do_thing",
		"mcp__weather__forecast",
		"mcp__chat__send",
	}
	for _, name := range tools {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := Match(name, json.RawMessage(`{}`), "/repo")
			if result.Tier != T3 {
				t.Errorf("Match(%s) tier = %d, want T3", name, result.Tier)
			}
		})
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

// TestClassifyFilePath_ControlChars verifies paths with embedded \x00, \r,
// or \n are rejected at T7 with a clear reason. These characters break path
// semantics across OS layers (Linux honours the null terminator; macOS may
// not; auditing tools display only the prefix).
func TestClassifyFilePath_ControlChars(t *testing.T) {
	t.Parallel()
	const cwd = "/home/user/repo"
	tests := []struct {
		name     string
		filePath string
	}{
		{"null byte", "/home/user/repo/foo\x00.go"},
		{"carriage return", "/home/user/repo/foo\r.go"},
		{"newline", "/home/user/repo/foo\n.go"},
		{"null in relative", "foo\x00bar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tier, reason := classifyFilePath(tc.filePath, cwd, "Test")
			if tier != T7 {
				t.Errorf("classifyFilePath(%q) tier = T%d, want T7 (reason=%q)", tc.filePath, tier, reason)
			}
		})
	}
}

// TestClassifyFilePath_TildeRejected verifies leading "~" is rejected at T7
// with a reason explaining the operator must pass an absolute or repo-
// relative path. filepath.Join does NOT shell-expand tilde.
func TestClassifyFilePath_TildeRejected(t *testing.T) {
	t.Parallel()
	const cwd = "/home/user/repo"
	tests := []string{
		"~/secrets",
		"~root/.ssh/authorized_keys",
		"~",
	}
	for _, fp := range tests {
		t.Run(fp, func(t *testing.T) {
			t.Parallel()
			tier, reason := classifyFilePath(fp, cwd, "Test")
			if tier != T7 {
				t.Errorf("classifyFilePath(%q) tier = T%d, want T7 (reason=%q)", fp, tier, reason)
			}
		})
	}
}

// TestClassifyFilePath_NewFileWriteInRepo verifies that a write to a
// not-yet-existing path inside the repo still classifies as T1 (regression
// guard for the EvalSymlinks fallback: it must not mis-flag new files).
func TestClassifyFilePath_NewFileWriteInRepo(t *testing.T) {
	t.Parallel()
	repoDir := t.TempDir()
	// Path that does not exist yet — typical Write tool case.
	target := filepath.Join(repoDir, "newfile", "deeper.go")
	tier, reason := classifyFilePath(target, repoDir, "Write")
	if tier != T1 {
		t.Errorf("classifyFilePath(non-existent in-repo) tier = T%d, want T1 (reason=%q)", tier, reason)
	}
}

// TestClassifyFilePath_SymlinkOutsideRepo verifies that an in-repo symlink
// pointing outside the repo is classified as T5 — Clean alone wouldn't catch
// this, EvalSymlinks does.
func TestClassifyFilePath_SymlinkOutsideRepo(t *testing.T) {
	t.Parallel()
	skipOnWindows(t, "symlink fixture relies on POSIX permissions")
	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(repoDir, 0o750); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o750); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	target := filepath.Join(outsideDir, "secrets.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(repoDir, "symlink-to-outside")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tier, reason := classifyFilePath(link, repoDir, "Edit")
	if tier != T5 {
		t.Errorf("classifyFilePath(in-repo symlink → outside) tier = T%d, want T5 (reason=%q)", tier, reason)
	}
}

// TestClassifyFilePath_SymlinkInsideRepo verifies an in-repo symlink to
// another in-repo path stays at T1.
func TestClassifyFilePath_SymlinkInsideRepo(t *testing.T) {
	t.Parallel()
	skipOnWindows(t, "symlink fixture relies on POSIX permissions")
	repoDir := t.TempDir()
	subDir := filepath.Join(repoDir, "sub")
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	target := filepath.Join(subDir, "real.go")
	if err := os.WriteFile(target, []byte("// real"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(repoDir, "alias.go")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tier, reason := classifyFilePath(link, repoDir, "Edit")
	if tier != T1 {
		t.Errorf("classifyFilePath(in-repo symlink → in-repo) tier = T%d, want T1 (reason=%q)", tier, reason)
	}
}
