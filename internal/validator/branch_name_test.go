package validator

import (
	"strings"
	"testing"
)

// [GTD 1d7898d1] ValidateBranchName is the shared validator behind the MCP
// (tools_gtd.go) and HTTP (gtd_handler.go) branch_name field, and it had no
// direct test: both callers' tests exercised it indirectly, which covers the
// paths those callers happen to take and nothing else. The rune-vs-byte length
// rule and the Unicode-category-C rejection are exactly the parts an indirect
// test is least likely to reach.
//
// Every invisible probe below is built from its code point with string(rune(…))
// and never typed as a literal. A literal U+FEFF makes the file a syntax error
// (`illegal byte order mark`), a literal U+200B is indistinguishable from a
// typo, and neither shows up in a diff.
const (
	cpSoftHyphen     = 0x00AD // Cf — rejected
	cpZeroWidthSpace = 0x200B // Cf — rejected
	cpByteOrderMark  = 0xFEFF // Cf — rejected
	cpPrivateUse     = 0xE000 // Co — rejected
	cpNoBreakSpace   = 0x00A0 // Zs — NOT category C, accepted
	cpLineSeparator  = 0x2028 // Zl — NOT category C, accepted
)

func TestValidateBranchName_Accepts(t *testing.T) {
	t.Parallel()

	// A 255-rune CJK name is 765 bytes in UTF-8. It must pass, because the cap
	// counts runes. If this goes red, the length check has regressed to bytes —
	// the sprint 8-7 gap E divergence between the MCP and HTTP paths.
	cjkAtLimit := strings.Repeat("分", MaxBranchNameLen)
	if got := len([]rune(cjkAtLimit)); got != MaxBranchNameLen {
		t.Fatalf("fixture is wrong: cjkAtLimit is %d runes, want %d", got, MaxBranchNameLen)
	}
	if len(cjkAtLimit) <= MaxBranchNameLen {
		t.Fatalf("fixture proves nothing: cjkAtLimit is %d bytes, which must exceed the rune "+
			"cap %d for a byte-length regression to fail this test", len(cjkAtLimit), MaxBranchNameLen)
	}

	accept := map[string]string{
		"empty":          "", // optional field; callers decide if it is required
		"simple":         "fix/56-login-validation-error",
		"nested slashes": "feature/wbt/0914-p2-sweep",
		"dots and dash":  "release-1.2.3",
		"cjk":            "功能/中文分支名",
		"ascii at limit": strings.Repeat("a", MaxBranchNameLen),
		"cjk at limit":   cjkAtLimit,

		// The next three document the contract rather than endorsing the input:
		// this validator checks length and Unicode category C, nothing else.
		// Tightening it should have to fail a test that says so out loud
		// instead of silently changing what callers are allowed to send.
		"plain space is not rejected here": "fix/login page",
		"U+2028 is category Zl, not C":     "fix/login" + string(rune(cpLineSeparator)) + "page",
		"U+00A0 is category Zs, not C":     "fix/login" + string(rune(cpNoBreakSpace)) + "page",
	}
	for name, s := range accept {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if msg := ValidateBranchName(s); msg != "" {
				t.Errorf("ValidateBranchName(%.40q) = %q, want \"\"", s, msg)
			}
		})
	}
}

func TestValidateBranchName_Rejects(t *testing.T) {
	t.Parallel()

	reject := map[string]struct {
		in   string
		want string // substring the returned message must carry
	}{
		"one rune over":     {strings.Repeat("a", MaxBranchNameLen+1), "255 characters"},
		"cjk one rune over": {strings.Repeat("分", MaxBranchNameLen+1), "255 characters"},

		"null byte":       {"fix/login\x00", "control characters"},
		"newline":         {"fix/login\n", "control characters"},
		"carriage return": {"fix/login\r", "control characters"},
		"tab":             {"fix/\tlogin", "control characters"},
		"escape":          {"fix/\x1blogin", "control characters"},
		"del":             {"fix/login\x7f", "control characters"}, // the r == 0x7F arm

		"soft hyphen":      {"fix/log" + string(rune(cpSoftHyphen)) + "in", "control characters"},
		"zero width space": {"fix/log" + string(rune(cpZeroWidthSpace)) + "in", "control characters"},
		"byte order mark":  {string(rune(cpByteOrderMark)) + "fix/login", "control characters"},
		"private use":      {"fix/log" + string(rune(cpPrivateUse)) + "in", "control characters"},
	}
	for name, tc := range reject {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			msg := ValidateBranchName(tc.in)
			if msg == "" {
				t.Fatalf("ValidateBranchName(%.40q) = \"\", want a rejection", tc.in)
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("ValidateBranchName(%.40q) = %q, want it to mention %q", tc.in, msg, tc.want)
			}
		})
	}
}

// TestValidateBranchName_LengthIsCheckedBeforeContent pins the order the two
// rules run in. An over-long name made entirely of control characters must
// report the length problem: the length branch returns first, and a caller
// shown "control characters" for a 300-character name goes looking for the
// wrong thing.
func TestValidateBranchName_LengthIsCheckedBeforeContent(t *testing.T) {
	t.Parallel()

	over := strings.Repeat("\x00", MaxBranchNameLen+1)
	if msg := ValidateBranchName(over); !strings.Contains(msg, "255 characters") {
		t.Errorf("ValidateBranchName(over-long all-NUL) = %q, want the length message", msg)
	}
}
