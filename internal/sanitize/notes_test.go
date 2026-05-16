package sanitize_test

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
)

func TestNotes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text passthrough", in: "task: Ship E3", want: "task: Ship E3"},
		{name: "null byte stripped", in: "task: foo\x00bar", want: "task: foobar"},
		{name: "CRLF stripped", in: "task: foo\r\nbar", want: "task: foobar"},
		{name: "ANSI clear-screen stripped", in: "task: foo\x1b[2Jbar", want: "task: foobar"},
		{name: "tab preserved", in: "task: foo\tbar", want: "task: foo\tbar"},
		{name: "500 rune cap", in: "task: " + strings.Repeat("a", 600), want: "task: " + strings.Repeat("a", 494)},
		{name: "empty string", in: "", want: ""},
		{name: "CR only stripped", in: "foo\rbar", want: "foobar"},
		{name: "DEL stripped", in: "foo\x7fbar", want: "foobar"},
		{name: "unicode passthrough", in: "task: emoji \U0001F600", want: "task: emoji \U0001F600"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitize.Notes(tc.in)
			if got != tc.want {
				t.Errorf("Notes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNotes_Idempotent(t *testing.T) {
	inputs := []string{
		"foo\x00\x1b[2Jbar",
		"hello\x1b[31m world",
		"\r\ntest\x7f",
		strings.Repeat("a", 600),
	}
	for _, in := range inputs {
		once := sanitize.Notes(in)
		twice := sanitize.Notes(once)
		if once != twice {
			t.Errorf("Notes not idempotent for %q: once=%q, twice=%q", in, once, twice)
		}
	}
}
