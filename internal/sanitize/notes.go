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
func isAnsiFinal(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '@' || r == '~'
}
