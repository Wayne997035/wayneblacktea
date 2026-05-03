package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractFilePath verifies extractFilePath handles every input shape the
// PreToolUse payload can have.
func TestExtractFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"empty raw", "", ""},
		{"valid file_path", `{"file_path":"/tmp/foo.go"}`, "/tmp/foo.go"},
		{"absent file_path", `{"command":"ls"}`, ""},
		{"malformed JSON", `{"file_path":`, ""},
		{"non-string file_path", `{"file_path":123}`, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractFilePath(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("extractFilePath(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestConfigureSlog_WritesToTempFile verifies configureSlog redirects the
// default slog handler to a file in TempDir (so PreToolUse hook output does
// not surface to the Claude Code user terminal as warnings).
//
// Cannot use t.Parallel: configureSlog mutates the process-global default
// handler. The test restores the previous default on cleanup.
func TestConfigureSlog_WritesToTempFile(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Force TempDir to a fresh location for this test so we can assert the
	// file appears AND we don't pollute a shared /tmp/wbt-guard.log.
	tempRoot := t.TempDir()
	t.Setenv("TMPDIR", tempRoot)

	configureSlog()

	// Emit a Warn so the handler actually writes.
	slog.Warn("test-marker", "k", "v")

	logPath := filepath.Join(tempRoot, "wbt-guard.log")
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("expected log file %q to exist: %v", logPath, err)
	}
	// Mode 0600 contract — only the operator should be able to read the
	// audit log; group/world-readable would re-introduce the leak vector
	// the marker-perm-warn check is meant to prevent.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("log file %q mode = %#o, want group/world unreadable (0600)", logPath, perm)
	}
	data, err := os.ReadFile(logPath) //nolint:gosec // path is t.TempDir() + constant.
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(data) == 0 {
		t.Error("log file is empty after slog.Warn")
	}
}
