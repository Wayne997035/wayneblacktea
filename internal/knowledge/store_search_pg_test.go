package knowledge_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
)

// TestKnowledgeStore_Pg_Search verifies that Store.Search executes without
// the "column does not exist" Postgres error that occurred when ORDER BY
// referenced SELECT-list aliases (strength, sim) directly in the outer query.
// Migration 000040 must also apply cleanly (GIN expression index).
func TestKnowledgeStore_Pg_Search(t *testing.T) {
	pool := openKnowledgePgPool(t)
	wsID := uuid.New()
	store := knowledge.NewStore(pool, nil, &wsID)
	ctx := context.Background()

	cases := []struct {
		name    string
		setup   func(t *testing.T) // insert items before searching
		query   string
		limit   int
		wantMin int // minimum expected result count (may be 0 for no-match cases)
		wantErr bool
	}{
		{
			name:    "no items — empty result no error",
			setup:   func(_ *testing.T) {},
			query:   "knowledge graph",
			limit:   10,
			wantMin: 0,
			wantErr: false,
		},
		{
			name: "matching item returned",
			setup: func(t *testing.T) {
				t.Helper()
				_, err := store.AddItem(ctx, knowledge.AddItemParams{
					Type:    "til",
					Title:   "knowledge graph fundamentals",
					Content: "A knowledge graph connects entities and their relationships.",
				})
				if err != nil {
					t.Fatalf("AddItem: %v", err)
				}
			},
			query:   "knowledge graph",
			limit:   10,
			wantMin: 1,
			wantErr: false,
		},
		{
			name: "query with no match returns empty slice no error",
			setup: func(t *testing.T) {
				t.Helper()
				_, err := store.AddItem(ctx, knowledge.AddItemParams{
					Type:    "article",
					Title:   "Go concurrency patterns",
					Content: "Goroutines and channels are first-class in Go.",
				})
				if err != nil {
					t.Fatalf("AddItem: %v", err)
				}
			},
			query:   "xyzzy_nomatch_sentinel",
			limit:   5,
			wantMin: 0,
			wantErr: false,
		},
		{
			name: "limit respected",
			setup: func(t *testing.T) {
				t.Helper()
				for i := 0; i < 5; i++ {
					_, err := store.AddItem(ctx, knowledge.AddItemParams{
						Type:    "zettelkasten",
						Title:   "distributed systems overview",
						Content: "Distributed systems involve multiple nodes coordinating.",
					})
					if err != nil {
						t.Fatalf("AddItem %d: %v", i, err)
					}
				}
			},
			query:   "distributed systems",
			limit:   2,
			wantMin: 0, // at least 0 — verifies the query executes without error
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			results, err := store.Search(ctx, tc.query, tc.limit)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Search(%q, %d): unexpected error: %v", tc.query, tc.limit, err)
			}
			if len(results) < tc.wantMin {
				t.Errorf("Search(%q, %d): got %d results, want >= %d", tc.query, tc.limit, len(results), tc.wantMin)
			}
			if len(results) > tc.limit {
				t.Errorf("Search(%q, %d): got %d results, exceeds limit %d", tc.query, tc.limit, len(results), tc.limit)
			}
		})
	}
}

// TestKnowledgeStore_Pg_SearchCoarse verifies that Store.SearchCoarse executes
// without the "column does not exist" Postgres error, and that it filters to
// root-only rows (parent_id IS NULL, heading_level = 0).
func TestKnowledgeStore_Pg_SearchCoarse(t *testing.T) {
	pool := openKnowledgePgPool(t)
	wsID := uuid.New()
	store := knowledge.NewStore(pool, nil, &wsID)
	ctx := context.Background()

	cases := []struct {
		name    string
		setup   func(t *testing.T)
		query   string
		limit   int
		wantMin int
		wantErr bool
	}{
		{
			name:    "no items — empty result no error",
			setup:   func(_ *testing.T) {},
			query:   "knowledge graph",
			limit:   5,
			wantMin: 0,
			wantErr: false,
		},
		{
			name: "root item returned by SearchCoarse",
			setup: func(t *testing.T) {
				t.Helper()
				_, err := store.AddItem(ctx, knowledge.AddItemParams{
					Type:    "article",
					Title:   "knowledge graph overview",
					Content: "Top-level article about knowledge graphs.",
				})
				if err != nil {
					t.Fatalf("AddItem root: %v", err)
				}
			},
			query:   "knowledge graph",
			limit:   10,
			wantMin: 1,
			wantErr: false,
		},
		{
			name: "no match returns empty slice no error",
			setup: func(t *testing.T) {
				t.Helper()
				_, err := store.AddItem(ctx, knowledge.AddItemParams{
					Type:    "til",
					Title:   "Go modules",
					Content: "Go modules provide dependency management.",
				})
				if err != nil {
					t.Fatalf("AddItem: %v", err)
				}
			},
			query:   "xyzzy_coarse_nomatch",
			limit:   5,
			wantMin: 0,
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			results, err := store.SearchCoarse(ctx, tc.query, tc.limit)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("SearchCoarse(%q, %d): unexpected error: %v", tc.query, tc.limit, err)
			}
			if len(results) < tc.wantMin {
				t.Errorf("SearchCoarse(%q, %d): got %d results, want >= %d", tc.query, tc.limit, len(results), tc.wantMin)
			}
			if len(results) > tc.limit {
				t.Errorf("SearchCoarse(%q, %d): got %d results, exceeds limit %d", tc.query, tc.limit, len(results), tc.limit)
			}
			// Verify all returned items are root-level (no parent_id).
			for i, item := range results {
				if item.ParentID.Valid {
					t.Errorf("SearchCoarse result[%d] has parent_id=%v, expected root-only items", i, item.ParentID)
				}
			}
		})
	}
}
