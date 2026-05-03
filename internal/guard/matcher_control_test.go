package guard

import (
	"encoding/json"
	"testing"
)

// TestMatch_Edit_PathTraversalControlChar verifies the Edit matcher escalates
// a path containing a literal NUL byte to T7. Go's encoding/json decodes JSON
// uXXXX escapes to literal Unicode runes, so an LLM-emitted "u0000" inside
// file_path becomes a NUL in the resulting Go string. classifyFilePath then
// rejects on the control-char check.
func TestMatch_Edit_PathTraversalControlChar(t *testing.T) {
	t.Parallel()
	const cwd = "/home/user/myrepo"
	// Build the payload programmatically so the source file stays ASCII.
	payload, err := json.Marshal(map[string]string{
		"file_path": "/home/user/myrepo/foo\x00.go",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	result := Match("Edit", payload, cwd)
	if result.Tier != T7 {
		t.Errorf("Match(Edit, control-char path) tier = T%d, want T7", result.Tier)
	}
}
