package gtd_test

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestIsReservedAuditAction_Variants is F191-14's unit-level proof: 40
// disguised spellings of the three reserved audit action names must still
// be rejected, and 14 strings that merely resemble a reserved name must not
// be. Pure unit test, no DB — gtd.IsReservedAuditAction takes no store
// dependency.
//
// Every non-ASCII code point below is written as string(rune(0x...)) so the
// source file itself stays pure ASCII: a literal BOM or other invisible
// character in a Go source file is either a compile error (BOM mid-file) or
// invisible to reviewers. ASCII control characters (< 0x80) use plain \x
// escapes, matching internal/sanitize/notes_test.go's existing style — those
// are pure ASCII text in the source, not raw bytes, so they carry none of
// that risk.
func TestIsReservedAuditAction_Variants(t *testing.T) {
	const (
		projectDeleted  = "project_deleted"
		taskDeleted     = "task_deleted"
		projectRestored = "project_restored"
	)

	trueCases := []struct {
		name   string
		action string
	}{
		// the three reserved names plus case/whitespace variants
		{"exact project_deleted", projectDeleted},
		{"exact task_deleted", taskDeleted},
		{"exact project_restored", projectRestored},
		{"mixed case Project_Deleted", "Project_Deleted"},
		{"leading and trailing space", " " + projectDeleted + " "},

		// invisible characters, one position each — the whitelist in
		// canonicalAuditAction drops anything that isn't
		// Letter/Number/Punct/Symbol outright (covers most of these), plus
		// an explicit Other_Default_Ignorable_Code_Point + U+2800 exclusion
		// for the handful that are Letter/Symbol category but still render
		// invisibly.
		{"U+200B suffix (zero width space)", projectDeleted + string(rune(0x200B))},
		{"U+200D prefix (zero width joiner)", string(rune(0x200D)) + projectDeleted},
		{"U+FEFF prefix (BOM / zero width no-break space)", string(rune(0xFEFF)) + projectDeleted},
		{"U+202E prefix (right-to-left override)", string(rune(0x202E)) + projectDeleted},
		{"U+3164 suffix (Hangul filler, Lo + ODI)", projectDeleted + string(rune(0x3164))},
		{"U+115F prefix (Hangul choseong filler, Lo + ODI)", string(rune(0x115F)) + projectDeleted},
		{"U+1160 prefix (Hangul jungseong filler, Lo + ODI)", string(rune(0x1160)) + projectDeleted},
		{"U+FFA0 middle (halfwidth Hangul filler, Lo + ODI)", "project_" + string(rune(0xFFA0)) + "deleted"},
		{"U+2800 suffix (braille blank, explicit exclusion — not Cf/Mn/ODI)", projectDeleted + string(rune(0x2800))},
		{"U+00AD middle (soft hyphen)", "project_" + string(rune(0xAD)) + "deleted"},
		{"U+FE0F suffix (variation selector-16)", projectDeleted + string(rune(0xFE0F))},
		{"U+200A suffix (hair space)", projectDeleted + string(rune(0x200A))},
		{"U+3000 prefix (ideographic space)", string(rune(0x3000)) + projectDeleted},
		{"U+2028 middle (line separator)", "project_" + string(rune(0x2028)) + "deleted"},
		{"U+E0001 suffix (language tag)", projectDeleted + string(rune(0xE0001))},
		{"U+180E suffix (Mongolian vowel separator)", projectDeleted + string(rune(0x180E))},
		{"U+061C prefix (Arabic letter mark)", string(rune(0x061C)) + projectDeleted},
		{"U+2061 middle (function application)", "project_" + string(rune(0x2061)) + "deleted"},

		// homoglyphs, combining marks, and NFKD compatibility forms
		{"Cyrillic е (U+0435) in place of Latin e", "proj" + string(rune(0x0435)) + "ct_deleted"},
		{"fullwidth low line (U+FF3F) in place of underscore", "project" + string(rune(0xFF3F)) + "deleted"},
		{"fullwidth p (U+FF50) leading", string(rune(0xFF50)) + "roject_deleted"},
		{"precomposed e-acute (U+00E9)", "proj" + string(rune(0x00E9)) + "ct_deleted"},
		{"combining acute accent (U+0301) after e", "proje" + string(rune(0x0301)) + "ct_deleted"},
		{"extra space inside project_ deleted", "project_ deleted"},

		// accented spelling — intentionally caught as a false positive, see
		// IsReservedAuditAction's doc comment and SEC-PR191-R2-01's P3 for
		// why this is accepted rather than carved out
		{"prôject_dèleted (intended false positive, P3)", "pr" + string(rune(0x00F4)) + "ject_d" + string(rune(0x00E8)) + "leted"},

		// ANSI escape sequences and control characters
		{"wrapped in SGR red/reset", "\x1b[31m" + projectDeleted + "\x1b[0m"},
		{"CSI erase-line prefix", "\x1b[2K" + projectDeleted},
		{"CR prefix", "\r" + projectDeleted},
		{"LF suffix", projectDeleted + "\n"},
		{"simple ESC M prefix", "\x1bM" + projectDeleted},
		{"NUL middle", "project_" + "\x00" + "deleted"},
		{"BEL middle", "project_" + "\x07" + "deleted"},
		{"DEL suffix", projectDeleted + "\x7f"},
		{"C1 NEL (U+0085) suffix", projectDeleted + string(rune(0x85))},
		{"C1 CSI (U+009B) prefix", string(rune(0x9B)) + projectDeleted},
	}

	falseCases := []struct {
		name   string
		action string
	}{
		{"unrelated Chinese phrase", "完成 PR 審查"},
		{"15 Han characters, same rune count as project_deleted — CJK is not a wildcard script", strings.Repeat("刪", 15)},
		{"longer variant with suffix", projectDeleted + "_extra"},
		{"hyphen instead of underscore", "project-deleted"},
		{"space instead of underscore", "project deleted"},
		{"unrelated real word, same length as task_deleted", "task_updated"},
		{"unrelated action name", "atom_consolidation"},
		{"unrelated action name 2", "closeout_session_check"},
		{"empty string", ""},
		{"English sentence", "Deleted the project"},
		{"Chinese sentence", "刪除專案 X"},
		{"emoji sentence", "deployed v1.2 \U0001F680"},
		{"trailing count suffix", "task_deleted_count=3"},
		{"Chinese sentence containing the literal reserved name as a substring", "修正 project_deleted 稽核"},
	}

	if len(trueCases) != 40 {
		t.Fatalf("trueCases has %d entries, want 40 (see SEC-PR191-R2-01 acceptance)", len(trueCases))
	}
	if len(falseCases) != 14 {
		t.Fatalf("falseCases has %d entries, want 14 (see SEC-PR191-R2-01 acceptance)", len(falseCases))
	}

	// [F191-14]
	for _, tc := range trueCases {
		t.Run("true/"+tc.name, func(t *testing.T) {
			if !gtd.IsReservedAuditAction(tc.action) {
				t.Errorf("IsReservedAuditAction(%q) = false, want true", tc.action)
			}
		})
	}
	for _, tc := range falseCases {
		t.Run("false/"+tc.name, func(t *testing.T) {
			if gtd.IsReservedAuditAction(tc.action) {
				t.Errorf("IsReservedAuditAction(%q) = true, want false", tc.action)
			}
		})
	}
}
