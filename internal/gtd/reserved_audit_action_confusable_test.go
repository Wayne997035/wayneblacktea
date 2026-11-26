package gtd_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestIsReservedAuditAction_ASCIIConfusables is the [F1124-02] guard: common
// ASCII look-alikes (I/1/| for l, 0 for o) must be treated as the reserved
// name's letter, while near-misses outside the fold table stay allowed. Pure
// unit test, no DB.
func TestIsReservedAuditAction_ASCIIConfusables(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   bool
	}{
		{"capital_I_for_l", "task_deIeted", true},
		{"capital_I_for_l_negative", "task_updated", false},
		{"digit_1_for_l", "project_de1eted", true},
		{"digit_1_for_l_negative", "project_de2eted", false},
		{"zero_for_o", "project_rest0red", true},
		{"zero_for_o_negative", "project_restxred", false},
		{"case_regression", "PROJECT_DELETED", true},
		{"case_regression_negative", "project_deleter", false},
		{"pipe_for_l", "task_de|eted", true},
		{"pipe_for_l_negative", "task_de-eted", false},
		{"longer_not_matched", "task_deIeted_x", false},
		{"exact_name_still_true", "task_deleted", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gtd.IsReservedAuditAction(tc.action); got != tc.want {
				t.Errorf("IsReservedAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}

// TestIsReservedAuditAction_SpacingCompat guards [F1125-01] and [F1125-02].
// [F1125-01]: compatibility characters such as U+2017 decompose under NFKD
// into U+0020 + a combining mark, and the directly-sent expanded form
// "space + combining mark" renders as a single visible glyph; both must be
// treated as one placeholder character in a reserved name.
// [F1125-02]: U+0385 expands to space + two combining marks, so the
// placeholder rule must not replace the first combining mark too (the
// "task\u0385deleted" row fails without the not-Mn/Me condition).
func TestIsReservedAuditAction_SpacingCompat(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   bool
	}{
		{"u2017_double_low_line", "task\u2017deleted", true},
		{"space_plus_u0332", "task \u0332deleted", true},
		{"space_plus_u0333", "task \u0333deleted", true},
		{"space_plus_u0331", "task \u0331deleted", true},
		{"project_space_u0305", "project \u0305deleted", true},
		{"project_space_u0332_restored", "project \u0332restored", true},
		{"u0385_space_two_marks", "task\u0385deleted", true},
		// [F1125-06] NFKD of 240 x U+0344 plus inserted U+034F pushes the
		// placeholder canon past sanitize.Notes' 500-rune cap.
		{"u0344_prefix_past_cap", strings.Repeat("\u0344", 240) + "task\u2017deleted", true},
		{"plain_space_negative", "task deleted", false},
		{"nbsp_negative", "task\u00a0deleted", false},
		{"u2017_updated_negative", "task\u2017updated", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gtd.IsReservedAuditAction(tc.action); got != tc.want {
				t.Errorf("IsReservedAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}

// TestIsReservedAuditAction_CombiningMarkEnumeration guards [F1125-01]
// exhaustively: every visible Mn/Me code point after a space, and every code
// point whose NFKD starts with U+0020 and is longer than one rune, must read
// as a single placeholder inside each reserved name. Invisible marks are
// covered by TestIsReservedAuditAction_InvisibleRuns.
func TestIsReservedAuditAction_CombiningMarkEnumeration(t *testing.T) {
	var missed []string
	templates := []string{"task%cdeleted", "project%cdeleted", "project%crestored"}
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if unicode.In(r, unicode.Mn, unicode.Me) && !invisibleMark(r) && !gtd.IsReservedAuditAction("task "+string(r)+"deleted") {
			missed = append(missed, fmt.Sprintf("mark U+%04X", r))
		}
		if !spacingLedNFKD(r) {
			continue
		}
		for _, tpl := range templates {
			if a := fmt.Sprintf(tpl, r); !gtd.IsReservedAuditAction(a) {
				missed = append(missed, fmt.Sprintf("spacing U+%04X in %q", r, tpl))
			}
		}
	}
	if len(missed) > 0 {
		t.Errorf("%d code points not recognised as reserved: %s", len(missed), strings.Join(missed, "; "))
	}
}

// spacingLedNFKD reports whether r's NFKD form starts with U+0020 and is
// longer than one rune.
func spacingLedNFKD(r rune) bool {
	d := norm.NFKD.String(string(r))
	return strings.HasPrefix(d, " ") && len([]rune(d)) > 1
}

// invisibleMark reports whether r is a combining mark that renders nothing:
// a Variation_Selector or Other_Default_Ignorable_Code_Point Mn/Me.
func invisibleMark(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me) &&
		unicode.In(r, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point)
}

// invisibleRuns lists "dropped character + only invisible marks" runs: every
// Zs / Cf / Other_Default_Ignorable non-mark followed by U+FE0F, and U+200B /
// U+00A0 / U+0020 followed by each invisible mark (also returned).
func invisibleRuns() (runs []string, marks []rune) {
	for r := rune(0); r <= 0x10FFFF; r++ {
		switch {
		case r >= 0xD800 && r <= 0xDFFF:
		case invisibleMark(r):
			marks = append(marks, r)
			for _, base := range []string{"\u200b", "\u00a0", " "} {
				runs = append(runs, base+string(r))
			}
		case unicode.In(r, unicode.Mn, unicode.Me):
		case unicode.In(r, unicode.Zs, unicode.Cf, unicode.Other_Default_Ignorable_Code_Point):
			runs = append(runs, string(r)+"\ufe0f")
		}
	}
	return runs, marks
}

// TestIsReservedAuditAction_InvisibleRuns guards [F1125-05]: a run that
// renders nothing must not change the verdict wherever it is inserted into
// the SE-03 / SE-07 payloads (start, before "deleted", end), and a space
// followed only by invisible marks still reads as a plain space.
func TestIsReservedAuditAction_InvisibleRuns(t *testing.T) {
	runs, marks := invisibleRuns()
	var missed []string
	for _, p := range []string{"task\u2017deleted", "task \u0333deleted"} {
		for _, run := range runs {
			for _, a := range []string{run + p, strings.Replace(p, "deleted", run+"deleted", 1), p + run} {
				if !gtd.IsReservedAuditAction(a) {
					missed = append(missed, fmt.Sprintf("%+q", a))
				}
			}
		}
	}
	if len(missed) > 0 {
		t.Errorf("%d invisible-run insertions not recognised as reserved, first: %s",
			len(missed), strings.Join(missed[:min(len(missed), 10)], "; "))
	}
	for _, m := range marks {
		if a := "task " + string(m) + "deleted"; gtd.IsReservedAuditAction(a) {
			t.Errorf("IsReservedAuditAction(%+q) = true, want false (renders as a plain space)", a)
		}
	}
}

// TestIsReservedAuditAction_VisibleMarkInInvisibleRun guards [F1125-05]: a
// visible mark anywhere in a string of invisible marks after a dropped base
// (first, last, between, or in the full-length string) still yields one
// placeholder.
func TestIsReservedAuditAction_VisibleMarkInInvisibleRun(t *testing.T) {
	_, marks := invisibleRuns()
	// [F1125-05] a visible mark anywhere in the invisible-mark string turns the
	// dropped base + marks into one visible glyph (underlined blank), so it
	// must read as one placeholder wherever the visible mark sits.
	const visible = "\u0332"
	all := string(marks)
	var chain []string
	for _, base := range []string{" ", "\u200b"} {
		add := func(mid string) { chain = append(chain, "task"+base+mid+"deleted") }
		// (c) invisible first, visible last
		add("\ufe0f" + visible)
		for _, m := range marks {
			add(string(m) + visible)
		}
		// (d) visible first, invisible last
		add(visible + "\ufe0f")
		for _, m := range marks {
			add(visible + string(m))
		}
		// (e) visible at any position among repeated invisible marks
		for k := 0; k <= 4; k++ {
			for j := 0; j <= 4; j++ {
				add(strings.Repeat("\ufe0f", k) + visible + strings.Repeat("\ufe0f", j))
			}
		}
		// (e) long strings of every invisible mark, visible at either end
		add(all + visible)
		add(visible + all)
	}
	var chainMissed []string
	for _, a := range chain {
		if !gtd.IsReservedAuditAction(a) {
			chainMissed = append(chainMissed, fmt.Sprintf("%+q", a))
		}
	}
	if len(chainMissed) > 0 {
		t.Errorf("%d base + invisible/visible mark strings not recognised as reserved, first: %s",
			len(chainMissed), strings.Join(chainMissed[:min(len(chainMissed), 10)], "; "))
	}
}

// TestIsReservedAuditAction_MultiRuneD guards [F1125-04]: a "d" may be drawn
// as "c" + "l" (or look-alikes of each), and the fold must not be so wide
// that a non-d position becomes reserved.
func TestIsReservedAuditAction_MultiRuneD(t *testing.T) {
	cases := []struct {
		action string
		want   bool
	}{
		{"task_cleleted", true},
		{"project_cleleted", true},
		{"task_\u0441leleted", true},
		{"task_c\u04cfeleted", true},
		{"project_restore\u0441l", true},
		{"project_\u0441\u04cfeleted", true},
		{"task_clean", false},
		{"project_closed", false},
	}
	for _, tc := range cases {
		if got := gtd.IsReservedAuditAction(tc.action); got != tc.want {
			t.Errorf("IsReservedAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
		}
	}
	// Over-folding enumeration (R3-01): replace each position with a
	// two-rune spelling; only a "d" position may become reserved.
	for _, name := range []string{"task_deleted", "project_deleted", "project_restored"} {
		for i := 0; i < len(name); i++ {
			for _, sp := range []string{"cl", "ci", "c1", "c|"} {
				action := name[:i] + sp + name[i+1:]
				want := name[i] == 'd'
				if got := gtd.IsReservedAuditAction(action); got != want {
					t.Errorf("name %q pos %d spelling %q: IsReservedAuditAction(%q) = %v, want %v", name, i, sp, action, got, want)
				}
			}
		}
	}
}

// TestIsReservedAuditAction_NoFalsePositives guards [F1125-03] and
// [F1125-04] from the allow side. [F1125-03]: canonicalAuditAction and the
// placeholder pass share droppedByCanon; the row it protects is the existing
// U+2800 case in TestIsReservedAuditAction_Variants
// (reserved_audit_action_variants_test.go, unchanged). [F1125-04]: ordinary
// action names must never read as reserved.
func TestIsReservedAuditAction_NoFalsePositives(t *testing.T) {
	names := []string{
		"task:begin", "task:completed", "task:updated", "task:added", "decision:logged",
		"plan:confirmed", "session:handoff", "worksession:started", "worksession:finished",
		"worksession:checkpointed", "dashboard:reconciled", "atom_consolidation",
		"closeout_session_check", "task_completed", "task_closed", "task_cleared",
		"task_cleaned", "task_cloned", "task_declined", "task_delegated", "task_deferred",
		"task_reclaimed", "task_selected", "task_relabeled", "task_cancelled",
		"task_canceled", "task_clipped", "project_created", "project_closed",
		"project_cloned", "project_declined", "project_released", "project_restarted",
		"project_restore", "project_reclaimed", "project_archived", "project_cleared",
		"project_selected", "project_relocated", "project_restocked", "project_rescored",
		"project_restyled", "task deleted", "task-deleted", "Task Deleted", "taskdeleted",
		"task_delete", "task_deletes", "tasks_deleted",
	}
	for _, a := range names {
		if gtd.IsReservedAuditAction(a) {
			t.Errorf("IsReservedAuditAction(%q) = true, want false", a)
		}
	}
}

// zeroWidthBases enumerates the base-character set B for [F1125-07]: every
// Zs / Cf / Other_Default_Ignorable_Code_Point code point that is not a
// Mn/Me mark (surrogates skipped).
func zeroWidthBases() []rune {
	var bs []rune
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if unicode.In(r, unicode.Mn, unicode.Me) {
			continue
		}
		if unicode.In(r, unicode.Zs, unicode.Cf, unicode.Other_Default_Ignorable_Code_Point) {
			bs = append(bs, r)
		}
	}
	return bs
}

// visibleMarks enumerates V for [F1125-07]: every Mn/Me mark that renders.
func visibleMarks() []rune {
	var vs []rune
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if unicode.In(r, unicode.Mn, unicode.Me) && !invisibleMark(r) {
			vs = append(vs, r)
		}
	}
	return vs
}

// bidiControls are the 12 code points IsReservedAuditAction rejects outright.
var bidiControls = []rune{0x061C, 0x200E, 0x200F, 0x202A, 0x202B, 0x202C, 0x202D, 0x202E, 0x2066, 0x2067, 0x2068, 0x2069}

func isBidiControlRune(r rune) bool {
	for _, b := range bidiControls {
		if r == b {
			return true
		}
	}
	return false
}

// withoutBidi returns rs minus the 12 bidi control code points.
func withoutBidi(rs []rune) []rune {
	var out []rune
	for _, r := range rs {
		if !isBidiControlRune(r) {
			out = append(out, r)
		}
	}
	return out
}

func containsRune(rs []rune, want rune) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}

// pinEnumerations fails the test when B or V is not the set the
// [F1125-07] groups assume, so a mis-written predicate cannot run empty.
func pinEnumerations(t *testing.T, bs, vs []rune) {
	t.Helper()
	if len(bs) == 0 || len(vs) == 0 {
		t.Fatalf("empty enumeration: len(B)=%d len(V)=%d", len(bs), len(vs))
	}
	wantB := append([]rune{0x200B, 0x2060, 0xFEFF, 0x00AD, 0x200A, 0x3000}, bidiControls...)
	for _, r := range wantB {
		if !containsRune(bs, r) {
			t.Fatalf("B is missing U+%04X", r)
		}
	}
	for _, r := range []rune{0x0301, 0x0332, 0x0333} {
		if !containsRune(vs, r) {
			t.Fatalf("V is missing U+%04X", r)
		}
	}
	for _, r := range []rune{0xFE0F, 0x034F} {
		if containsRune(vs, r) {
			t.Fatalf("V must not contain U+%04X", r)
		}
	}
}

// expandRunes builds one input per rune by handing it to build.
func expandRunes(rs []rune, build func(r rune) string) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, build(r))
	}
	return out
}

// checkReservedVerdict asserts IsReservedAuditAction(input) == want for every
// input; a group reports once with the miss count and the first 10 inputs.
func checkReservedVerdict(t *testing.T, label string, want bool, inputs []string) {
	t.Helper()
	var wrong []string
	for _, in := range inputs {
		if gtd.IsReservedAuditAction(in) != want {
			wrong = append(wrong, fmt.Sprintf("%+q", in))
		}
	}
	if len(wrong) > 0 {
		t.Errorf("%s: %d of %d inputs have verdict != %v; first: %s",
			label, len(wrong), len(inputs), want, strings.Join(wrong[:min(len(wrong), 10)], "; "))
	}
}

// sandwich builds pre + r + post for every rune in rs.
func sandwich(rs []rune, pre, post string) []string {
	return expandRunes(rs, func(r rune) string { return pre + string(r) + post })
}

// zeroWidthBaseGroups returns the labelled "want true" input groups (a)-(d),
// (g) and (h) of TestIsReservedAuditAction_ZeroWidthBaseRuns.
func zeroWidthBaseGroups(bs, plainBs, vs []rune) map[string][]string {
	return map[string][]string{
		"(a) task+U+2017+b+U+0333+deleted":       sandwich(bs, "task\u2017", "\u0333deleted"),
		"(b) task+space+U+0332+b+U+0332+deleted": sandwich(bs, "task \u0332", "\u0332deleted"),
		"(c) task+U+2017+U+200B+v+deleted":       sandwich(vs, "task\u2017\u200b", "deleted"),
		"(d) task+U+2017+de+b+U+0301+leted":      sandwich(bs, "task\u2017de", "\u0301leted"),
		"(g1) three placeholders":                sandwich(plainBs, "task \u0332", "\u0332\u200b\u0332deleted"),
		"(g2) two places":                        sandwich(plainBs, "task\u2017", "\u0333de\u200b\u0301leted"),
		"(g3) placeholder at head":               sandwich(plainBs, "\u200b\u0332task\u2017", "\u0333deleted"),
		"(g4) placeholder at tail":               sandwich(plainBs, "task\u2017", "\u0333deleted\u200b\u0332"),
		"(g5) project_restored":                  sandwich(plainBs, "project\u2017", "\u0333restored"),
		"(g6) project_deleted":                   sandwich(plainBs, "project\u2017", "\u0333deleted"),
		"(h) placeholder as c in cl":             {"task_\u200b\u0332leleted", "project_\u200b\u0332leleted"},
	}
}

// TestIsReservedAuditAction_ZeroWidthBaseRuns guards [F1125-07]: a
// placeholder (U+FFFD from spacingCompatToPlaceholder) may match one name
// character or none, in any number and at any position, so a zero-width or
// near-zero-width base + visible mark cannot hide a reserved name. The
// reverse direction (no combining mark, or one slot holding two marks) must
// stay allowed.
func TestIsReservedAuditAction_ZeroWidthBaseRuns(t *testing.T) {
	bs, vs := zeroWidthBases(), visibleMarks()
	pinEnumerations(t, bs, vs)
	plainBs := withoutBidi(bs)

	for label, inputs := range zeroWidthBaseGroups(bs, plainBs, vs) {
		checkReservedVerdict(t, label, true, inputs)
	}

	// (e) no combining mark: nothing draws an underscore, so not reserved;
	// the 12 bidi controls are rejected by the bidi rule instead.
	checkReservedVerdict(t, "(e) task+b+deleted, no mark", false,
		sandwich(plainBs, "task", "deleted"))
	checkReservedVerdict(t, "(e) bidi controls stay reserved", true,
		sandwich(bidiControls, "task", "deleted"))

	// (i) one slot with two marks must not become two placeholders.
	checkReservedVerdict(t, "(i) two marks, one slot", false,
		[]string{"task\u0385eleted", "task \u0332\u0333eleted"})
}

// clGapGroupA builds the (a) inputs: c + b + U+0333 + l at the five d
// positions, b over every base. U+2017 is deliberate: the plain canon drops it
// whole, so only the spacing canon can match; a plain "_" would let the plain
// canon answer true and hide the gap.
func clGapGroupA(bs []rune) map[string][]string {
	return map[string][]string{
		"(a1) task_cleleted":     sandwich(bs, "task\u2017c", "\u0333leleted"),
		"(a2) task_deletecl":     sandwich(bs, "task\u2017deletec", "\u0333l"),
		"(a3) project_cleleted":  sandwich(bs, "project\u2017c", "\u0333leleted"),
		"(a4) project_deletecl":  sandwich(bs, "project\u2017deletec", "\u0333l"),
		"(a5) project_restorecl": sandwich(bs, "project\u2017restorec", "\u0333l"),
	}
}

// TestIsReservedAuditAction_ClPlaceholderGap guards [F1126-01]: a placeholder
// may sit between the "c" and the "l" of a "cl" d, any number of them, but a
// solid rune may not.
func TestIsReservedAuditAction_ClPlaceholderGap(t *testing.T) {
	bs := withoutBidi(zeroWidthBases())
	if len(bs) == 0 {
		t.Fatal("empty base enumeration")
	}
	for label, inputs := range clGapGroupA(bs) {
		checkReservedVerdict(t, label, true, inputs)
	}

	// (b) three placeholders between c and l.
	checkReservedVerdict(t, "(b) three placeholders between c and l", true, []string{
		"task\u2017c\u200b\u0333\u00a0\u0301\u2060\u0332leleted",
	})

	// (c) a placeholder as the l of cl (R3-S1): narrowing the l side must fail.
	checkReservedVerdict(t, "(c) placeholder as l of cl", true, []string{
		"task_c\u200b\u0332eleted",
		"project_c\u200b\u0332eleted",
		"project_restorec\u200b\u0332",
	})

	// (d) a solid rune between c and l must not be skipped.
	var solid []string
	for _, x := range []string{"e", "x", "z", "o"} {
		solid = append(solid, "task\u2017c"+x+"leleted")
	}
	checkReservedVerdict(t, "(d) solid rune between c and l", false, solid)
}

// allocBytesPerRun returns the mean TotalAlloc bytes of one call to f.
func allocBytesPerRun(runs uint64, f func()) uint64 {
	f()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / runs
}

// TestIsReservedAuditAction_WorstCaseCost guards [F1126-02]: the matcher's
// allocation count and bytes stay small for the worst inputs sanitize.Notes
// lets through (500 runes that NFKD expands). It does not call t.Parallel so
// no other test measures MemStats at the same time.
func TestIsReservedAuditAction_WorstCaseCost(t *testing.T) {
	const maxAllocs = 200
	const maxBytes = 1 << 20

	// Input one: 500 x U+FDFA (7500-rune canon) must be cut by the pre-filter.
	one := strings.Repeat("\ufdfa", 500)
	if gtd.IsReservedAuditAction(one) {
		t.Errorf("input one: want false, got true")
	}
	if got := testing.AllocsPerRun(5, func() { gtd.IsReservedAuditAction(one) }); got > maxAllocs {
		t.Errorf("input one: %v allocs per call, want <= %d", got, maxAllocs)
	} else {
		t.Logf("input one: %v allocs per call", got)
	}
	got := allocBytesPerRun(5, func() { gtd.IsReservedAuditAction(one) })
	if got > maxBytes {
		t.Errorf("input one: %d bytes per call, want <= %d", got, maxBytes)
	}
	t.Logf("input one: %d bytes per call", got)

	// Input two: the pre-filter lets it through; tables must be contiguous.
	two := strings.Repeat("\u00a8", 499) + "x"
	if gtd.IsReservedAuditAction(two) {
		t.Errorf("input two: want false, got true")
	}
	if got := testing.AllocsPerRun(5, func() { gtd.IsReservedAuditAction(two) }); got > maxAllocs {
		t.Errorf("input two: %v allocs per call, want <= %d", got, maxAllocs)
	} else {
		t.Logf("input two: %v allocs per call", got)
	}

	// Input three: many placeholders must not make the pre-filter drop a
	// reserved name; only non-placeholder runes count.
	three := "task\u2017" + strings.Repeat("\u200b\u0333", 30) + "deleted"
	if !gtd.IsReservedAuditAction(three) {
		t.Errorf("input three: want true (reserved), got false")
	}
}
