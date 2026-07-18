package sanitize

import (
	"errors"
	"fmt"
)

// ErrControlChar is returned by RejectControlChars when s contains a NUL
// byte or a forbidden ASCII control character.
var ErrControlChar = errors.New("text contains forbidden control character")

// RejectControlChars validates that s contains no NUL byte and no ASCII
// control character below 0x20 other than tab (and, when allowNewline is
// true, LF/CR), and does not exceed maxRunes. Unlike Notes (which silently
// strips and truncates), RejectControlChars never modifies s — on any
// violation it returns an error so the caller can reject the whole write.
//
// allowNewline should be true for long-form / markdown fields (article
// content, approach_md) where line breaks are semantic content, and false
// for single-line classifier-style fields (title, condition) where an
// embedded newline is either noise or a CLI-listing-spoofing attempt per
// backend-security-design.md §5.4.
func RejectControlChars(s string, maxRunes int, allowNewline bool) (string, error) {
	n := 0
	for _, r := range s {
		if r == '\x00' {
			return "", ErrControlChar
		}
		if r != '\t' && r < 0x20 && (!allowNewline || (r != '\n' && r != '\r')) {
			return "", ErrControlChar
		}
		n++
		if n > maxRunes {
			return "", fmt.Errorf("text exceeds %d characters", maxRunes)
		}
	}
	return s, nil
}
