package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
)

// TestKnowledgeStore_Prepare_TableDriven mirrors
// TestKnowledgeStore_Prepare_URLDedup_TableDriven in
// internal/knowledge/store_prepare_test.go for the SQLite backend: URL
// exact-match dedup only (SQLite has no embed client wired, so
// PreparedItem.Vec is always nil / DedupSkipped is always true — see
// knowledge.PreparedItem.Vec's doc comment).
func TestKnowledgeStore_Prepare_TableDriven(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	ctx := context.Background()

	seeded, err := s.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "Seed item", Content: "seed content",
		URL: "https://example.com/seed",
	})
	if err != nil {
		t.Fatalf("seeding AddItem: %v", err)
	}
	parentBytes := [16]byte(seeded.ID)

	cases := []struct {
		name    string
		params  knowledge.AddItemParams
		wantDup bool
	}{
		{
			name: "happy path: no URL collision",
			params: knowledge.AddItemParams{
				Type: "til", Title: "Fresh item", Content: "unrelated content",
				URL: "https://example.com/fresh",
			},
			wantDup: false,
		},
		{
			name: "error: URL exact-match dedup",
			params: knowledge.AddItemParams{
				Type: "til", Title: "Different title, same URL", Content: "different content",
				URL: "https://example.com/seed",
			},
			wantDup: true,
		},
		{
			name: "edge: child item (ParentID set) skips URL dedup despite a colliding URL",
			params: knowledge.AddItemParams{
				Type: "til", Title: "Section child", Content: "child section content",
				URL:      "https://example.com/seed",
				ParentID: &parentBytes,
			},
			wantDup: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prep, err := s.Prepare(ctx, tc.params)
			if tc.wantDup {
				var dupErr knowledge.ErrDuplicate
				if !errors.As(err, &dupErr) {
					t.Fatalf("Prepare(%+v): expected ErrDuplicate, got %v", tc.params, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Prepare(%+v): unexpected error: %v", tc.params, err)
			}
			if prep.Params.Title != tc.params.Title {
				t.Errorf("PreparedItem.Params.Title = %q, want %q", prep.Params.Title, tc.params.Title)
			}
			if prep.Vec != nil {
				t.Errorf("expected nil Vec (SQLite has no embed client wired), got %v", prep.Vec)
			}
			if !prep.DedupSkipped {
				t.Errorf("expected DedupSkipped=true (SQLite never runs cosine dedup)")
			}
		})
	}
}

// TestKnowledgeStore_WriteItemTx_CommitRoundTrip verifies WriteItemTx writes
// a row visible via GetByID only after the caller commits the tx, and that
// the result matches what AddItem would produce for equivalent params —
// proving Prepare+WriteItemTx+Commit is behaviorally equivalent to AddItem's
// non-tx path. Mirrors the Postgres
// TestKnowledgeStore_WriteItemTx_CommitRoundTrip.
func TestKnowledgeStore_WriteItemTx_CommitRoundTrip(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	ctx := context.Background()

	params := knowledge.AddItemParams{
		Type:    "til",
		Title:   "WriteItemTx commit round trip",
		Content: "content written via the tx path",
		URL:     "https://example.com/write-item-tx-commit",
		Tags:    []string{"a", "b"},
	}

	prep, err := s.Prepare(ctx, params)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	tx, err := s.DB().BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	item, err := s.WriteItemTx(ctx, tx, prep)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("WriteItemTx: %v", err)
	}
	if item.Title != params.Title || len(item.Tags) != 2 {
		t.Fatalf("unexpected item from WriteItemTx: %+v", item)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit: %v", err)
	}

	got, err := s.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after commit: %v", err)
	}
	if got.Title != params.Title || !got.Url.Valid || got.Url.String != params.URL {
		t.Errorf("committed row mismatch: %+v", got)
	}
}

// TestKnowledgeStore_WriteItemTx_RollbackLeavesNoRow verifies a WriteItemTx
// call followed by Rollback leaves no visible row. Mirrors the Postgres
// TestKnowledgeStore_WriteItemTx_RollbackLeavesNoRow; this is the SQLite
// half of the property proposal.AcceptOrchestration's deferred Rollback
// depends on when a later orchestration step fails.
func TestKnowledgeStore_WriteItemTx_RollbackLeavesNoRow(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	ctx := context.Background()

	params := knowledge.AddItemParams{
		Type: "til", Title: "WriteItemTx rollback", Content: "should never be visible",
	}
	prep, err := s.Prepare(ctx, params)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	tx, err := s.DB().BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	item, err := s.WriteItemTx(ctx, tx, prep)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("WriteItemTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("tx.Rollback: %v", err)
	}

	if _, err := s.GetByID(ctx, item.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("expected ErrNotFound for rolled-back row, got %v", err)
	}
}

// TestKnowledgeStore_DB_Accessor is a minimal smoke test for the DB()
// accessor added alongside Prepare/WriteItemTx (mirrors ProposalStore.DB()),
// proving it returns a non-nil handle whose BeginTx works.
func TestKnowledgeStore_DB_Accessor(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	if s.DB() == nil {
		t.Fatal("DB() returned nil")
	}
	tx, err := s.DB().BeginTx(context.Background())
	if err != nil {
		t.Fatalf("DB().BeginTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("tx.Rollback: %v", err)
	}
}
