package validator

import (
	"strings"
	"testing"
)

func TestCheckVagueness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		field       string
		text        string
		kind        string
		wantEmpty   bool   // true = expect no warnings
		wantContain string // non-empty = at least one warning must contain this substring
	}{
		// happy-path: descriptive text with file:line ref
		{
			name:      "descriptive with file ref",
			field:     "description",
			text:      "Fix OOM in internal/gtd/store.go:392 by adding context deadline",
			kind:      "general",
			wantEmpty: true,
		},
		// happy-path: chore kind skips all checks
		{
			name:      "chore kind skips checks",
			field:     "description",
			text:      "TBD",
			kind:      "chore",
			wantEmpty: true,
		},
		// happy-path: long enough text without file:line
		{
			name:      "long text no file ref is ok",
			field:     "description",
			text:      "Investigate the root cause of the nightly OOM restart in production environment",
			kind:      "general",
			wantEmpty: true,
		},
		// error: exact TBD marker
		{
			name:        "TBD marker",
			field:       "description",
			text:        "TBD",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "TBD",
		},
		// error: ??? marker
		{
			name:        "question marks marker",
			field:       "description",
			text:        "???",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "???",
		},
		// error: TODO marker (case-insensitive)
		{
			name:        "todo lowercase",
			field:       "intent",
			text:        "todo: fix this",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "TODO",
		},
		// error: see audit phrase
		{
			name:        "see audit phrase",
			field:       "rationale",
			text:        "see audit for details on why this approach was chosen",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "see audit",
		},
		// error: 見上面 phrase
		{
			name:        "chinese vague phrase",
			field:       "description",
			text:        "見上面",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "見上面",
		},
		// error: 等等 phrase
		{
			name:        "etc phrase",
			field:       "description",
			text:        "Fix bugs 等等",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "等等",
		},
		// error: too short without file:line
		{
			name:        "short no fileline",
			field:       "description",
			text:        "Fix the bug",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "too short",
		},
		// edge: short but has file:line reference — not flagged for shortness
		{
			name:      "short with fileline",
			field:     "description",
			text:      "Fix main.go:42",
			kind:      "general",
			wantEmpty: true, // file:line present, so shortness doesn't trigger
		},
		// edge: WIP marker embedded in longer text
		{
			name:        "WIP embedded",
			field:       "description",
			text:        "This is a long description that mentions WIP progress and other things",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "WIP",
		},
		// edge: path-style file:line (internal/foo/bar.go:10)
		{
			name:      "path style fileline",
			field:     "description",
			text:      "Fix crash in internal/gtd/store.go:10",
			kind:      "general",
			wantEmpty: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckVagueness(tc.field, tc.text, tc.kind)
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Errorf("CheckVagueness(%q, %q, %q) = %v; want empty", tc.field, tc.text, tc.kind, got)
				}
				return
			}
			if len(got) == 0 {
				t.Errorf("CheckVagueness(%q, %q, %q) = empty; want non-empty warnings", tc.field, tc.text, tc.kind)
				return
			}
			if tc.wantContain != "" {
				found := false
				for _, w := range got {
					if strings.Contains(w, tc.wantContain) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("CheckVagueness(%q, %q, %q) = %v; want a warning containing %q", tc.field, tc.text, tc.kind, got, tc.wantContain)
				}
			}
		})
	}
}

func TestHasFileLineRef(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text string
		want bool
	}{
		{"internal/gtd/store.go:392", true},
		{"main.go:1", true},
		{"foo/bar:42", true},
		{"no colon here", false},
		{"word:notanumber", false},
		{":42", false},
		{"a.b:0", false}, // no .go suffix and no slash — not a file:line
		{"a.go:0", true},
		{"", false},
	}
	for _, c := range cases {
		got := hasFileLineRef(c.text)
		if got != c.want {
			t.Errorf("hasFileLineRef(%q) = %v; want %v", c.text, got, c.want)
		}
	}
}
