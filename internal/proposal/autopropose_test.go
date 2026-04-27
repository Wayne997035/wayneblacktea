package proposal_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/waynechen/wayneblacktea/internal/db"
	"github.com/waynechen/wayneblacktea/internal/proposal"
)

func TestShouldAutoProposeFor(t *testing.T) {
	cases := []struct {
		name string
		item *db.KnowledgeItem
		want bool
	}{
		{"article propose", &db.KnowledgeItem{Type: "article", Title: "Go generics deep dive"}, true},
		{"til propose", &db.KnowledgeItem{Type: "til", Title: "errcheck nuances"}, true},
		{"zettelkasten propose", &db.KnowledgeItem{Type: "zettelkasten", Title: "DDD"}, true},
		{"bookmark skip", &db.KnowledgeItem{Type: "bookmark", Title: "Tailwind v4"}, false},
		{"empty title skip", &db.KnowledgeItem{Type: "article", Title: ""}, false},
		{"nil skip", nil, false},
		{"unknown type skip", &db.KnowledgeItem{Type: "podcast", Title: "weird"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := proposal.ShouldAutoProposeFor(tc.item)
			if got != tc.want {
				t.Errorf("ShouldAutoProposeFor(%+v) = %v, want %v", tc.item, got, tc.want)
			}
		})
	}
}

func TestConceptCandidate_JSONRoundtrip(t *testing.T) {
	id := uuid.New()
	in := proposal.ConceptCandidate{
		Title:          "FSRS scheduling",
		Content:        "stability * exp((d-r)/s)",
		Tags:           []string{"learning", "spaced-repetition"},
		SourceItemID:   id.String(),
		SourceItemType: "article",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var out proposal.ConceptCandidate
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Title != in.Title || out.Content != in.Content ||
		out.SourceItemID != in.SourceItemID || out.SourceItemType != in.SourceItemType ||
		!equalStringSlices(out.Tags, in.Tags) {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", out, in)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestConceptCandidate_OmitEmptyTags(t *testing.T) {
	in := proposal.ConceptCandidate{Title: "t", Content: "c"}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Tags omitempty: empty/nil slice should not appear in JSON.
	if got := string(raw); contains(got, `"tags"`) {
		t.Errorf("expected omitempty to drop tags; got %s", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
