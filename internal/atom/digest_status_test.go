package atom_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
)

// TestIsValidDigestStatus verifies the five-value enum and rejects anything
// outside it, including adversarial-shaped input (backend-security-design.md
// §2 — a caller must not be able to slip an arbitrary string past this gate).
func TestIsValidDigestStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"pending is valid", atom.DigestStatusPending, true},
		{"done is valid", atom.DigestStatusDone, true},
		{"failed is valid", atom.DigestStatusFailed, true},
		{"consolidated is valid", atom.DigestStatusConsolidated, true},
		{"promoted is valid", atom.DigestStatusPromoted, true},
		{"empty string is invalid", "", false},
		{"unknown value is invalid", "archived", false},
		{"case-sensitive: uppercase PENDING is invalid", "PENDING", false},
		{"whitespace-padded value is invalid", " pending", false},
		{"sql-injection-shaped payload is invalid", "pending'; DROP TABLE memory_atoms;--", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := atom.IsValidDigestStatus(tc.status)
			if got != tc.want {
				t.Errorf("IsValidDigestStatus(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// TestCheckPromotionEligibility verifies the shared promotion quality gate
// used by both promote_atom_to_knowledge (internal/mcp/tools_atom.go) and
// the weekly atom-bridge cron (internal/scheduler/atom_bridge.go).
func TestCheckPromotionEligibility(t *testing.T) {
	longEnough := strings.Repeat("x", atom.PromotionMinContentRunes)
	tooShort := strings.Repeat("x", atom.PromotionMinContentRunes-1)
	// CJK content: same rune count as longEnough but 3x the byte length —
	// proves the gate counts runes, not bytes.
	cjkLongEnough := strings.Repeat("測", atom.PromotionMinContentRunes)
	twoTags := []string{"a", "b"}
	oneTag := []string{"a"}

	tests := []struct {
		name          string
		content       string
		tags          []string
		wantEligible  bool
		wantContentOK bool
		wantTagsOK    bool
	}{
		{"exact boundary content + exact boundary tags is eligible", longEnough, twoTags, true, true, true},
		{"one rune under minimum content is not eligible", tooShort, twoTags, false, false, true},
		{"one tag under minimum is not eligible", longEnough, oneTag, false, true, false},
		{"both content and tags under minimum is not eligible", tooShort, oneTag, false, false, false},
		{"CJK content measured by rune count, not byte length", cjkLongEnough, twoTags, true, true, true},
		{"nil tags is not eligible", longEnough, nil, false, true, false},
		{"empty content is not eligible", "", twoTags, false, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := atom.CheckPromotionEligibility(tc.content, tc.tags)
			if got.Eligible() != tc.wantEligible {
				t.Errorf("Eligible() = %v, want %v", got.Eligible(), tc.wantEligible)
			}
			if got.ContentOK != tc.wantContentOK {
				t.Errorf("ContentOK = %v, want %v", got.ContentOK, tc.wantContentOK)
			}
			if got.TagsOK != tc.wantTagsOK {
				t.Errorf("TagsOK = %v, want %v", got.TagsOK, tc.wantTagsOK)
			}
		})
	}
}

// TestErrInvalidDigestStatus_Wrapping verifies the fmt.Errorf("%w: %q", ...)
// idiom used by both backend stores' SetDigestStatus unwraps correctly via
// errors.Is — this is what callers actually rely on to detect the rejection.
func TestErrInvalidDigestStatus_Wrapping(t *testing.T) {
	wrapped := fmt.Errorf("%w: %q", atom.ErrInvalidDigestStatus, "bogus")
	if !errors.Is(wrapped, atom.ErrInvalidDigestStatus) {
		t.Error("expected errors.Is(wrapped, ErrInvalidDigestStatus) to be true")
	}
}
