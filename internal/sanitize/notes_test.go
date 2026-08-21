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

// TestSanitizeNotes_PunctuationCSIFinalByte is U17's exact codex repro (F20):
// a CSI sequence terminated by a punctuation final byte ('^', 0x5E) used to
// not be recognised as final, so skipAnsi kept scanning and consumed the
// next letter it found ('K' in "KEEP") as if it were the final byte —
// silently eating the leading character of the following text.
func TestSanitizeNotes_PunctuationCSIFinalByte(t *testing.T) {
	got := sanitize.Notes("ESC\x1b[0^KEEP")
	want := "ESCKEEP"
	if got != want {
		t.Errorf("Notes(%q) = %q, want %q (full \"KEEP\" must survive)", "ESC\x1b[0^KEEP", got, want)
	}
}

// TestSanitizeNotes_CSIFinalByteRange is a table test over the full
// ECMA-48 §5.4 final-byte range (0x40-0x7E) — every byte in that range must
// terminate the CSI sequence and let the following text through untouched,
// not just the letters isAnsiFinal recognised before the U17 fix.
func TestSanitizeNotes_CSIFinalByteRange(t *testing.T) {
	for b := rune(0x40); b <= 0x7E; b++ {
		in := "pre\x1b[3" + string(b) + "post"
		want := "prepost"
		got := sanitize.Notes(in)
		if got != want {
			t.Errorf("final byte %q (0x%X): Notes(%q) = %q, want %q", string(b), b, in, got, want)
		}
	}
}

// TestSanitizeNotes_NonFinalByteContinuesScanning is the inverse of the CSI
// final-byte range test: a byte outside 0x40-0x7E (e.g. a digit, which is a
// CSI parameter byte) must NOT terminate the sequence — the scan continues
// until a real final byte is found, so the escape sequence's intended
// content ('9' here) is consumed, not leaked into the output.
func TestSanitizeNotes_NonFinalByteContinuesScanning(t *testing.T) {
	got := sanitize.Notes("pre\x1b[39mpost")
	want := "prepost"
	if got != want {
		t.Errorf("Notes(%q) = %q, want %q", "pre\x1b[39mpost", got, want)
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
