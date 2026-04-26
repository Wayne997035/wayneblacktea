package knowledge

import (
	"errors"
	"strings"
	"testing"
)

// TestErrDuplicate_Error verifies the formatted error message.
func TestErrDuplicate_Error(t *testing.T) {
	cases := []struct {
		name          string
		err           ErrDuplicate
		wantSubstring string
	}{
		{
			name:          "100% match (URL exact)",
			err:           ErrDuplicate{ExistingTitle: "Go 1.21 Release Notes", Similarity: 1.0},
			wantSubstring: "Go 1.21 Release Notes",
		},
		{
			name:          "92% similarity match",
			err:           ErrDuplicate{ExistingTitle: "Understanding Context in Go", Similarity: 0.92},
			wantSubstring: "92%",
		},
		{
			name:          "88% similarity threshold",
			err:           ErrDuplicate{ExistingTitle: "Effective Go Patterns", Similarity: 0.88},
			wantSubstring: "88%",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			if msg == "" {
				t.Fatal("ErrDuplicate.Error() must not be empty")
			}
			if len(msg) < 10 {
				t.Errorf("ErrDuplicate.Error() suspiciously short: %q", msg)
			}
			// Verify the message contains either the expected substring or "already saved".
			if !strings.Contains(msg, tc.wantSubstring) && !strings.Contains(msg, "already saved") {
				t.Errorf("ErrDuplicate.Error() = %q, want substring %q", msg, tc.wantSubstring)
			}
		})
	}
}

// TestErrDuplicate_ErrorsAs verifies errors.As works for callers checking the type.
func TestErrDuplicate_ErrorsAs(t *testing.T) {
	original := ErrDuplicate{ExistingTitle: "Some Title", Similarity: 0.91}

	var target ErrDuplicate
	if !errors.As(original, &target) {
		t.Fatal("errors.As should match ErrDuplicate value type")
	}
	if target.ExistingTitle != original.ExistingTitle {
		t.Errorf("ExistingTitle: got %q, want %q", target.ExistingTitle, original.ExistingTitle)
	}
	if target.Similarity != original.Similarity {
		t.Errorf("Similarity: got %v, want %v", target.Similarity, original.Similarity)
	}
}

// TestErrDuplicate_NotErrNotFound verifies ErrDuplicate is distinct from ErrNotFound.
func TestErrDuplicate_NotErrNotFound(t *testing.T) {
	dup := ErrDuplicate{ExistingTitle: "x", Similarity: 0.9}
	if errors.Is(dup, ErrNotFound) {
		t.Error("ErrDuplicate must not match ErrNotFound")
	}
}

// TestDedupThreshold validates the constant is within expected bounds.
func TestDedupThreshold(t *testing.T) {
	if dedupSimilarityThreshold < 0.80 || dedupSimilarityThreshold > 0.95 {
		t.Errorf("dedupSimilarityThreshold = %v, expected between 0.80 and 0.95", dedupSimilarityThreshold)
	}
}
