package sanitize_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
)

func TestContainsToolCallFragment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		// Clean strings should pass.
		{name: "empty string", input: "", want: false},
		{name: "plain text", input: "Continue implementing the handler", want: false},
		{name: "markdown heading", input: "## Fix 1 — Tag-noise detection", want: false},
		{name: "markdown bold", input: "Handler skeleton done, **repo layer** missing", want: false},

		// HTML-style tags: xmlTagRe uses an allowlist of known MCP field names,
		// so short HTML tags like </b> are NOT flagged (not in the allowlist).
		{name: "html bold closing tag", input: "some <b>bold</b> text", want: false},

		// Case-insensitive: uppercase or mixed-case tags must still be detected.
		{name: "uppercase tag bypasses old regex", input: "</Intent> fragment", want: true},

		// Tool-call serialization fragments — all must be caught.
		{name: "closing intent tag", input: "some text </intent> more", want: true},
		{name: "closing context_summary tag", input: "</context_summary>", want: true},
		{name: "closing invoke tag with space", input: "foo </invoke> bar", want: true},
		{name: "opening invoke tag", input: "prefix <invoke name=\"set_session_handoff\">", want: true},
		{name: "closing invoke tag tight", input: "</invoke>", want: true},
		{name: "parameter tag", input: `<parameter name="intent">value</parameter>`, want: true},
		{name: "parameter tag with spaces", input: `start <parameter  name="context_summary"> end`, want: true},
		{name: "closing snake_case tag", input: "see </repo_name> here", want: true},
		{name: "multiple fragments", input: "</intent>\n<parameter name=\"context_summary\">", want: true},
		{name: "embedded in longer text", input: "Session handoff </intent> for next session", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitize.ContainsToolCallFragment(tc.input)
			if got != tc.want {
				t.Errorf("ContainsToolCallFragment(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateNoTagNoise(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		input   string
		wantErr error
	}{
		// Clean input.
		{name: "empty", input: "", wantErr: nil},
		{name: "clean intent text", input: "Continue implementing the handler after refactor", wantErr: nil},
		{name: "clean markdown", input: "# Plan\n- step 1\n- step 2", wantErr: nil},

		// Contaminated input must return ErrTagNoise.
		{name: "closing intent tag", input: "</intent>", wantErr: sanitize.ErrTagNoise},
		{name: "parameter tag", input: `<parameter name="context_summary">text`, wantErr: sanitize.ErrTagNoise},
		{name: "opening invoke tag", input: `<invoke name="log_decision">`, wantErr: sanitize.ErrTagNoise},
		{name: "closing invoke with bracket", input: "</invoke>", wantErr: sanitize.ErrTagNoise},
		{name: "embedded fragment in prose", input: "The session ended. </invoke> Next steps:", wantErr: sanitize.ErrTagNoise},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := sanitize.ValidateNoTagNoise(tc.input)
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("ValidateNoTagNoise(%q) returned unexpected error: %v", tc.input, err)
				}
			} else {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("ValidateNoTagNoise(%q) = %v, want %v", tc.input, err, tc.wantErr)
				}
			}
		})
	}
}

// TestValidateNoTagNoise_ReportsExcerpt is U2's accept criterion. It
// simulates the exact wrap convention every real caller already uses
// (decision/store.go:44 — fmt.Errorf("log_decision: rationale %w", err)) so
// the assertion matches what a caller of log_decision actually sees: the
// field name (supplied by the caller's own wrap, unchanged by this fix) AND
// a bounded excerpt around the matched fragment (the part this fix adds).
func TestValidateNoTagNoise_ReportsExcerpt(t *testing.T) {
	rationale := "see </invoke> tag"
	inner := sanitize.ValidateNoTagNoise(rationale)
	if inner == nil {
		t.Fatalf("ValidateNoTagNoise(%q) = nil, want ErrTagNoise", rationale)
	}
	err := fmt.Errorf("log_decision: rationale %w", inner)

	if !errors.Is(err, sanitize.ErrTagNoise) {
		t.Errorf("errors.Is(err, ErrTagNoise) = false, want true (err: %v)", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "rationale") {
		t.Errorf("error message %q does not contain field name %q", msg, "rationale")
	}
	if !strings.Contains(msg, "</invoke>") {
		t.Errorf("error message %q does not contain the matched fragment %q", msg, "</invoke>")
	}
}

// TestValidateNoTagNoise_ExcerptIsBounded verifies the excerpt window does
// not echo an entire large payload back into the error message — only a
// bounded run of runes around the matched fragment (backend-security-design.md
// §5.4 — self-check item 4: the fix for a bad error message must not become
// its own information-disclosure/amplification surface).
func TestValidateNoTagNoise_ExcerptIsBounded(t *testing.T) {
	payload := strings.Repeat("x", 5000) + "</invoke>" + strings.Repeat("y", 5000)
	err := sanitize.ValidateNoTagNoise(payload)
	if err == nil {
		t.Fatal("expected ErrTagNoise, got nil")
	}
	msg := err.Error()
	if len(msg) >= len(payload) {
		t.Errorf("error message length %d is not bounded relative to a %d-rune payload", len(msg), len(payload))
	}
	if strings.Contains(msg, strings.Repeat("x", 100)) || strings.Contains(msg, strings.Repeat("y", 100)) {
		t.Errorf("error message leaked far more of the payload than the excerpt window should allow: %q", msg)
	}
}
