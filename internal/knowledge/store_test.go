package knowledge_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// openSQLiteKnowledgeStore opens an in-memory SQLite knowledge store for unit
// tests. The store is automatically closed when the test ends.
func openSQLiteKnowledgeStore(t *testing.T) *sqlite.KnowledgeStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewKnowledgeStore(d)
}

// openSQLiteKnowledgeStoreWS opens a named in-memory SQLite knowledge store
// scoped to the given workspace UUID string.
func openSQLiteKnowledgeStoreWS(t *testing.T, dsn, workspaceID string) *sqlite.KnowledgeStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), dsn, workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewKnowledgeStore(d)
}

// TestListByProjectID_HappyPath verifies that an item tagged with a project_id
// is returned by ListByProjectID.
func TestListByProjectID_HappyPath(t *testing.T) {
	s := openSQLiteKnowledgeStore(t)
	ctx := context.Background()

	projectID := uuid.New()
	pBytes := [16]byte(projectID)

	item, err := s.AddItem(ctx, knowledge.AddItemParams{
		Type:      "til",
		Title:     "Project-linked knowledge",
		Content:   "some content",
		ProjectID: &pBytes,
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	results, err := s.ListByProjectID(ctx, projectID, 10)
	if err != nil {
		t.Fatalf("ListByProjectID: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 item, got %d", len(results))
	}
	if results[0].ID != item.ID {
		t.Errorf("want ID %s, got %s", item.ID, results[0].ID)
	}
}

// TestListByProjectID_EmptyResult verifies that querying a project_id with no
// matching items returns an empty (non-nil) slice.
func TestListByProjectID_EmptyResult(t *testing.T) {
	s := openSQLiteKnowledgeStore(t)
	ctx := context.Background()

	results, err := s.ListByProjectID(ctx, uuid.New(), 10)
	if err != nil {
		t.Fatalf("ListByProjectID: %v", err)
	}
	if results == nil {
		t.Error("want non-nil empty slice, got nil")
	}
	if len(results) != 0 {
		t.Errorf("want 0 items for unknown project_id, got %d", len(results))
	}
}

// TestListByProjectID_WorkspaceScoping verifies that items belonging to
// workspace A are not visible when queried from workspace B.
func TestListByProjectID_WorkspaceScoping(t *testing.T) {
	ctx := context.Background()
	wsA := uuid.New().String()
	wsB := uuid.New().String()
	projectID := uuid.New()
	pBytes := [16]byte(projectID)

	// Shared named in-memory DB so both stores see the same schema.
	dsn := "file:knowledge-ws-" + uuid.New().String() + "?mode=memory&cache=shared"
	storeA := openSQLiteKnowledgeStoreWS(t, dsn, wsA)
	storeB := openSQLiteKnowledgeStoreWS(t, dsn, wsB)

	_, err := storeA.AddItem(ctx, knowledge.AddItemParams{
		Type:      "til",
		Title:     "Workspace A only",
		Content:   "secret",
		ProjectID: &pBytes,
	})
	if err != nil {
		t.Fatalf("AddItem in wsA: %v", err)
	}

	rows, err := storeB.ListByProjectID(ctx, projectID, 10)
	if err != nil {
		t.Fatalf("ListByProjectID from wsB: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("workspace B must not see workspace A items, got %d rows", len(rows))
	}
}

// TestListByTaskID_HappyPath verifies that an item tagged with a task_id is
// returned by ListByTaskID.
func TestListByTaskID_HappyPath(t *testing.T) {
	s := openSQLiteKnowledgeStore(t)
	ctx := context.Background()

	taskID := uuid.New()
	tBytes := [16]byte(taskID)

	item, err := s.AddItem(ctx, knowledge.AddItemParams{
		Type:    "article",
		Title:   "Task-linked knowledge",
		Content: "related to task",
		TaskID:  &tBytes,
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	results, err := s.ListByTaskID(ctx, taskID, 10)
	if err != nil {
		t.Fatalf("ListByTaskID: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 item, got %d", len(results))
	}
	if results[0].ID != item.ID {
		t.Errorf("want ID %s, got %s", item.ID, results[0].ID)
	}
}

// TestListByTaskID_EmptyResult verifies that querying a task_id with no
// matching items returns an empty (non-nil) slice.
func TestListByTaskID_EmptyResult(t *testing.T) {
	s := openSQLiteKnowledgeStore(t)
	ctx := context.Background()

	results, err := s.ListByTaskID(ctx, uuid.New(), 10)
	if err != nil {
		t.Fatalf("ListByTaskID: %v", err)
	}
	if results == nil {
		t.Error("want non-nil empty slice, got nil")
	}
	if len(results) != 0 {
		t.Errorf("want 0 items for unknown task_id, got %d", len(results))
	}
}

// TestListByTaskID_WorkspaceScoping verifies that items belonging to workspace
// A are not visible when queried from workspace B via task_id filter.
func TestListByTaskID_WorkspaceScoping(t *testing.T) {
	ctx := context.Background()
	wsA := uuid.New().String()
	wsB := uuid.New().String()
	taskID := uuid.New()
	tBytes := [16]byte(taskID)

	dsn := "file:knowledge-task-ws-" + uuid.New().String() + "?mode=memory&cache=shared"
	storeA := openSQLiteKnowledgeStoreWS(t, dsn, wsA)
	storeB := openSQLiteKnowledgeStoreWS(t, dsn, wsB)

	_, err := storeA.AddItem(ctx, knowledge.AddItemParams{
		Type:    "til",
		Title:   "Task item in wsA",
		Content: "private",
		TaskID:  &tBytes,
	})
	if err != nil {
		t.Fatalf("AddItem in wsA: %v", err)
	}

	rows, err := storeB.ListByTaskID(ctx, taskID, 10)
	if err != nil {
		t.Fatalf("ListByTaskID from wsB: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("workspace B must not see workspace A items, got %d rows", len(rows))
	}
}

// --- F0929-74 throttle tests -------------------------------------------------
//
// searchCore's FTS query runs against knowledge_items regardless of the embed
// throttle, so these need a real, migrated Postgres — openKnowledgePgPool,
// the singleton pool set up by store_postgres_test.go's TestMain, is reused
// here rather than starting a second container. Direct manipulation of the
// process-global budget goes through the ForTest hooks exported by store.go
// (this ticket's other output-path) — same reason internal/lifecycle uses an
// export_test.go-style hook (SetPortProbeTimeoutForTest): the budget/refill
// function are unexported by design, and this file can't be package
// knowledge (white-box) without an import cycle, since it also needs
// internal/storage/sqlite for the tests above, and sqlite imports knowledge.

// countingEmbedder is a minimal ai.ContextEmbeddingProvider stub that counts
// calls and always returns a fixed 768-dim vector (matches the knowledge_items
// embedding column, migrations/000005_knowledge.up.sql:10) — same shape as
// fixedVecEmbedder (store_prepare_test.go) plus a thread-safe call counter,
// needed here to prove exactly how many times embed was invoked across a
// budget-draining loop / concurrent burst.
type countingEmbedder struct {
	mu    sync.Mutex
	calls int
}

func (c *countingEmbedder) Embed(context.Context, string) ([]float32, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return make([]float32, 768), nil
}

func (c *countingEmbedder) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

var _ ai.ContextEmbeddingProvider = (*countingEmbedder)(nil)

// resetContextPackEmbedBudget puts the process-global embed budget into a
// known, freshly-refilled state so each throttle test starts deterministic
// regardless of test execution order within this binary.
func resetContextPackEmbedBudget() {
	knowledge.ResetContextPackEmbedBudgetForTest(
		knowledge.ContextPackEmbedMaxPerWindowForTest,
		time.Now().Add(knowledge.ContextPackEmbedWindowForTest),
	)
}

// TestSearchReadOnly_BudgetExhausted_ReturnsFTSWithoutError is the required
// named test for F0929-74 (per dispatch record): the 61st SearchReadOnly
// call within the same window must NOT invoke the embed client, and the
// call itself must still return successfully (no error) with FTS-only
// results — never a hard failure. Also covers acceptance row 3 (short
// queries don't consume a token): a batch of <=3-word calls runs first and
// must not eat into the budget the 60 long-query calls below need.
func TestSearchReadOnly_BudgetExhausted_ReturnsFTSWithoutError(t *testing.T) {
	// Not parallel: drains the global contextPackEmbedBudget; a parallel
	// sibling test would interfere.
	pool := openKnowledgePgPool(t)
	resetContextPackEmbedBudget()

	wsID := uuid.New()
	embed := &countingEmbedder{}
	store := knowledge.NewStore(pool, embed, &wsID)
	ctx := context.Background()

	// Row 3: short (<=3-word) queries must short-circuit before the
	// throttle check ever runs, so they must not decrement the budget.
	for i := 0; i < 5; i++ {
		if _, err := store.SearchReadOnly(ctx, "short query", 10); err != nil {
			t.Fatalf("SearchReadOnly (short query, iteration %d): %v", i, err)
		}
	}
	if got := embed.callCount(); got != 0 {
		t.Fatalf("short-query calls must never invoke embed, got %d calls", got)
	}

	// Drain the full budget with >3-word queries — every one of these must
	// still succeed and invoke embed, proving the short queries above did
	// not consume any tokens.
	for i := 0; i < knowledge.ContextPackEmbedMaxPerWindowForTest; i++ {
		if _, err := store.SearchReadOnly(ctx, "a sufficiently long query string", 10); err != nil {
			t.Fatalf("SearchReadOnly (long query, iteration %d): %v", i, err)
		}
	}
	if got := embed.callCount(); got != knowledge.ContextPackEmbedMaxPerWindowForTest {
		t.Fatalf("after %d long-query calls, embed call count = %d, want %d (short queries must not have consumed budget)",
			knowledge.ContextPackEmbedMaxPerWindowForTest, got, knowledge.ContextPackEmbedMaxPerWindowForTest)
	}

	// The 61st long-query call: budget exhausted, must degrade silently —
	// no error, embed not invoked again. (No knowledge_items were seeded to
	// match this query, so an empty/nil result is the correct FTS-only
	// outcome here; the point under test is the absence of an error and the
	// frozen embed call count, not the item count.)
	if _, err := store.SearchReadOnly(ctx, "a sufficiently long query string", 10); err != nil {
		t.Fatalf("61st SearchReadOnly call returned an error, want silent degrade: %v", err)
	}
	if got := embed.callCount(); got != knowledge.ContextPackEmbedMaxPerWindowForTest {
		t.Fatalf("61st call: embed call count = %d, want unchanged at %d (budget must block the call)",
			got, knowledge.ContextPackEmbedMaxPerWindowForTest)
	}
}

// TestSearch_NotThrottled_IgnoresExhaustedBudget covers acceptance row 2:
// Store.Search (throttleEmbed=false, used by search_knowledge /
// tools_procedural.go) must keep calling embed on every invocation
// regardless of SearchReadOnly having exhausted the shared budget — the
// throttle must not leak onto the wrong caller.
func TestSearch_NotThrottled_IgnoresExhaustedBudget(t *testing.T) {
	pool := openKnowledgePgPool(t)
	// Simulate an exhausted budget (as if SearchReadOnly had drained it).
	knowledge.ResetContextPackEmbedBudgetForTest(0, time.Now().Add(knowledge.ContextPackEmbedWindowForTest))

	wsID := uuid.New()
	embed := &countingEmbedder{}
	store := knowledge.NewStore(pool, embed, &wsID)
	ctx := context.Background()

	const n = 100
	for i := 0; i < n; i++ {
		if _, err := store.Search(ctx, "a sufficiently long query string", 10); err != nil {
			t.Fatalf("Search (iteration %d): %v", i, err)
		}
	}
	if got := embed.callCount(); got != n {
		t.Fatalf("Search embed call count = %d, want %d (Search must never be throttled by the SearchReadOnly-only budget)", got, n)
	}
}

// TestSearchReadOnly_EmbedBudget_Concurrent covers acceptance row 5:
// 100 goroutines racing on a freshly-reset budget must yield at most
// contextPackEmbedMaxPerWindow successful embed calls, with no data race
// (run under -race).
func TestSearchReadOnly_EmbedBudget_Concurrent(t *testing.T) {
	pool := openKnowledgePgPool(t)
	resetContextPackEmbedBudget()

	wsID := uuid.New()
	embed := &countingEmbedder{}
	store := knowledge.NewStore(pool, embed, &wsID)
	ctx := context.Background()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, err := store.SearchReadOnly(ctx, "a sufficiently long query string", 10); err != nil {
				t.Errorf("SearchReadOnly: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := embed.callCount(); got > knowledge.ContextPackEmbedMaxPerWindowForTest {
		t.Fatalf("concurrent embed call count = %d, want at most %d", got, knowledge.ContextPackEmbedMaxPerWindowForTest)
	}
}

// TestTryAcquireContextPackEmbedToken_DrainsAndRefills is the pure
// package-internal boundary test for F0929-74, requiring no DB: same shape
// as TestTryAcquireClassifyToken_DrainsAndRefills
// (internal/mcp/middleware_classify_test.go), asserting the refill boundary
// is strictly now.After(resetAt) — matching that sibling bit-for-bit.
func TestTryAcquireContextPackEmbedToken_DrainsAndRefills(t *testing.T) {
	// Not parallel: drains the global contextPackEmbedBudget.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	knowledge.ResetContextPackEmbedBudgetForTest(0, t0.Add(-time.Second)) // already expired

	// First acquire at t0 must refill (resetAt was in the past) and succeed.
	if !knowledge.TryAcquireContextPackEmbedTokenForTest(t0) {
		t.Fatal("first call after expiry should refill and succeed")
	}

	// Drain the remaining ContextPackEmbedMaxPerWindowForTest-1 tokens
	// within the same window.
	for i := 1; i < knowledge.ContextPackEmbedMaxPerWindowForTest; i++ {
		if !knowledge.TryAcquireContextPackEmbedTokenForTest(t0) {
			t.Fatalf("call %d within window should succeed", i+1)
		}
	}

	// Bucket is now empty within the same window — must reject.
	if knowledge.TryAcquireContextPackEmbedTokenForTest(t0) {
		t.Error("call after draining bucket within window should be rejected")
	}

	// Advance past the window — next call must refill.
	tNext := t0.Add(knowledge.ContextPackEmbedWindowForTest + time.Second)
	if !knowledge.TryAcquireContextPackEmbedTokenForTest(tNext) {
		t.Error("call after window expiry should refill and succeed")
	}
}
