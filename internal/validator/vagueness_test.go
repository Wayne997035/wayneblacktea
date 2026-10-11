package validator

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestCheckVagueness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		field       string
		text        string
		kind        string
		wantEmpty   bool   // true = expect no warnings
		wantContain string // non-empty = at least one warning must contain this substring
	}{
		// happy-path: descriptive text with file:line ref
		{
			name:      "descriptive with file ref",
			field:     "description",
			text:      "Fix OOM in internal/gtd/store.go:392 by adding context deadline",
			kind:      "general",
			wantEmpty: true,
		},
		// happy-path: chore kind skips all checks
		{
			name:      "chore kind skips checks",
			field:     "description",
			text:      "TBD",
			kind:      "chore",
			wantEmpty: true,
		},
		// happy-path: long enough text without file:line
		{
			name:      "long text no file ref is ok",
			field:     "description",
			text:      "Investigate the root cause of the nightly OOM restart in production environment",
			kind:      "general",
			wantEmpty: true,
		},
		// error: exact TBD marker
		{
			name:        "TBD marker",
			field:       "description",
			text:        "TBD",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "TBD",
		},
		// error: ??? marker
		{
			name:        "question marks marker",
			field:       "description",
			text:        "???",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "???",
		},
		// error: TODO marker (case-insensitive)
		{
			name:        "todo lowercase",
			field:       "intent",
			text:        "todo: fix this",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "TODO",
		},
		// error: see audit phrase
		{
			name:        "see audit phrase",
			field:       "rationale",
			text:        "see audit for details on why this approach was chosen",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "see audit",
		},
		// error: 見上面 phrase
		{
			name:        "chinese vague phrase",
			field:       "description",
			text:        "見上面",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "見上面",
		},
		// error: 等等 phrase
		{
			name:        "etc phrase",
			field:       "description",
			text:        "Fix bugs 等等",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "等等",
		},
		// error: too short without file:line
		{
			name:        "short no fileline",
			field:       "description",
			text:        "Fix the bug",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "too short",
		},
		// edge: short but has file:line reference — not flagged for shortness
		{
			name:      "short with fileline",
			field:     "description",
			text:      "Fix main.go:42",
			kind:      "general",
			wantEmpty: true, // file:line present, so shortness doesn't trigger
		},
		// edge: WIP marker embedded in longer text
		{
			name:        "WIP embedded",
			field:       "description",
			text:        "This is a long description that mentions WIP progress and other things",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "WIP",
		},
		// edge: path-style file:line (internal/foo/bar.go:10)
		{
			name:      "path style fileline",
			field:     "description",
			text:      "Fix crash in internal/gtd/store.go:10",
			kind:      "general",
			wantEmpty: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckVagueness(tc.field, tc.text, tc.kind)
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Errorf("CheckVagueness(%q, %q, %q) = %v; want empty", tc.field, tc.text, tc.kind, got)
				}
				return
			}
			if len(got) == 0 {
				t.Errorf("CheckVagueness(%q, %q, %q) = empty; want non-empty warnings", tc.field, tc.text, tc.kind)
				return
			}
			if tc.wantContain != "" {
				found := false
				for _, w := range got {
					if strings.Contains(w, tc.wantContain) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("CheckVagueness(%q, %q, %q) = %v; want a warning containing %q", tc.field, tc.text, tc.kind, got, tc.wantContain)
				}
			}
		})
	}
}

// TestCheckVagueness_AutoCapturedMarker verifies that the literal placeholder
// strings the auto-capture path historically produced are now flagged as
// vague. Cases cover both the canonical upper-cased literal and the lower-
// cased phrase form (defence in depth against future case-flip regressions),
// plus a non-vague descriptive baseline to confirm the markers do not
// over-match.
func TestCheckVagueness_AutoCapturedMarker(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		field       string
		text        string
		kind        string
		wantContain string // substring expected in at least one warning
	}{
		{
			name:        "literal auto-captured from MCP — exact bug-day form",
			field:       "description",
			text:        "auto-captured from MCP: complete_task",
			kind:        "general",
			wantContain: "AUTO-CAPTURED FROM MCP",
		},
		{
			name:        "literal auto-captured from activity log — exact bug-day form",
			field:       "description",
			text:        "auto-captured from activity log",
			kind:        "general",
			wantContain: "AUTO-CAPTURED FROM ACTIVITY LOG",
		},
		{
			name:        "upper-cased AUTO-CAPTURED FROM MCP embedded in longer text",
			field:       "description",
			text:        "Long story short: AUTO-CAPTURED FROM MCP placeholder leaked into the description",
			kind:        "general",
			wantContain: "AUTO-CAPTURED FROM MCP",
		},
		{
			name:        "mixed-case Auto-Captured From Activity Log still triggers",
			field:       "description",
			text:        "Auto-Captured From Activity Log entry; needs real description",
			kind:        "general",
			wantContain: "AUTO-CAPTURED FROM ACTIVITY LOG",
		},
		{
			name:        "phrase form lower-cased embedded in sentence",
			field:       "description",
			text:        "Note that this is auto-captured from activity log and should be rewritten",
			kind:        "general",
			wantContain: "auto-captured from activity log",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckVagueness(tc.field, tc.text, tc.kind)
			if len(got) == 0 {
				t.Fatalf("CheckVagueness(%q, %q, %q) = empty; want at least one warning containing %q", tc.field, tc.text, tc.kind, tc.wantContain)
			}
			found := false
			for _, w := range got {
				if strings.Contains(w, tc.wantContain) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("CheckVagueness(%q, %q, %q) = %v; want a warning containing %q", tc.field, tc.text, tc.kind, got, tc.wantContain)
			}
		})
	}
}

// TestCheckVagueness_AutoCapturedMarker_ChoreKindSkipped verifies that the
// chore-kind exemption still applies to the new markers. A chore task with
// an auto-capture placeholder MUST NOT produce a warning, matching the
// pre-existing chore-skips-all-checks contract.
func TestCheckVagueness_AutoCapturedMarker_ChoreKindSkipped(t *testing.T) {
	t.Parallel()
	got := CheckVagueness("description", "auto-captured from MCP: complete_task", "chore")
	if len(got) != 0 {
		t.Errorf("CheckVagueness with kind=chore = %v; want empty (chore skips all checks)", got)
	}
}

// lueP is the prefix every 略 acceptance input carries. It holds a file:line
// reference, so the "too short" warning cannot fire and "called" depends on 略
// alone.
const lueP = "Fix internal/gtd/store.go:392: "

// lueWarning is the exact, byte-for-byte warning text for a flagged 略.
const lueWarning = `description: contains vague phrase "略"`

// lueCase is one 略 input and whether it must be flagged.
type lueCase struct {
	name       string
	text       string
	wantCalled bool
}

// lueCalled reports whether CheckVagueness (kind general) flags 略 in text.
func lueCalled(text string) bool {
	for _, w := range CheckVagueness("description", text, "general") {
		if strings.Contains(w, `vague phrase "略"`) {
			return true
		}
	}
	return false
}

// TestCheckVagueness_LueAllowlist is the [F1011-21] table: 略 is flagged only
// outside allowlisted compounds, and an omit-class compound exempts only when
// content follows on the same line. The ordinary-word rows are literal on
// purpose (not generated from lueCompounds), so deleting an entry fails here.
func TestCheckVagueness_LueAllowlist(t *testing.T) {
	t.Parallel()

	cases := []lueCase{
		// Compounds with content after them are exempt; the placeholder next to them is not.
		{"omit verb with object", lueP + "省略 X 則不變", false},
		{"placeholder 細節略", lueP + "細節略", true},
		{"ordinary 策略 mid-sentence", lueP + "部署策略調整", false},
		{"comma then placeholder", lueP + "部署,細節略", true},
		{"ordinary 忽略 compound", lueP + "忽略大小寫", false},
		{"comma splits 忽略", lueP + "忽,略大小寫", true},
		{"略過 with content", lueP + "略過測試", false},
		{"space splits 略過", lueP + "略 過測試", true},
		{"省略 then arrow content", lueP + "省略 project_id → 原值不變", false},
		{"clause-final 其餘省略", lueP + "其餘省略", true},
		{"簡略 modifies what follows", lueP + "簡略說明", false},
		{"縮略 modifies what follows", lueP + "縮略圖", false},
		{"clause-final 以下簡略", lueP + "以下簡略", true},
		{"clause-final 以下縮略", lueP + "以下縮略", true},

		// Clause-final omit-class words are elision markers.
		{"細節略過。 ends the clause", lueP + "細節略過。", true},
		{"細節略過測試。 content follows the compound", lueP + "細節略過測試。", false},
		{"newline ends the clause", lueP + "以下省略\n下一行", true},
		{"space does not end the clause", lueP + "以下省略 下一行", false},
		{"fail-safe 此參數可省略。", lueP + "此參數可省略。", true},
		{"此參數可省略 project_id。 content follows", lueP + "此參數可省略 project_id。", false},

		// Terminators, brackets, whitespace and line breaks right after an omit-class word.
		{"wrapped (省略)", lueP + "(省略)", true},
		{"wrapped （省略）", lueP + "（省略）", true},
		{"wrapped [省略]", lueP + "[省略]", true},
		{"spaces only after 省略", lueP + "省略   ", true},
		{"space then newline then text", lueP + "省略 \n下一行", true},
		{"CRLF then text", lueP + "省略\r\n下一行", true},
		{"space then arrow is content", lueP + "省略 → 原值", false},
		{"ideographic space then X is content", lueP + "省略\u3000X", false},
		{"space then tight arrow is content", lueP + "省略 →x", false},

		// Lead supplement: each whitespace / bracket element of contentFollows is pinned by its own row.
		{"省略 then TAB at end of text", lueP + "省略\t", true},
		{"省略 then U+00A0 at end of text", lueP + "省略\u00a0", true},
		{"省略 then U+3000 at end of text", lueP + "省略\u3000", true},
		{"wrapped 【省略】", lueP + "【省略】", true},
		{"wrapped ［省略］", lueP + "［省略］", true},

		// Ordinary words stay exempt at the end of a clause; omit-class words do not.
		{"影響可忽略。", lueP + "影響可忽略。", false},
		{"採用快取策略。", lueP + "採用快取策略。", false},

		// One literal row per ordinary word, alone at the end of the text.
		{"ordinary 概略", lueP + "概略", false},
		{"ordinary 戰略", lueP + "戰略", false},
		{"ordinary 領略", lueP + "領略", false},
		{"ordinary 侵略", lueP + "侵略", false},
		{"ordinary 攻略", lueP + "攻略", false},
		{"ordinary 約略", lueP + "約略", false},
		{"ordinary 粗略", lueP + "粗略", false},
		{"ordinary 大略", lueP + "大略", false},
		{"ordinary 忽略", lueP + "忽略", false},
		{"ordinary 策略", lueP + "策略", false},

		// Omission markers that are deliberately not allowlisted.
		{"marker 從略", lueP + "從略", true},
		{"marker 中略", lueP + "中略", true},
		{"marker 上略", lueP + "上略", true},
		{"marker 下略", lueP + "下略", true},
		{"marker 前略", lueP + "前略", true},
		{"marker 後略", lueP + "後略", true},
		{"marker 節略", lueP + "節略", true},
		{"marker 餘略", lueP + "餘略", true},

		// Words that contain 略 but are not on either allowlist. Any of these
		// going green means an allowlist was widened; the last six are the
		// Simplified forms of allowlisted Traditional words. [PR #200 R1-02]
		{"not allowlisted 略述", lueP + "略述", true},
		{"not allowlisted 略有改善", lueP + "略有改善", true},
		{"not allowlisted 略微", lueP + "略微", true},
		{"not allowlisted 略為", lueP + "略為", true},
		{"not allowlisted 略顯", lueP + "略顯", true},
		{"not allowlisted 略高", lueP + "略高", true},
		{"not allowlisted 略低", lueP + "略低", true},
		{"simplified 战略", lueP + "战略", true},
		{"simplified 领略", lueP + "领略", true},
		{"simplified 简略", lueP + "简略", true},
		{"simplified 约略", lueP + "约略", true},
		{"simplified 略过", lueP + "略过", true},
		{"simplified 缩略", lueP + "缩略", true},

		// A clause-final omit word wrapped in a closing mark is still called:
		// quotes do not exempt (decision ea7e6826). One row per lueClosers
		// mark, written out literally so removing a mark fails its row.
		// [PR #200 R1-01]
		{"wrapped 「」", lueP + "以下「省略」", true},
		{"wrapped backtick", lueP + "以下`省略`", true},
		{"wrapped ascii dq", lueP + "以下\"省略\"", true},
		{"wrapped bold", lueP + "**以下省略**", true},
		{"wrapped fullwidth dq", lueP + "以下＂省略＂", true},
		{"wrapped 〝〞", lueP + "以下〝省略〞", true},
		{"wrapped 《》", lueP + "以下《省略》", true},
		{"wrapped {}", lueP + "以下{省略}", true},
		{"wrapped <>", lueP + "以下<省略>", true},
		{"wrapped «»", lueP + "以下«省略»", true},
		{"wrapped then 。", lueP + "以下「省略」。", true},
		{"closer U+300D", lueP + "以下省略」", true},
		{"closer U+300F", lueP + "以下省略』", true},
		{"closer U+201D", lueP + "以下省略”", true},
		{"closer U+2019", lueP + "以下省略’", true},
		{"closer U+FF02", lueP + "以下省略＂", true},
		{"closer U+301E", lueP + "以下省略〞", true},
		{"closer U+00BB", lueP + "以下省略»", true},
		{"closer U+300B", lueP + "以下省略》", true},
		{"closer U+3009", lueP + "以下省略〉", true},
		{"closer U+3015", lueP + "以下省略〕", true},
		{"closer U+007D", lueP + "以下省略}", true},
		{"closer U+003E", lueP + "以下省略>", true},
		{"closer U+FF5D", lueP + "以下省略｝", true},
		{"closer U+0022", lueP + "以下省略\"", true},
		{"closer U+0027", lueP + "以下省略'", true},
		{"closer U+0060", lueP + "以下省略`", true},
		{"closer U+002A", lueP + "以下省略*", true},
		{"closer U+005F", lueP + "以下省略_", true},
		{"closer U+007E", lueP + "以下省略~", true},
		{"closer U+FF0A", lueP + "以下省略＊", true},
		{"closer U+FF40", lueP + "以下省略｀", true},

		// The same marks after whitespace open the object of an omit verb and
		// count as content, for every kind of horizontal whitespace.
		{"object opens with 1", lueP + "略過 *.pb.go 檔案", false},
		{"object opens with 2", lueP + "略過 `.env` 檔案", false},
		{"object opens with 3", lueP + "略過 \".git\" 目錄", false},
		{"object opens with 4", lueP + "略過 '.' 與 '..'", false},
		{"object opens with 5", lueP + "省略 *.md", false},
		{"object opens with 6", lueP + "略過 \"?\" 參數", false},
		{"object opens with 7", lueP + "省略 `,` 分隔", false},
		{"ws before opener U+0020", lueP + "略過 *.pb.go 檔案", false},
		{"ws before opener U+0009", lueP + "略過\t*.pb.go 檔案", false},
		{"ws before opener U+00A0", lueP + "略過\u00a0*.pb.go 檔案", false},
		{"ws before opener U+3000", lueP + "略過\u3000*.pb.go 檔案", false},
		{"content after closer 「」", lueP + "其中「省略」這一步之後補", false},
		{"content after closer 《》", lueP + "其中《省略》一節之後補", false},
		{"trailing space after closing mark", lueP + "以下「省略」 ", true},
		{"object after closing mark and space", lueP + "**省略** project_id", false},

		// The trailing 略 causes the hit, not the 省略 before it.
		{"省略 A,其餘略", lueP + "省略 A,其餘略", true},
		{"省略 A,其餘說明", lueP + "省略 A,其餘說明", false},

		// A strip-then-search implementation gets both of these wrong.
		{"placeholder before 略過", lueP + "略省略過測試", true},
		{"省略 and 略過 share a 略", lueP + "省略略過測試", false},

		// Bare placeholders.
		{"placeholder 其餘略", lueP + "其餘略", true},
		{"placeholder (略)", lueP + "(略)", true},
		{"placeholder 以下略", lueP + "以下略", true},
		{"bare 略", lueP + "略", true},
		{"no 略 at all", lueP + "細節說明如下", false},

		// Quotes and backticks neither exempt a 略 nor un-exempt a compound.
		{"double-quoted 略", lueP + "\"略\"", true},
		{"backtick-wrapped 略", lueP + "`略`", true},
		{"corner-bracket 略", lueP + "「略」", true},
		{"backtick-wrapped 策略", lueP + "`策略`", false},
	}

	// Terminators directly after 省略 end the clause: full-width and ASCII.
	for _, term := range []string{"，", "。", "、", "；", "：", "！", "？", "…", ",", ".", ";", ":", "!", "?"} {
		cases = append(cases, lueCase{"省略 then terminator " + term, lueP + "省略" + term, true})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := lueCalled(tc.text); got != tc.wantCalled {
				t.Errorf("CheckVagueness(%q) 略 called = %v; want %v (warnings: %v)",
					tc.text, got, tc.wantCalled, CheckVagueness("description", tc.text, "general"))
			}
		})
	}

	t.Run("one warning per call however many occurrences", lueOneWarningPerCall)
	t.Run("other phrases are untouched", lueOtherPhrasesUntouched)
	t.Run("short text without file:line", lueShortTextOnly)
	t.Run("chore kind skips the scan", lueChoreSkipsScan)
}

// lueOneWarningPerCall: many 略 occurrences still yield exactly one warning,
// byte-identical to the pre-allowlist text.
func lueOneWarningPerCall(t *testing.T) {
	t.Parallel()
	for _, text := range []string{lueP + "省略 A,其餘略", lueP + strings.Repeat("略 ", 50)} {
		got := CheckVagueness("description", text, "general")
		if len(got) != 1 || got[0] != lueWarning {
			t.Errorf("CheckVagueness(%q) = %v; want exactly [%s]", text, got, lueWarning)
		}
	}
}

// lueOtherPhrasesUntouched: the 等等 phrase keeps its own warning.
func lueOtherPhrasesUntouched(t *testing.T) {
	t.Parallel()
	want := `description: contains vague phrase "等等"`
	found := false
	for _, w := range CheckVagueness("description", lueP+"等等", "general") {
		if w == want {
			found = true
		}
	}
	if !found {
		t.Errorf("CheckVagueness(%q) lost the %q warning", lueP+"等等", want)
	}
}

// lueShortTextOnly: without a file:line prefix the short-text warning fires
// alongside (or instead of) the 略 warning.
func lueShortTextOnly(t *testing.T) {
	t.Parallel()
	// 省略 X 則不變 is 8 runes, so only the short-text warning fires.
	const shortOnly = "description: too short (< 30 chars) and no file:line reference"
	got := CheckVagueness("description", "省略 X 則不變", "general")
	if len(got) != 1 || got[0] != shortOnly {
		t.Errorf("CheckVagueness(省略 X 則不變) = %v; want exactly [%s]", got, shortOnly)
	}
	both := CheckVagueness("description", "細節略", "general")
	var sawLue, sawShort bool
	for _, w := range both {
		sawLue = sawLue || w == lueWarning
		sawShort = sawShort || w == shortOnly
	}
	if !sawLue || !sawShort {
		t.Errorf("CheckVagueness(細節略) = %v; want both the 略 and the too-short warning", both)
	}
}

// lueChoreSkipsScan: kind chore returns nil, the same text under general does not.
func lueChoreSkipsScan(t *testing.T) {
	t.Parallel()
	if got := CheckVagueness("description", lueP+"細節略", "chore"); got != nil {
		t.Errorf("chore CheckVagueness = %v; want nil", got)
	}
	if got := CheckVagueness("description", lueP+"細節略", "general"); len(got) == 0 {
		t.Error("general CheckVagueness of the same text = empty; want the 略 warning")
	}
}

// TestCheckVagueness_LueLinearTime guards the [F1011-22] promise that the 略
// scan stays linear (no ReDoS): four adversarial shapes must each return in
// under a second. It runs serially on purpose so parallel tests cannot skew
// the clock.
func TestCheckVagueness_LueLinearTime(t *testing.T) {
	const limit = time.Second

	cases := []lueCase{
		{"100k bare 略", strings.Repeat("略", 100_000), true},
		{"100k 省略 then x", strings.Repeat("省略 ", 100_000) + "x", false},
		{"省略 then 1M spaces then x", "省略" + strings.Repeat(" ", 1_000_000) + "x", false},
		{"100k 省略略過 then x", strings.Repeat("省略略過", 100_000) + "x", false},
		{"省略 then 1M asterisks then x", "省略" + strings.Repeat("*", 1_000_000) + "x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			got := lueCalled(tc.text)
			elapsed := time.Since(start)
			if elapsed > limit {
				t.Errorf("CheckVagueness took %v; want under %v (quadratic scan?)", elapsed, limit)
			}
			if got != tc.wantCalled {
				t.Errorf("略 called = %v; want %v", got, tc.wantCalled)
			}
		})
	}
}

// TestLueCompounds_Invariants pins what the offset arithmetic in lueCovers
// relies on [F1011-22]: every allowlist word holds exactly one 略 and at least
// two runes. Appending 略 (one rune) or 略略 (two 略) to either list fails here.
func TestLueCompounds_Invariants(t *testing.T) {
	t.Parallel()

	groups := []struct {
		name  string
		words []string
	}{
		{"lueCompounds", lueCompounds},
		{"lueOmitCompounds", lueOmitCompounds},
	}
	for _, g := range groups {
		for _, w := range g.words {
			if n := strings.Count(w, "略"); n != 1 {
				t.Errorf("%s entry %q has %d occurrences of 略; want exactly 1", g.name, w, n)
			}
			if n := utf8.RuneCountInString(w); n < 2 {
				t.Errorf("%s entry %q has %d runes; want at least 2", g.name, w, n)
			}
		}
	}
}

func TestHasFileLineRef(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text string
		want bool
	}{
		{"internal/gtd/store.go:392", true},
		{"main.go:1", true},
		{"foo/bar:42", true},
		{"no colon here", false},
		{"word:notanumber", false},
		{":42", false},
		{"a.b:0", false}, // no .go suffix and no slash — not a file:line
		{"a.go:0", true},
		{"", false},
	}
	for _, c := range cases {
		got := hasFileLineRef(c.text)
		if got != c.want {
			t.Errorf("hasFileLineRef(%q) = %v; want %v", c.text, got, c.want)
		}
	}
}
