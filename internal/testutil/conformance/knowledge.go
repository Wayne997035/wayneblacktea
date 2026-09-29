package conformance

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
)

// knowledgeSmokeItemTitle/knowledgeSmokeItemContent are the fixed literal
// values every caller of RunKnowledgeSmoke MUST use when creating the item
// behind embeddedItemID (row 9's "AddItem happy path... caller does this
// before calling in" setup) — RunKnowledgeSmoke asserts against these
// exact values rather than taking them as parameters, since embedding
// attachment itself is backend-specific and already threaded through the
// embeddedItemID/queryVec parameters.
const (
	knowledgeSmokeItemTitle   = "smoke-item"
	knowledgeSmokeItemContent = "c"
)

// RunKnowledgeSmoke runs the 2 shared knowledge behaviours (AddItem+GetByID
// happy path, SearchByCosine finding the pre-embedded item while excluding
// an unembedded one — acceptance row 10's negative case) against store, as
// t.Run subtests named backend+"/"+<op>. embeddedItemID/queryVec are
// backend-specific setup the caller performs beforehand (see
// knowledgeSmokeItemTitle/Content above for the AddItem contract this
// function assumes). unembeddedItemID is a second item the caller created
// WITHOUT attaching an embedding — on Postgres, via a second store built
// with a nil embed client (knowledge.NewStore(pool, nil, wsID)), so
// AddItem's embedAndCheckDupAtLevel short-circuits at its `s.embed == nil`
// check (internal/knowledge/store.go) and never touches the fixed-vector
// test embedder or its similarity-dedup guard; on SQLite, AddItem never
// embeds regardless, so no second store is needed there.
func RunKnowledgeSmoke(
	t *testing.T, store knowledge.StoreIface, embeddedItemID, unembeddedItemID uuid.UUID, queryVec []float32, backend string,
) {
	ctx := context.Background()

	t.Run(backend+"/AddItem_HappyPath_ThenGetByID", func(t *testing.T) {
		got, err := store.GetByID(ctx, embeddedItemID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Title != knowledgeSmokeItemTitle {
			t.Errorf("Title = %q, want %q", got.Title, knowledgeSmokeItemTitle)
		}
		if got.Content != knowledgeSmokeItemContent {
			t.Errorf("Content = %q, want %q", got.Content, knowledgeSmokeItemContent)
		}
	})

	t.Run(backend+"/SearchByCosine_FindsEmbeddedExcludesUnembedded", func(t *testing.T) {
		rows, err := store.SearchByCosine(ctx, queryVec, 5)
		if err != nil {
			t.Fatalf("SearchByCosine: %v", err)
		}
		var foundEmbedded, foundUnembedded bool
		for _, r := range rows {
			if r.ID == embeddedItemID {
				foundEmbedded = true
			}
			if r.ID == unembeddedItemID {
				foundUnembedded = true
			}
		}
		if !foundEmbedded {
			t.Errorf("SearchByCosine results %v missing embedded item %s", rows, embeddedItemID)
		}
		if foundUnembedded {
			t.Errorf("SearchByCosine results %v include unembedded item %s, want excluded", rows, unembeddedItemID)
		}
	})
}
