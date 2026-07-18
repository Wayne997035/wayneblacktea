// Package likeescape holds the single ILIKE/LIKE pattern-escaping helper that
// was duplicated verbatim across the Postgres skill/atom stores (as
// escapeLikePostgres) and the SQLite knowledge store (as escapeLike). Postgres
// ILIKE and SQLite LIKE share the same ESCAPE '\' syntax, so one
// implementation covers both backends.
package likeescape

import "strings"

// Escape backslash-escapes the LIKE/ILIKE special characters (\, %, _) in s
// so that user-supplied query text is treated as a literal substring rather
// than a wildcard pattern. Callers wrap the result in "%" + Escape(s) + "%"
// and pair it with an explicit ESCAPE '\' clause in the SQL.
func Escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
