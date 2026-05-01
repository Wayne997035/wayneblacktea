package mcp

import (
	"strings"
	"testing"
)

func TestCheckField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fieldName  string
		value      string
		wantNoisy  bool
		wantSubstr string // substring expected in reason when noisy
	}{
		{
			name:      "clean short value passes",
			fieldName: "title",
			value:     "Use pgx/v5 pool for connection management",
			wantNoisy: false,
		},
		{
			name:       "value over 5000 chars is rejected",
			fieldName:  "rationale",
			value:      strings.Repeat("a", maxFieldLen+1),
			wantNoisy:  true,
			wantSubstr: "5000-character",
		},
		{
			name:      "exact 5000 chars is accepted",
			fieldName: "context",
			value:     strings.Repeat("b", maxFieldLen),
			wantNoisy: false,
		},
		{
			name:       "script tag is rejected",
			fieldName:  "title",
			value:      `Deploy <script>alert(1)</script> service`,
			wantNoisy:  true,
			wantSubstr: "injection",
		},
		{
			name:       "script tag case-insensitive is rejected",
			fieldName:  "context",
			value:      `<SCRIPT SRC="evil.js">`,
			wantNoisy:  true,
			wantSubstr: "injection",
		},
		{
			name:       "closing script tag is rejected",
			fieldName:  "decision",
			value:      `</script>`,
			wantNoisy:  true,
			wantSubstr: "injection",
		},
		{
			name:       "markdown fence is rejected",
			fieldName:  "rationale",
			value:      "Use docker\n```bash\nrm -rf /\n```",
			wantNoisy:  true,
			wantSubstr: "markdown fence",
		},
		{
			name:      "empty value passes",
			fieldName: "title",
			value:     "",
			wantNoisy: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason := checkField(tc.fieldName, tc.value)
			if tc.wantNoisy {
				if reason == "" {
					t.Errorf("expected noisy reason but got empty string")
				}
				if tc.wantSubstr != "" && !strings.Contains(reason, tc.wantSubstr) {
					t.Errorf("reason %q does not contain %q", reason, tc.wantSubstr)
				}
			} else if reason != "" {
				t.Errorf("expected clean but got reason: %q", reason)
			}
		})
	}
}

func TestCheckDecisionNoise(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		title     string
		ctx       string
		decision  string
		rationale string
		wantNoisy bool
		wantMsg   string
	}{
		{
			name:      "all clean fields pass",
			title:     "Switch DB driver",
			ctx:       "pgx/v4 is deprecated",
			decision:  "Adopt pgx/v5",
			rationale: "Better performance and maintained upstream",
			wantNoisy: false,
		},
		{
			name:      "identical decision and rationale rejected",
			title:     "Use Redis cache",
			ctx:       "latency issue",
			decision:  "add redis",
			rationale: "add redis",
			wantNoisy: true,
			wantMsg:   "identical",
		},
		{
			name:      "title too long rejected",
			title:     strings.Repeat("x", maxFieldLen+1),
			ctx:       "ok",
			decision:  "ok",
			rationale: "different ok",
			wantNoisy: true,
			wantMsg:   "5000-character",
		},
		{
			name:      "context with script tag rejected",
			title:     "Deploy new feature",
			ctx:       "<script>exfiltrate(document.cookie)</script>",
			decision:  "deploy",
			rationale: "ready",
			wantNoisy: true,
			wantMsg:   "injection",
		},
		{
			name:      "rationale with markdown fence rejected",
			title:     "Configure nginx",
			ctx:       "need reverse proxy",
			decision:  "use nginx",
			rationale: "```\nmalicious config\n```",
			wantNoisy: true,
			wantMsg:   "markdown fence",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason := checkDecisionNoise(tc.title, tc.ctx, tc.decision, tc.rationale)
			if tc.wantNoisy {
				if reason == "" {
					t.Errorf("expected rejection but got empty reason")
				}
				if tc.wantMsg != "" && !strings.Contains(reason, tc.wantMsg) {
					t.Errorf("reason %q does not contain %q", reason, tc.wantMsg)
				}
			} else if reason != "" {
				t.Errorf("expected pass but got reason: %q", reason)
			}
		})
	}
}

func TestCheckHandoffNoise(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		intent     string
		ctxSummary string
		wantNoisy  bool
		wantMsg    string
	}{
		{
			name:       "valid handoff passes",
			intent:     "Resume refactoring noise-filter PR",
			ctxSummary: "Wrote noise_filter.go, still need tests",
			wantNoisy:  false,
		},
		{
			name:      "intent over 5000 chars rejected",
			intent:    strings.Repeat("z", maxFieldLen+1),
			wantNoisy: true,
			wantMsg:   "5000-character",
		},
		{
			name:       "context_summary with script tag rejected",
			intent:     "Resume work",
			ctxSummary: "Working on <script src='evil'> feature",
			wantNoisy:  true,
			wantMsg:    "injection",
		},
		{
			name:       "context_summary with markdown fence rejected",
			intent:     "Resume work",
			ctxSummary: "Status:\n```\ndrop table users;\n```",
			wantNoisy:  true,
			wantMsg:    "markdown fence",
		},
		{
			name:      "empty context_summary passes",
			intent:    "Continue tomorrow",
			wantNoisy: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reason := checkHandoffNoise(tc.intent, tc.ctxSummary)
			if tc.wantNoisy {
				if reason == "" {
					t.Errorf("expected rejection reason but got empty string")
				}
				if tc.wantMsg != "" && !strings.Contains(reason, tc.wantMsg) {
					t.Errorf("reason %q does not contain %q", reason, tc.wantMsg)
				}
			} else if reason != "" {
				t.Errorf("expected pass but got reason: %q", reason)
			}
		})
	}
}

func TestSanitizeTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      []string
		wantTags   []string
		wantNoisy  bool
		wantSubstr string
	}{
		{
			name:      "clean tags pass through unchanged",
			input:     []string{"go", "backend", "db-migration"},
			wantTags:  []string{"go", "backend", "db-migration"},
			wantNoisy: false,
		},
		{
			name:      "tag with disallowed chars is stripped",
			input:     []string{"hello!", "world@", "ok-tag"},
			wantTags:  []string{"hello", "world", "ok-tag"},
			wantNoisy: false,
		},
		{
			name:      "tag exceeding maxTagLen is dropped",
			input:     []string{strings.Repeat("a", maxTagLen+1), "short"},
			wantTags:  []string{"short"},
			wantNoisy: false,
		},
		{
			name:       "more than 20 tags rejected",
			input:      make([]string, maxTagCount+1),
			wantNoisy:  true,
			wantSubstr: "20-entry",
		},
		{
			name:      "tag that becomes empty after strip is dropped",
			input:     []string{"!!!", "valid"},
			wantTags:  []string{"valid"},
			wantNoisy: false,
		},
		{
			name:      "exactly 20 tags accepted",
			input:     makeNTags(maxTagCount),
			wantTags:  makeNTags(maxTagCount),
			wantNoisy: false,
		},
		{
			name:      "allowed special chars preserved",
			input:     []string{"feat/auth", "fix_bug", "v1.2.3"},
			wantTags:  []string{"feat/auth", "fix_bug", "v1.2.3"},
			wantNoisy: false,
		},
		{
			name:      "empty input returns empty slice",
			input:     []string{},
			wantTags:  []string{},
			wantNoisy: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, reason := sanitizeTags(tc.input)
			if tc.wantNoisy {
				if reason == "" {
					t.Errorf("expected rejection reason but got empty string")
				}
				if tc.wantSubstr != "" && !strings.Contains(reason, tc.wantSubstr) {
					t.Errorf("reason %q does not contain %q", reason, tc.wantSubstr)
				}
				if got != nil {
					t.Errorf("expected nil tags on rejection, got %v", got)
				}
			} else {
				if reason != "" {
					t.Errorf("expected pass but got reason: %q", reason)
				}
				if len(got) != len(tc.wantTags) {
					t.Errorf("tag count mismatch: got %d want %d; tags=%v", len(got), len(tc.wantTags), got)
					return
				}
				for i, tag := range got {
					if tag != tc.wantTags[i] {
						t.Errorf("tag[%d]: got %q want %q", i, tag, tc.wantTags[i])
					}
				}
			}
		})
	}
}

// makeNTags returns a slice of n unique tag strings.
func makeNTags(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strings.Repeat("a", i+1) // "a", "aa", "aaa", ...
	}
	return out
}
