package knowledge

import (
	"testing"

	"github.com/google/uuid"
	"github.com/waynechen/wayneblacktea/internal/db"
)

func makeItem(title string) db.KnowledgeItem {
	return db.KnowledgeItem{
		ID:    uuid.New(),
		Title: title,
		Type:  "til",
	}
}

func TestMergeRRF_EmptyInputs(t *testing.T) {
	result := mergeRRF(nil, nil, 10)
	if len(result) != 0 {
		t.Errorf("empty inputs: got %d items, want 0", len(result))
	}
}

func TestMergeRRF_FTSOnly(t *testing.T) {
	a := makeItem("alpha")
	b := makeItem("beta")
	c := makeItem("gamma")
	fts := []db.KnowledgeItem{a, b, c}

	result := mergeRRF(fts, nil, 10)
	if len(result) != 3 {
		t.Fatalf("fts only: got %d items, want 3", len(result))
	}
	// Items must appear and be drawn from fts list.
	titles := make(map[string]bool)
	for _, item := range result {
		titles[item.Title] = true
	}
	for _, expected := range []string{"alpha", "beta", "gamma"} {
		if !titles[expected] {
			t.Errorf("fts only: missing item %q in result", expected)
		}
	}
}

func TestMergeRRF_Deduplication(t *testing.T) {
	shared := makeItem("shared")
	fts := []db.KnowledgeItem{shared}
	vec := []db.KnowledgeItem{shared}

	result := mergeRRF(fts, vec, 10)
	if len(result) != 1 {
		t.Errorf("deduplication: got %d items, want 1", len(result))
	}
}

func TestMergeRRF_OrderByScore(t *testing.T) {
	// inBoth appears in both fts and vec → higher RRF score.
	// ftsOnly appears only in fts → lower score.
	inBoth := makeItem("in-both")
	ftsOnly := makeItem("fts-only")

	fts := []db.KnowledgeItem{ftsOnly, inBoth}
	vec := []db.KnowledgeItem{inBoth}

	result := mergeRRF(fts, vec, 10)
	if len(result) != 2 {
		t.Fatalf("order by score: got %d items, want 2", len(result))
	}
	// inBoth should rank first (higher combined score).
	if result[0].ID != inBoth.ID {
		t.Errorf("order by score: expected %q first, got %q", inBoth.Title, result[0].Title)
	}
}

func TestMergeRRF_LimitRespected(t *testing.T) {
	items := make([]db.KnowledgeItem, 10)
	for i := range items {
		items[i] = makeItem("item")
	}

	result := mergeRRF(items, nil, 3)
	if len(result) != 3 {
		t.Errorf("limit respected: got %d items, want 3", len(result))
	}
}

func TestMergeRRF_VecItemsIncluded(t *testing.T) {
	// Items that only appear in vec should still be returned when limit allows.
	vecOnly1 := makeItem("vec-only-1")
	vecOnly2 := makeItem("vec-only-2")
	ftsItem := makeItem("fts-item")

	fts := []db.KnowledgeItem{ftsItem}
	vec := []db.KnowledgeItem{vecOnly1, vecOnly2}

	result := mergeRRF(fts, vec, 10)
	if len(result) != 3 {
		t.Fatalf("vec items included: got %d items, want 3", len(result))
	}
	ids := make(map[uuid.UUID]bool)
	for _, item := range result {
		ids[item.ID] = true
	}
	for _, expected := range []db.KnowledgeItem{vecOnly1, vecOnly2, ftsItem} {
		if !ids[expected.ID] {
			t.Errorf("vec items included: missing item %q in result", expected.Title)
		}
	}
}
