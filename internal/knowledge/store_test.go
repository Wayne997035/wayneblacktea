package knowledge_test

import (
	"context"
	"testing"

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
