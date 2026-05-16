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
