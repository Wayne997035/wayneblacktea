package mcp

import (
	"strings"
	"testing"
)

// TestNoiseFilterWrappers_DelegateToValidator is a smoke test confirming the
// package-local wrappers in noise_filter.go actually reach
// internal/validator's implementation rather than re-implementing the logic.
// The exhaustive rule matrix (script-tag / fence / length / control-char
// cases) lives in internal/validator/noise_test.go — this file only proves
// the wiring, so it deliberately stays thin.
func TestNoiseFilterWrappers_DelegateToValidator(t *testing.T) {
	t.Parallel()

	t.Run("checkField rejects script tag", func(t *testing.T) {
		reason := checkField("title", `<script>alert(1)</script>`)
		if reason == "" || !strings.Contains(reason, "injection") {
			t.Errorf("expected injection rejection, got %q", reason)
		}
	})

	t.Run("checkField passes clean value", func(t *testing.T) {
		if reason := checkField("title", "clean value"); reason != "" {
			t.Errorf("expected clean pass, got reason %q", reason)
		}
	})

	t.Run("checkDecisionNoise rejects markdown fence", func(t *testing.T) {
		reason := checkDecisionNoise("t", "c", "d", "```rm -rf /```")
		if reason == "" || !strings.Contains(reason, "markdown fence") {
			t.Errorf("expected markdown fence rejection, got %q", reason)
		}
	})

	t.Run("checkCommandField rejects newline", func(t *testing.T) {
		reason := checkCommandField("field", "a\nb")
		if reason == "" || !strings.Contains(reason, "newline") {
			t.Errorf("expected newline rejection, got %q", reason)
		}
	})

	t.Run("checkHandoffNoise rejects script tag", func(t *testing.T) {
		reason := checkHandoffNoise("intent", "<script>evil</script>")
		if reason == "" || !strings.Contains(reason, "injection") {
			t.Errorf("expected injection rejection, got %q", reason)
		}
	})

	t.Run("sanitizeTags strips disallowed chars", func(t *testing.T) {
		got, reason := sanitizeTags([]string{"hello!"})
		if reason != "" {
			t.Fatalf("unexpected rejection: %q", reason)
		}
		if len(got) != 1 || got[0] != "hello" {
			t.Errorf("got %v, want [\"hello\"]", got)
		}
	})

	t.Run("sanitizeTags rejects over-count", func(t *testing.T) {
		_, reason := sanitizeTags(make([]string, maxTagCount+1))
		if reason == "" || !strings.Contains(reason, "20-entry") {
			t.Errorf("expected 20-entry rejection, got %q", reason)
		}
	})
}
