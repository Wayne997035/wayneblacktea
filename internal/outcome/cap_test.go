package outcome

import (
	"testing"

	"github.com/google/uuid"
)

// TestCapRelatedRuleIDs covers the pure helper both backends' FinalizeDraft
// use to bound related_rule_ids at MaxRelatedRuleIDsTotal without ever
// dropping an already-linked ID — PR #152 round 6 Major finding m-R6-3. See
// its doc comment for the full rationale; this pins the algorithm itself in
// isolation (fast, no DB) as a complement to the slower DB-level parity
// tests (store_test.go / storage/sqlite/outcome_test.go).
func TestCapRelatedRuleIDs(t *testing.T) {
	ids := make([]uuid.UUID, 12)
	for i := range ids {
		ids[i] = uuid.New()
	}

	tests := []struct {
		name     string
		existing []uuid.UUID
		merged   []uuid.UUID // existing followed by new, as unionRelatedRuleIDs would produce
		cap      int
		want     []uuid.UUID
	}{
		{
			name:     "merged already within cap: unchanged",
			existing: ids[0:2],
			merged:   ids[0:3],
			cap:      5,
			want:     ids[0:3],
		},
		{
			name:     "merged exceeds cap, existing under cap: slice to cap keeps all existing + fills remaining",
			existing: ids[0:2],
			merged:   ids[0:5],
			cap:      3,
			want:     ids[0:3], // existing (2) + 1 new
		},
		{
			name:     "existing exactly at cap: no new IDs admitted",
			existing: ids[0:3],
			merged:   ids[0:5],
			cap:      3,
			want:     ids[0:3], // existing only, both new candidates dropped
		},
		{
			name:     "existing already OVER cap (legacy data): existing preserved in full, all new dropped",
			existing: ids[0:5],
			merged:   ids[0:8],
			cap:      3,
			want:     ids[0:5], // all 5 existing survive despite cap=3, 3 new candidates dropped
		},
		{
			name:     "no new IDs at all: merged == existing, always a no-op regardless of cap",
			existing: ids[0:5],
			merged:   ids[0:5],
			cap:      3,
			want:     ids[0:5],
		},
		{
			name:     "empty existing, fresh IDs under cap",
			existing: nil,
			merged:   ids[0:2],
			cap:      5,
			want:     ids[0:2],
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CapRelatedRuleIDs(tc.existing, tc.merged, tc.cap)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (got=%v want=%v)", len(got), len(tc.want), got, tc.want)
			}
			for i, id := range tc.want {
				if got[i] != id {
					t.Errorf("[%d] = %s, want %s", i, got[i], id)
				}
			}
		})
	}
}

// TestCapNotesTotal is CapRelatedRuleIDs's twin for the Notes cumulative cap
// (MaxNotesTotalRunes, PR #152 round 6 Major M-R6-2 guarantee B) — same
// "never drop existing content, even if existing already exceeds the cap"
// rule, operating on runes instead of array elements.
func TestCapNotesTotal(t *testing.T) {
	tests := []struct {
		name          string
		existing      string
		merged        string
		cap           int
		want          string
		wantTruncated bool
	}{
		{
			name:          "merged already within cap: unchanged, not truncated",
			existing:      "aaa",
			merged:        "aaa\n\nbbb",
			cap:           20,
			want:          "aaa\n\nbbb",
			wantTruncated: false,
		},
		{
			name:          "merged exceeds cap, existing under cap: keeps all existing + partial new tail",
			existing:      "aaaaa",               // 5 runes
			merged:        "aaaaa\n\nbbbbbbbbbb", // 5+2+10=17 runes
			cap:           10,
			want:          "aaaaa\n\nbbb", // 5 existing + 2 sep + first 3 of new = 10
			wantTruncated: true,
		},
		{
			name:          "existing already AT cap: entire new segment dropped",
			existing:      "aaaaaaaaaa", // 10 runes
			merged:        "aaaaaaaaaa\n\nbbbbb",
			cap:           10,
			want:          "aaaaaaaaaa",
			wantTruncated: true,
		},
		{
			name:          "existing already OVER cap (legacy data): existing preserved in full, new segment dropped",
			existing:      "aaaaaaaaaaaaaaa", // 15 runes
			merged:        "aaaaaaaaaaaaaaa\n\nbbbbb",
			cap:           10,
			want:          "aaaaaaaaaaaaaaa", // all 15 survive despite cap=10
			wantTruncated: true,
		},
		{
			name:          "no new content: merged == existing, always a no-op regardless of cap",
			existing:      "aaaaaaaaaaaaaaa",
			merged:        "aaaaaaaaaaaaaaa",
			cap:           10,
			want:          "aaaaaaaaaaaaaaa",
			wantTruncated: false,
		},
		{
			name:          "multi-byte runes counted correctly, not bytes",
			existing:      "",
			merged:        "日本語のテキスト", // 8 runes, 24 bytes in UTF-8
			cap:           5,
			want:          "日本語のテ",
			wantTruncated: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := CapNotesTotal(tc.existing, tc.merged, tc.cap)
			if got != tc.want {
				t.Errorf("result = %q, want %q", got, tc.want)
			}
			if truncated != tc.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tc.wantTruncated)
			}
		})
	}
}

// TestNotesTruncated covers the pure helper PR #152 round 7 Major m-R7-1/
// m-R7-2 introduced to REPLACE FinalizeDraft's removed self-join diagnostic:
// it compares only the caller's own supplied text against the final
// RETURNING value, with no separate table read. Isolated, fast pin of the
// algorithm; store_test.go's DB-backed tests cover it end-to-end (including
// under real concurrency).
func TestNotesTruncated(t *testing.T) {
	tests := []struct {
		name       string
		finalNotes string
		newNotes   string
		wantTrunc  bool
	}{
		{
			name:       "fresh row, direct write: not truncated",
			finalNotes: "hello world",
			newNotes:   "hello world",
			wantTrunc:  false,
		},
		{
			name:       "enrich append landed in full: final ends with the new segment",
			finalNotes: "existing content\n\nnewly appended segment",
			newNotes:   "newly appended segment",
			wantTrunc:  false,
		},
		{
			name:       "nothing supplied: never truncated regardless of final value",
			finalNotes: "aaaaaaaaaa",
			newNotes:   "",
			wantTrunc:  false,
		},
		{
			name:       "cap cut the new segment's tail off: final does NOT end with the full new text",
			finalNotes: "existing\n\npartial-of-n",
			newNotes:   "partial-of-new-segment-that-got-cut",
			wantTrunc:  true,
		},
		{
			name:       "cap dropped the new segment entirely: final == existing only",
			finalNotes: "existing content, unchanged",
			newNotes:   "this whole segment got dropped",
			wantTrunc:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NotesTruncated(tc.finalNotes, tc.newNotes)
			if got != tc.wantTrunc {
				t.Errorf("NotesTruncated(%q, %q) = %v, want %v", tc.finalNotes, tc.newNotes, got, tc.wantTrunc)
			}
		})
	}
}

// TestRelatedRuleIDsTruncated is TestNotesTruncated's twin for
// related_rule_ids.
func TestRelatedRuleIDsTruncated(t *testing.T) {
	ids := make([]uuid.UUID, 5)
	for i := range ids {
		ids[i] = uuid.New()
	}

	tests := []struct {
		name      string
		finalIDs  []uuid.UUID
		newIDs    []uuid.UUID
		wantTrunc bool
	}{
		{
			name:      "fresh row: every supplied ID present in final",
			finalIDs:  ids[0:2],
			newIDs:    ids[0:2],
			wantTrunc: false,
		},
		{
			name:      "enrich union landed in full: all new IDs present alongside existing",
			finalIDs:  ids[0:3],
			newIDs:    ids[2:3],
			wantTrunc: false,
		},
		{
			name:      "nothing supplied: never truncated regardless of final value",
			finalIDs:  ids[0:2],
			newIDs:    nil,
			wantTrunc: false,
		},
		{
			name:      "cap dropped the one new ID: it's missing from final",
			finalIDs:  ids[0:3], // existing only, new ID never made it in
			newIDs:    ids[3:4],
			wantTrunc: true,
		},
		{
			name:      "cap dropped SOME but not all new IDs: any missing ID counts as truncated",
			finalIDs:  ids[0:4], // 3 existing + 1 of the 2 new IDs survived
			newIDs:    ids[3:5], // 2 new IDs supplied, only ids[3] made it into finalIDs
			wantTrunc: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RelatedRuleIDsTruncated(tc.finalIDs, tc.newIDs)
			if got != tc.wantTrunc {
				t.Errorf("RelatedRuleIDsTruncated(%v, %v) = %v, want %v", tc.finalIDs, tc.newIDs, got, tc.wantTrunc)
			}
		})
	}
}
