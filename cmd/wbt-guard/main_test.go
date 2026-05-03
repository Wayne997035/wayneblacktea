package main

import (
	"encoding/json"
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
