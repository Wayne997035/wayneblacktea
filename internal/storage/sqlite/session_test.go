package sqlite_test

import (
	"context"
	"errors"
	"testing"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// openSessionStoreWithDB opens a DB and a SessionStore, returning both so
// tests can execute raw SQL for fixture setup (e.g. custom timestamps).
func openSessionStoreWithDB(t *testing.T, workspaceID string) (*sqlite.DB, *sqlite.SessionStore) {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, sqlite.NewSessionStore(d)
}

func openSessionStore(t *testing.T, workspaceID string) *sqlite.SessionStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewSessionStore(d)
}

func TestSessionStore_SetAndLatestHandoff(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	h, err := s.SetHandoff(ctx, session.HandoffParams{
		Intent:         "continue dashboard refactor tomorrow",
		RepoName:       "wayneblacktea",
		ContextSummary: "stuck on TaskCard accordion expand",
	})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	if h.Intent != "continue dashboard refactor tomorrow" {
		t.Errorf("unexpected intent: %q", h.Intent)
	}
	if !h.RepoName.Valid || h.RepoName.String != "wayneblacktea" {
		t.Errorf("expected repo_name wayneblacktea, got %+v", h.RepoName)
	}
	if h.ResolvedAt.Valid {
		t.Errorf("freshly created handoff should not be resolved, got %+v", h.ResolvedAt)
	}
	if !h.CreatedAt.Valid {
		t.Errorf("expected created_at to be set")
	}

	latest, err := s.LatestHandoff(ctx)
	if err != nil {
		t.Fatalf("LatestHandoff: %v", err)
	}
	if latest.ID != h.ID {
		t.Errorf("expected latest = freshly created handoff, got id=%s want=%s", latest.ID, h.ID)
	}
}

func TestSessionStore_LatestHandoffReturnsNotFoundWhenEmpty(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	_, err := s.LatestHandoff(ctx)
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected session.ErrNotFound on empty table, got %v", err)
	}
}

func TestSessionStore_ResolveMakesHandoffInvisibleToLatest(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	h, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "do thing X"})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	if err := s.Resolve(ctx, h.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	_, err = s.LatestHandoff(ctx)
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected ErrNotFound after resolving the only handoff, got %v", err)
	}
}

func TestSessionStore_ResolveTwiceReturnsNotFound(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	h, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "do thing X"})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	if err := s.Resolve(ctx, h.ID); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if err := s.Resolve(ctx, h.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected second Resolve on already-resolved handoff to return ErrNotFound, got %v", err)
	}
}

func TestSessionStore_ResolveUnknownIDReturnsNotFound(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	if err := s.Resolve(ctx, uuid.New()); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown id, got %v", err)
	}
}

func TestSessionStore_LatestHandoffOrdersByCreatedAtDesc(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	first, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "first"})
	if err != nil {
		t.Fatalf("SetHandoff first: %v", err)
	}
	second, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "second"})
	if err != nil {
		t.Fatalf("SetHandoff second: %v", err)
	}

	latest, err := s.LatestHandoff(ctx)
	if err != nil {
		t.Fatalf("LatestHandoff: %v", err)
	}
	if latest.ID != second.ID {
		t.Errorf("expected latest = second handoff (id=%s), got id=%s (first id=%s)", second.ID, latest.ID, first.ID)
	}
}

func TestSessionStore_UpdateSummary_WritesToLatestHandoff(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	h, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "ship feature X"})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}

	const wantSummary = "Shipped feature X. Merged PR #42. Next: write tests."
	if err := s.UpdateSummary(ctx, wantSummary); err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}

	// Confirm the handoff is still visible and intent is intact after the update.
	latest, err := s.LatestHandoff(ctx)
	if err != nil {
		t.Fatalf("LatestHandoff after UpdateSummary: %v", err)
	}
	if latest.ID != h.ID {
		t.Errorf("expected same handoff id after UpdateSummary, got %s want %s", latest.ID, h.ID)
	}
	if latest.Intent != "ship feature X" {
		t.Errorf("intent changed after UpdateSummary: got %q", latest.Intent)
	}
}

func TestSessionStore_UpdateSummary_NoOpWhenNoHandoff(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	// No handoff exists — UpdateSummary must not return an error.
	if err := s.UpdateSummary(ctx, "nothing to update"); err != nil {
		t.Errorf("UpdateSummary with no handoff must not error, got %v", err)
	}
}

func TestSessionStore_UpdateSummary_NoOpAfterResolve(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	h, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "resolved work"})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	if err := s.Resolve(ctx, h.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// After resolving, UpdateSummary should affect 0 rows but return no error.
	if err := s.UpdateSummary(ctx, "too late"); err != nil {
		t.Errorf("UpdateSummary after resolve must not error, got %v", err)
	}
}

func TestSessionStore_WorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// Two stores, two workspaces, but they need to share the same DB to prove
	// scoping works. Open one DB and wrap two SessionStores around it via two
	// separate sqlite.Open calls would mean two DBs — instead, just verify at
	// the schema level by running both workspaces against in-memory DBs and
	// checking that wsA cannot see wsB's row by reusing the same backing DSN.
	dsn := "file:wbtest?mode=memory&cache=shared"

	dA, err := sqlite.Open(ctx, dsn, wsA)
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}
	t.Cleanup(func() { _ = dA.Close() })
	dB, err := sqlite.Open(ctx, dsn, wsB)
	if err != nil {
		t.Fatalf("Open B: %v", err)
	}
	t.Cleanup(func() { _ = dB.Close() })

	storeA := sqlite.NewSessionStore(dA)
	storeB := sqlite.NewSessionStore(dB)

	hA, err := storeA.SetHandoff(ctx, session.HandoffParams{Intent: "wsA work"})
	if err != nil {
		t.Fatalf("SetHandoff A: %v", err)
	}
	if !hA.WorkspaceID.Valid {
		t.Errorf("expected workspace_id to be persisted for wsA store")
	}

	if _, err := storeB.LatestHandoff(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("wsB should not see wsA's handoff, got err=%v", err)
	}

	got, err := storeA.LatestHandoff(ctx)
	if err != nil {
		t.Fatalf("storeA.LatestHandoff: %v", err)
	}
	if got.ID != hA.ID {
		t.Errorf("storeA should still see its own handoff: want id=%s got id=%s", hA.ID, got.ID)
	}
}

// --- UpdateEmbedding + SearchByCosine tests ---

func TestSessionStore_UpdateEmbedding_WritesBytes(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	if _, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "embed me"}); err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}

	p := localai.HashedEmbeddingProvider{}
	vec, _ := p.Embed("embed me")
	embBytes := localai.SerializeEmbedding(vec)

	if err := s.UpdateEmbedding(ctx, embBytes); err != nil {
		t.Fatalf("UpdateEmbedding: %v", err)
	}
}

func TestSessionStore_UpdateEmbedding_NoOpWhenNoHandoff(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	// No handoff — must not error.
	if err := s.UpdateEmbedding(ctx, []byte{0x01, 0x02}); err != nil {
		t.Errorf("UpdateEmbedding with no handoff must not error, got %v", err)
	}
}

func TestSessionStore_SearchByCosine_EmptyTableReturnsNil(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	p := localai.HashedEmbeddingProvider{}
	vec, _ := p.Embed("query")
	results, err := s.SearchByCosine(ctx, vec, 3)
	if err != nil {
		t.Fatalf("SearchByCosine on empty table: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSessionStore_SearchByCosine_NilVecReturnsNil(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	results, err := s.SearchByCosine(ctx, nil, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for nil queryEmbedding, got %v", results)
	}
}

func TestSessionStore_SearchByCosine_ZeroLimitReturnsNil(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	p := localai.HashedEmbeddingProvider{}
	vec, _ := p.Embed("query")
	results, err := s.SearchByCosine(ctx, vec, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for limit=0, got %v", results)
	}
}

func TestSessionStore_SearchByCosine_FindsHandoffWithEmbedding(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	// Create a resolved handoff with an embedding (SearchByCosine queries resolved_at IS NOT NULL).
	h, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "past session work"})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}

	p := localai.HashedEmbeddingProvider{}
	vec, _ := p.Embed("past session work")
	embBytes := localai.SerializeEmbedding(vec)
	if err := s.UpdateEmbedding(ctx, embBytes); err != nil {
		t.Fatalf("UpdateEmbedding: %v", err)
	}
	if err := s.Resolve(ctx, h.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	results, err := s.SearchByCosine(ctx, vec, 3)
	if err != nil {
		t.Fatalf("SearchByCosine: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected at least 1 result for handoff with matching embedding")
	}
}

func TestSessionStore_SearchByCosine_WorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	wsA := uuid.New().String()
	wsB := uuid.New().String()
	dsn := "file:wbtest_cosine?mode=memory&cache=shared"

	dA, err := sqlite.Open(ctx, dsn, wsA)
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}
	t.Cleanup(func() { _ = dA.Close() })
	dB, err := sqlite.Open(ctx, dsn, wsB)
	if err != nil {
		t.Fatalf("Open B: %v", err)
	}
	t.Cleanup(func() { _ = dB.Close() })

	storeA := sqlite.NewSessionStore(dA)
	storeB := sqlite.NewSessionStore(dB)

	// Create and resolve a handoff in wsA with an embedding.
	hA, err := storeA.SetHandoff(ctx, session.HandoffParams{Intent: "wsA only"})
	if err != nil {
		t.Fatalf("SetHandoff A: %v", err)
	}
	p := localai.HashedEmbeddingProvider{}
	vec, _ := p.Embed("wsA only")
	if err := storeA.UpdateEmbedding(ctx, localai.SerializeEmbedding(vec)); err != nil {
		t.Fatalf("UpdateEmbedding A: %v", err)
	}
	if err := storeA.Resolve(ctx, hA.ID); err != nil {
		t.Fatalf("Resolve A: %v", err)
	}

	// storeB should not see wsA's handoff.
	results, err := storeB.SearchByCosine(ctx, vec, 3)
	if err != nil {
		t.Fatalf("storeB.SearchByCosine: %v", err)
	}
	for _, r := range results {
		if r.ID == hA.ID {
			t.Errorf("storeB should not see wsA handoff id=%s", hA.ID)
		}
	}
}

// TestResolveHandoff_AlreadyResolved verifies that calling Resolve on an
// already-resolved handoff returns session.ErrNotFound on the second call.
// This exercises the AND resolved_at IS NULL predicate in the UPDATE query.
func TestResolveHandoff_AlreadyResolved(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	h, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "parity: double-resolve should fail"})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}

	// First resolve: must succeed.
	if err := s.Resolve(ctx, h.ID); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}

	// Second resolve on the same (now-resolved) row: must return ErrNotFound.
	if err := s.Resolve(ctx, h.ID); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("second Resolve on already-resolved handoff: want session.ErrNotFound, got %v", err)
	}
}

// TestHandoff_ContextCancel verifies that a cancelled context is propagated
// to all SessionStore operations (SetHandoff, LatestHandoff, Resolve).
func TestHandoff_ContextCancel(t *testing.T) {
	s := openSessionStore(t, "")

	// First create a handoff using a live context so we have an ID to resolve.
	liveCtx := context.Background()
	h, err := s.SetHandoff(liveCtx, session.HandoffParams{Intent: "will be cancelled"})
	if err != nil {
		t.Fatalf("SetHandoff with live ctx: %v", err)
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	t.Run("SetHandoff", func(t *testing.T) {
		_, err := s.SetHandoff(cancelledCtx, session.HandoffParams{Intent: "should fail"})
		if err == nil {
			t.Error("expected error from cancelled context, got nil")
		}
		// The error must wrap context.Canceled.
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled in error chain, got: %v", err)
		}
	})

	t.Run("LatestHandoff", func(t *testing.T) {
		_, err := s.LatestHandoff(cancelledCtx)
		if err == nil {
			t.Error("expected error from cancelled context, got nil")
		}
		// Either context.Canceled or a wrapped form of it.
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled in error chain, got: %v", err)
		}
	})

	t.Run("Resolve", func(t *testing.T) {
		err := s.Resolve(cancelledCtx, h.ID)
		if err == nil {
			t.Error("expected error from cancelled context, got nil")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled in error chain, got: %v", err)
		}
	})
}

// TestHandoff_CrossYearOrdering verifies that LatestHandoff returns the
// chronologically later handoff even when two rows span a year boundary
// (e.g. 2025-12-31 vs 2026-01-01). This guards against naive string-sort
// collation bugs in the ORDER BY created_at DESC clause.
func TestHandoff_CrossYearOrdering(t *testing.T) {
	ctx := context.Background()
	d, s := openSessionStoreWithDB(t, "")

	// Insert two handoffs directly with explicit timestamps spanning year boundary.
	// We bypass SetHandoff (which always uses nowRFC3339) to pin deterministic values.
	idA := uuid.New()
	idB := uuid.New()
	const q = `INSERT INTO session_handoffs (id, intent, created_at) VALUES (?, ?, ?)`
	if err := d.ExecContext(ctx, q, idA.String(), "old year handoff", "2025-12-31T23:59:59.000Z"); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := d.ExecContext(ctx, q, idB.String(), "new year handoff", "2026-01-01T00:00:00.000Z"); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	latest, err := s.LatestHandoff(ctx)
	if err != nil {
		t.Fatalf("LatestHandoff: %v", err)
	}
	if latest.ID != idB {
		t.Errorf("LatestHandoff should return the 2026-01-01 handoff (idB=%s), got id=%s (idA=%s)",
			idB, latest.ID, idA)
	}
	if latest.Intent != "new year handoff" {
		t.Errorf("unexpected intent: %q", latest.Intent)
	}
}

func TestSessionStore_HandoffsByRepo(t *testing.T) {
	s := openSessionStore(t, "")
	ctx := context.Background()

	// Two handoffs for the same repo, one for a different repo.
	if _, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "first wbt", RepoName: "wayneblacktea"}); err != nil {
		t.Fatalf("SetHandoff 1: %v", err)
	}
	if _, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "second wbt", RepoName: "wayneblacktea"}); err != nil {
		t.Fatalf("SetHandoff 2: %v", err)
	}
	if _, err := s.SetHandoff(ctx, session.HandoffParams{Intent: "for chatbot-go", RepoName: "chatbot-go"}); err != nil {
		t.Fatalf("SetHandoff 3: %v", err)
	}

	t.Run("returns only matching repo, newest first", func(t *testing.T) {
		got, err := s.HandoffsByRepo(ctx, "wayneblacktea", 50)
		if err != nil {
			t.Fatalf("HandoffsByRepo: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 wayneblacktea handoffs, got %d", len(got))
		}
		// Newest first → "second wbt" before "first wbt".
		if len(got) >= 1 && got[0].Intent != "second wbt" {
			t.Errorf("expected newest first, got %q", got[0].Intent)
		}
	})

	t.Run("limit caps result", func(t *testing.T) {
		got, err := s.HandoffsByRepo(ctx, "wayneblacktea", 1)
		if err != nil {
			t.Fatalf("HandoffsByRepo: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("expected 1 handoff, got %d", len(got))
		}
	})

	t.Run("non-matching repo → empty", func(t *testing.T) {
		got, err := s.HandoffsByRepo(ctx, "no-such-repo", 50)
		if err != nil {
			t.Fatalf("HandoffsByRepo: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 handoffs for unknown repo, got %d", len(got))
		}
	})
}
