package handler_test

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
)

// TestValidateBranchName_RuneLength is the round-1 follow-up for GTD
// ce3e0747: the previous len(s) > 255 check counted bytes, so a 85-character
// CJK branch name (~255 bytes, 85 runes) was rejected even though git itself
// accepts it. The fix uses len([]rune(s)) so byte-encoding no longer
// determines whether a name passes.
func TestValidateBranchName_RuneLength(t *testing.T) {
	// 85 CJK chars ("漢" 3 bytes each = 255 bytes). 85 runes ≤ 255 → accepted.
	cjk85 := strings.Repeat("漢", 85)
	// 256 ASCII chars (256 runes, 256 bytes) → rejected.
	ascii256 := strings.Repeat("a", 256)
	// 256 CJK chars (256 runes, 768 bytes) → rejected (rune count, not bytes).
	cjk256 := strings.Repeat("漢", 256)

	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty_is_valid", "", false},
		{"short_ascii", "feature/foo", false},
		{"ascii_255_boundary", strings.Repeat("a", 255), false},
		{"ascii_256_rejected", ascii256, true},
		{"cjk_85_chars_255_bytes_accepted", cjk85, false},
		{"cjk_256_chars_rejected", cjk256, true},
		{"control_char_rejected", "feature/\x01bad", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := handler.ValidateBranchNameForTest(tc.input)
			if (msg != "") != tc.wantErr {
				t.Errorf("validateBranchName(%q): err=%q, wantErr=%v",
					tc.input, msg, tc.wantErr)
			}
		})
	}
}

// TestStrictVagueness_ParseBool verifies the handler-package strictVagueness
// helper accepts the strconv.ParseBool truthy set ("1", "t", "T", "true",
// "True", "TRUE") and rejects everything else (including the previously-
// accepted-only "true" plus "0", empty, garbage). Round-1 follow-up GTD
// 6cda1ce2.
func TestStrictVagueness_ParseBool(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"truthy_1", "1", true},
		{"truthy_lower_t", "t", true},
		{"truthy_upper_T", "T", true},
		{"truthy_true", "true", true},
		{"truthy_True", "True", true},
		{"truthy_TRUE", "TRUE", true},
		{"falsy_0", "0", false},
		{"falsy_false", "false", false},
		{"falsy_empty", "", false},
		{"invalid_yes_falsy", "yes", false},
		{"invalid_2_falsy", "2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WBT_STRICT_VAGUENESS", tc.env)
			if got := handler.StrictVaguenessForTest(); got != tc.want {
				t.Errorf("strictVagueness() with env=%q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
