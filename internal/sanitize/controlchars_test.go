package sanitize_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
)

func TestRejectControlChars(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		maxRune      int
		allowNewline bool
		want         string
		wantErr      error
	}{
		{name: "plain text passthrough", in: "task: Ship E3", maxRune: 100, want: "task: Ship E3"},
		{name: "tab preserved", in: "foo\tbar", maxRune: 100, want: "foo\tbar"},
		{name: "unicode and emoji preserved", in: "任務完成 \U0001F600", maxRune: 100, want: "任務完成 \U0001F600"},
		{name: "null byte rejected", in: "foo\x00bar", maxRune: 100, wantErr: sanitize.ErrControlChar},
		{name: "ANSI escape rejected", in: "foo\x1b[2Jbar", maxRune: 100, wantErr: sanitize.ErrControlChar},
		{name: "newline rejected when allowNewline=false", in: "foo\nbar", maxRune: 100, allowNewline: false, wantErr: sanitize.ErrControlChar},
		{name: "CR rejected when allowNewline=false", in: "foo\rbar", maxRune: 100, allowNewline: false, wantErr: sanitize.ErrControlChar},
		{
			name: "markdown with newlines preserved when allowNewline=true", in: "# Heading\n- item one\n- item two",
			maxRune: 100, allowNewline: true, want: "# Heading\n- item one\n- item two",
		},
		{
			name: "ANSI still rejected even when allowNewline=true", in: "line one\n\x1b[31mred\x1b[0m",
			maxRune: 100, allowNewline: true, wantErr: sanitize.ErrControlChar,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitize.RejectControlChars(tc.in, tc.maxRune, tc.allowNewline)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("RejectControlChars(%q) err = %v, want %v", tc.in, err, tc.wantErr)
				}
				if got != "" {
					t.Errorf("expected empty string on error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("RejectControlChars(%q): unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("RejectControlChars(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRejectControlChars_LengthLimit(t *testing.T) {
	in := strings.Repeat("a", 11)
	_, err := sanitize.RejectControlChars(in, 10, false)
	if err == nil {
		t.Fatal("expected length-exceeded error, got nil")
	}
	if errors.Is(err, sanitize.ErrControlChar) {
		t.Errorf("expected a length error, not ErrControlChar")
	}
}

func TestRejectControlChars_NeverModifiesInput(t *testing.T) {
	// Unlike Notes, RejectControlChars must never silently strip/truncate on
	// success — it returns s byte-for-byte identical to the input.
	in := "line one\nline two\twith tab and 中文 emoji \U0001F600"
	got, err := sanitize.RejectControlChars(in, 1000, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != in {
		t.Errorf("expected passthrough, got %q, want %q", got, in)
	}
}
