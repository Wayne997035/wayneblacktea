package sanitize

// Notes strips control characters (except horizontal tab) and ANSI escape
// sequences from text before it is stored in activity_log.notes. Capped at
// 500 runes per §5.4 of backend-security-design.md.
func Notes(s string) string {
	var b []rune
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		r := runes[i]
		if r == '\x1b' {
			i = skipAnsi(runes, i+1)
			continue
		}
		if r == '\t' || (r >= 0x20 && r != 0x7f) {
			b = append(b, r)
		}
		i++
	}
	if len(b) > 500 {
		b = b[:500]
	}
	return string(b)
}

// skipAnsi advances past an ANSI escape sequence; pos is the index after ESC.
func skipAnsi(runes []rune, pos int) int {
	if pos >= len(runes) {
		return pos
	}
	if runes[pos] != '[' {
		return pos + 1 // simple two-char ESC sequence, e.g. \x1bM
	}
	pos++
	for pos < len(runes) {
		c := runes[pos]
		pos++
		if isAnsiFinal(c) {
			break
		}
	}
	return pos
}

// isAnsiFinal reports whether r is the final byte of a CSI sequence.
// A CSI sequence is ESC '[' followed by zero or more parameter bytes
// (0x30-0x3F) and intermediate bytes (0x20-0x2F), terminated by exactly one
// final byte in 0x40-0x7E (ECMA-48 §5.4). The previous version only
// recognised letters plus '@'/'~', so punctuation final bytes like '^' or
// '_' were treated as non-final: skipAnsi kept scanning past them and
// consumed the next real letter it found (often the first character of the
// following text) as the final byte instead, silently eating that
// character. codex F20 repro: "ESC[0^KEEP" lost the leading "K" because '^'
// (0x5E) wasn't recognised as final and 'K' was consumed as if it were.
func isAnsiFinal(r rune) bool {
	return r >= 0x40 && r <= 0x7E
}
