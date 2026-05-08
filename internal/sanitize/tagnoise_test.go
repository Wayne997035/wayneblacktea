package sanitize_test

import (
	"errors"
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

		// HTML-style tags — the regex does match short single-letter closing tags
		// like </b> because the data is plain text/markdown, not HTML. The false
		// positive rate is acceptable compared to the security gain.
		// Document explicitly: a closing tag like </b> IS flagged.
		{name: "html bold closing tag", input: "some <b>bold</b> text", want: true},

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
