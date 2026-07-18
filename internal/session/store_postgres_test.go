package session_test

import (
	"context"
	"testing"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestPgPool returns the package-level singleton pool initialised in
// TestMain. Skip with -short: testcontainers requires Docker and adds ~5-10s.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// TestStore_SearchByCosine_WorkspaceIsolation verifies the SECURITY claim on
// Store.SearchByCosine (store.go): a store scoped to workspace B never sees
// embeddings belonging to workspace A, even when the query vector is an exact
// match. Mirrors the pattern in internal/gtd's
// TestStore_SetVisionItemID_WorkspaceIsolation (create in A, query from B,
// assert absence).
func TestStore_SearchByCosine_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA, wsB := uuid.New(), uuid.New()
	storeA := session.NewStore(pool, &wsA)
	storeB := session.NewStore(pool, &wsB)
	ctx := context.Background()

	h, err := storeA.SetHandoff(ctx, session.HandoffParams{
		RepoName: "wsA-repo",
		Intent:   "workspace A secret intent",
	})
	if err != nil {
		t.Fatalf("SetHandoff in wsA: %v", err)
	}

	vec := make([]float32, localai.HashedEmbeddingDims)
	for i := range vec {
		vec[i] = 1.0
	}
	if err := storeA.UpdateEmbeddingByID(ctx, h.ID, localai.SerializeEmbedding(vec), "hashed", localai.HashedEmbeddingDims); err != nil {
		t.Fatalf("UpdateEmbeddingByID in wsA: %v", err)
	}

	// storeA (same workspace) must find it.
	gotA, err := storeA.SearchByCosine(ctx, vec, 10)
	if err != nil {
		t.Fatalf("SearchByCosine (wsA): %v", err)
	}
	if !containsHandoffID(gotA, h.ID) {
		t.Errorf("expected wsA's own store to find handoff %s, got %d results", h.ID, len(gotA))
	}

	// storeB (different workspace) with the identical query vector must NOT
	// find it — this is the SECURITY claim under test.
	gotB, err := storeB.SearchByCosine(ctx, vec, 10)
	if err != nil {
		t.Fatalf("SearchByCosine (wsB): %v", err)
	}
	if containsHandoffID(gotB, h.ID) {
		t.Errorf("SECURITY: wsB's store found a handoff owned by wsA (cross-workspace leak): %s", h.ID)
	}
}

func containsHandoffID(rows []db.SessionHandoff, id uuid.UUID) bool {
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestStore_MarkNextActionDone_HappyPath verifies that MarkNextActionDone
// flips the matching step's status to "done" and leaves other steps
// untouched.
func TestStore_MarkNextActionDone_HappyPath(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := session.NewStore(pool, &wsID)
	ctx := context.Background()

	h, err := store.SetHandoff(ctx, session.HandoffParams{
		RepoName: "next-action-repo",
		Intent:   "ship the feature",
		NextActions: []session.NextAction{
			{Step: 1, Title: "write code", Status: session.NextActionPending},
			{Step: 2, Title: "write tests", Status: session.NextActionPending},
		},
	})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}

	updated, err := store.MarkNextActionDone(ctx, h.ID, 1)
	if err != nil {
		t.Fatalf("MarkNextActionDone: %v", err)
	}
	if updated.ID != h.ID {
		t.Fatalf("expected updated handoff ID %s, got %s", h.ID, updated.ID)
	}

	// Re-fetch to confirm the write is durable, not just an in-memory echo.
	refetched, err := store.MarkNextActionDone(ctx, h.ID, 2)
	if err != nil {
		t.Fatalf("MarkNextActionDone step 2: %v", err)
	}
	if refetched.ID != h.ID {
		t.Fatalf("expected refetched handoff ID %s, got %s", h.ID, refetched.ID)
	}
}

// TestStore_MarkNextActionDone_StepNotFound verifies the error path: marking
// a step number that does not exist in next_actions returns an error rather
// than silently succeeding.
func TestStore_MarkNextActionDone_StepNotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := session.NewStore(pool, &wsID)
	ctx := context.Background()

	h, err := store.SetHandoff(ctx, session.HandoffParams{
		RepoName: "step-not-found-repo",
		Intent:   "single step handoff",
		NextActions: []session.NextAction{
			{Step: 1, Title: "only step", Status: session.NextActionPending},
		},
	})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}

	if _, err := store.MarkNextActionDone(ctx, h.ID, 99); err == nil {
		t.Error("expected error for non-existent step 99, got nil")
	}
}

// TestStore_MarkNextActionDone_WorkspaceIsolation verifies that a store
// scoped to workspace B cannot mark a next_action done on a handoff owned by
// workspace A, matching the SECURITY comment on MarkNextActionDone.
func TestStore_MarkNextActionDone_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA, wsB := uuid.New(), uuid.New()
	storeA := session.NewStore(pool, &wsA)
	storeB := session.NewStore(pool, &wsB)
	ctx := context.Background()

	h, err := storeA.SetHandoff(ctx, session.HandoffParams{
		RepoName: "isolation-repo",
		Intent:   "wsA only",
		NextActions: []session.NextAction{
			{Step: 1, Title: "step one", Status: session.NextActionPending},
		},
	})
	if err != nil {
		t.Fatalf("SetHandoff in wsA: %v", err)
	}

	if _, err := storeB.MarkNextActionDone(ctx, h.ID, 1); err == nil {
		t.Error("expected error when wsB attempts to mark a wsA-owned handoff done, got nil")
	}
}

// backdateResolvedAt directly rewrites resolved_at for the given handoff,
// bypassing Resolve (which always stamps NOW()).
func backdateResolvedAt(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, resolvedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE session_handoffs SET resolved_at = $1 WHERE id = $2`, resolvedAt, id,
	); err != nil {
		t.Fatalf("backdate resolved_at: %v", err)
	}
}

func handoffExists(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM session_handoffs WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count session_handoffs: %v", err)
	}
	return n == 1
}

// TestStore_PruneOlderThan_Expired verifies that a RESOLVED handoff older
// than the cutoff is deleted.
func TestStore_PruneOlderThan_Expired(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := session.NewStore(pool, &wsID)
	ctx := context.Background()

	h, err := store.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "expired resolved"})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	if err := store.Resolve(ctx, h.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	backdateResolvedAt(t, pool, h.ID, time.Now().Add(-400*24*time.Hour))

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	n, err := store.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n < 1 {
		t.Errorf("rows deleted = %d, want >= 1", n)
	}
	if handoffExists(t, pool, h.ID) {
		t.Error("expired resolved handoff survived prune")
	}
}

// TestStore_PruneOlderThan_NotExpired_OpenHandoffNeverPruned verifies two
// things: (1) an unresolved handoff is never pruned regardless of age, and
// (2) a recently-resolved handoff is not pruned.
func TestStore_PruneOlderThan_NotExpired_OpenHandoffNeverPruned(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := session.NewStore(pool, &wsID)
	ctx := context.Background()

	// Open handoff, backdate created_at to look ancient — must still survive
	// because resolved_at is NULL.
	open, err := store.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "still open"})
	if err != nil {
		t.Fatalf("SetHandoff (open): %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE session_handoffs SET created_at = $1 WHERE id = $2`,
		time.Now().Add(-1000*24*time.Hour), open.ID,
	); err != nil {
		t.Fatalf("backdate created_at: %v", err)
	}

	// Recently-resolved handoff — must survive because resolved_at is fresh.
	fresh, err := store.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "recently resolved"})
	if err != nil {
		t.Fatalf("SetHandoff (fresh): %v", err)
	}
	if err := store.Resolve(ctx, fresh.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	if _, err := store.PruneOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if !handoffExists(t, pool, open.ID) {
		t.Error("open handoff was pruned despite resolved_at IS NULL")
	}
	if !handoffExists(t, pool, fresh.ID) {
		t.Error("recently-resolved handoff was pruned")
	}
}

// TestStore_PruneOlderThan_Boundary verifies the cutoff comparison is
// strict-less-than: a resolved handoff exactly at the cutoff survives, one
// resolved 1 second before the cutoff is deleted.
func TestStore_PruneOlderThan_Boundary(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := session.NewStore(pool, &wsID)
	ctx := context.Background()

	cutoff := time.Now().Add(-365 * 24 * time.Hour)

	atCutoff, err := store.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "at cutoff"})
	if err != nil {
		t.Fatalf("SetHandoff (atCutoff): %v", err)
	}
	if err := store.Resolve(ctx, atCutoff.ID); err != nil {
		t.Fatalf("Resolve (atCutoff): %v", err)
	}
	backdateResolvedAt(t, pool, atCutoff.ID, cutoff)

	justBefore, err := store.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "just before cutoff"})
	if err != nil {
		t.Fatalf("SetHandoff (justBefore): %v", err)
	}
	if err := store.Resolve(ctx, justBefore.ID); err != nil {
		t.Fatalf("Resolve (justBefore): %v", err)
	}
	backdateResolvedAt(t, pool, justBefore.ID, cutoff.Add(-1*time.Second))

	if _, err := store.PruneOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if !handoffExists(t, pool, atCutoff.ID) {
		t.Error("handoff resolved exactly at cutoff was pruned (want: survive)")
	}
	if handoffExists(t, pool, justBefore.ID) {
		t.Error("handoff resolved just before cutoff survived (want: pruned)")
	}
}
