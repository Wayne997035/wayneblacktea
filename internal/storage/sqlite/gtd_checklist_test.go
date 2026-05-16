package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestGTDStore_Checklist_AddItem verifies that AddChecklistItem appends a new item
// to the task's checklist and returns the updated slice.
func TestGTDStore_Checklist_AddItem(t *testing.T) {
	wsID := "11111111-1111-4111-8111-111111111111"
	s := openMem(t, wsID)
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "checklist task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	wsUUID := uuid.MustParse(wsID)
	item := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     "Step one",
		FileRef:   "internal/gtd/gtd.go",
		Notes:     "initial note",
		CreatedAt: time.Now().UTC(),
	}
	items, err := s.AddChecklistItem(ctx, task.ID, wsUUID, item)
	if err != nil {
		t.Fatalf("AddChecklistItem: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ID != item.ID {
		t.Errorf("item ID mismatch: got %s, want %s", items[0].ID, item.ID)
	}
	if items[0].Title != "Step one" {
		t.Errorf("unexpected title: %q", items[0].Title)
	}
	if items[0].Done {
		t.Errorf("new item should not be done")
	}
	if items[0].CompletedAt != nil {
		t.Errorf("new item should not have CompletedAt set")
	}
}

// TestGTDStore_Checklist_AddItem_TaskNotFound ensures ErrNotFound is returned
// when the task does not exist in the workspace.
func TestGTDStore_Checklist_AddItem_TaskNotFound(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	item := gtd.ChecklistItem{ID: uuid.New(), Title: "ghost", CreatedAt: time.Now().UTC()}
	_, err := s.AddChecklistItem(ctx, uuid.New(), uuid.UUID{}, item)
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestGTDStore_Checklist_Toggle verifies UpdateChecklistItem marks an item done
// and sets CompletedAt, then can be unmarked (done=false clears CompletedAt).
func TestGTDStore_Checklist_Toggle(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, _ := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "toggle task", Priority: 3})
	item := gtd.ChecklistItem{ID: uuid.New(), Title: "toggle me", CreatedAt: time.Now().UTC()}
	items, err := s.AddChecklistItem(ctx, task.ID, uuid.UUID{}, item)
	if err != nil || len(items) == 0 {
		t.Fatalf("AddChecklistItem: %v", err)
	}

	// Mark done=true.
	done := true
	evidenceURL := "https://example.com/pr/42"
	updated, err := s.UpdateChecklistItem(ctx, task.ID, uuid.UUID{}, items[0].ID, gtd.UpdateChecklistItemParams{
		Done:        &done,
		EvidenceURL: &evidenceURL,
	})
	if err != nil {
		t.Fatalf("UpdateChecklistItem (done=true): %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("expected 1 item after update, got %d", len(updated))
	}
	if !updated[0].Done {
		t.Error("expected Done=true after toggle")
	}
	if updated[0].CompletedAt == nil {
		t.Error("expected CompletedAt to be set when Done=true")
	}
	if updated[0].EvidenceURL != evidenceURL {
		t.Errorf("unexpected EvidenceURL: got %q, want %q", updated[0].EvidenceURL, evidenceURL)
	}

	// Mark done=false — CompletedAt should be cleared.
	notDone := false
	updated2, err := s.UpdateChecklistItem(ctx, task.ID, uuid.UUID{}, items[0].ID, gtd.UpdateChecklistItemParams{
		Done: &notDone,
	})
	if err != nil {
		t.Fatalf("UpdateChecklistItem (done=false): %v", err)
	}
	if updated2[0].Done {
		t.Error("expected Done=false after untoggle")
	}
	if updated2[0].CompletedAt != nil {
		t.Error("expected CompletedAt to be cleared when Done=false")
	}
}

// TestGTDStore_Checklist_UpdateItem_ItemNotFound verifies ErrNotFound for unknown itemID.
func TestGTDStore_Checklist_UpdateItem_ItemNotFound(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, _ := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "update miss", Priority: 3})
	done := true
	_, err := s.UpdateChecklistItem(ctx, task.ID, uuid.UUID{}, uuid.New(), gtd.UpdateChecklistItemParams{Done: &done})
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown itemID, got %v", err)
	}
}

// TestGTDStore_Checklist_Delete verifies that DeleteChecklistItem removes the item.
func TestGTDStore_Checklist_Delete(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, _ := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "delete task", Priority: 3})
	item1 := gtd.ChecklistItem{ID: uuid.New(), Title: "keep me", CreatedAt: time.Now().UTC()}
	item2 := gtd.ChecklistItem{ID: uuid.New(), Title: "delete me", CreatedAt: time.Now().UTC()}

	_, _ = s.AddChecklistItem(ctx, task.ID, uuid.UUID{}, item1)
	items, err := s.AddChecklistItem(ctx, task.ID, uuid.UUID{}, item2)
	if err != nil || len(items) != 2 {
		t.Fatalf("setup: expected 2 items, got %v, err=%v", len(items), err)
	}

	if err := s.DeleteChecklistItem(ctx, task.ID, uuid.UUID{}, item2.ID); err != nil {
		t.Fatalf("DeleteChecklistItem: %v", err)
	}

	// Verify by adding a no-op item and reading back — we verify item2 is gone.
	dummy := gtd.ChecklistItem{ID: uuid.New(), Title: "verify", CreatedAt: time.Now().UTC()}
	remaining, err := s.AddChecklistItem(ctx, task.ID, uuid.UUID{}, dummy)
	if err != nil {
		t.Fatalf("post-delete AddChecklistItem: %v", err)
	}
	for _, it := range remaining {
		if it.ID == item2.ID {
			t.Errorf("item2 should have been deleted, still found: %+v", it)
		}
	}
	// item1 and dummy must be present.
	var found1 bool
	for _, it := range remaining {
		if it.ID == item1.ID {
			found1 = true
		}
	}
	if !found1 {
		t.Error("item1 should still be present after deleting item2")
	}
}

// TestGTDStore_Checklist_Delete_ItemNotFound verifies ErrNotFound for unknown itemID.
func TestGTDStore_Checklist_Delete_ItemNotFound(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, _ := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "del miss", Priority: 3})
	err := s.DeleteChecklistItem(ctx, task.ID, uuid.UUID{}, uuid.New())
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown itemID, got %v", err)
	}
}

// TestGTDStore_Checklist_Lifecycle exercises the full add → toggle → delete flow.
func TestGTDStore_Checklist_Lifecycle(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "lifecycle task", Priority: 2})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Step 1: add item.
	item := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     "lifecycle step",
		Notes:     "initial",
		CreatedAt: time.Now().UTC(),
	}
	items, err := s.AddChecklistItem(ctx, task.ID, uuid.UUID{}, item)
	if err != nil || len(items) != 1 {
		t.Fatalf("AddChecklistItem: len=%d err=%v", len(items), err)
	}

	// Step 2: update title.
	newTitle := "updated step"
	items2, err := s.UpdateChecklistItem(ctx, task.ID, uuid.UUID{}, item.ID, gtd.UpdateChecklistItemParams{
		Title: &newTitle,
	})
	if err != nil {
		t.Fatalf("UpdateChecklistItem (title): %v", err)
	}
	if items2[0].Title != newTitle {
		t.Errorf("expected title %q, got %q", newTitle, items2[0].Title)
	}

	// Step 3: mark done.
	done := true
	items3, err := s.UpdateChecklistItem(ctx, task.ID, uuid.UUID{}, item.ID, gtd.UpdateChecklistItemParams{
		Done: &done,
	})
	if err != nil || !items3[0].Done {
		t.Fatalf("UpdateChecklistItem (done=true): err=%v done=%v", err, items3[0].Done)
	}

	// Step 4: delete.
	if err := s.DeleteChecklistItem(ctx, task.ID, uuid.UUID{}, item.ID); err != nil {
		t.Fatalf("DeleteChecklistItem: %v", err)
	}

	// Final: checklist is empty (verify by adding a sentinel and checking slice length).
	sentinel := gtd.ChecklistItem{ID: uuid.New(), Title: "sentinel", CreatedAt: time.Now().UTC()}
	final, err := s.AddChecklistItem(ctx, task.ID, uuid.UUID{}, sentinel)
	if err != nil {
		t.Fatalf("final AddChecklistItem: %v", err)
	}
	if len(final) != 1 {
		t.Errorf("expected 1 item after lifecycle delete, got %d", len(final))
	}
	if final[0].ID != sentinel.ID {
		t.Errorf("expected only sentinel item, got %+v", final)
	}
}

// TestGTDStore_Checklist_SanitisesNullBytes verifies null bytes in title/notes are stripped.
func TestGTDStore_Checklist_SanitisesNullBytes(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, _ := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "sanitise task", Priority: 3})

	rawTitle := "hello\x00world"
	sanitised := gtd.SanitiseChecklistText(rawTitle)
	if strings.Contains(sanitised, "\x00") {
		t.Fatalf("SanitiseChecklistText should strip null bytes, got %q", sanitised)
	}

	item := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     sanitised,
		CreatedAt: time.Now().UTC(),
	}
	items, err := s.AddChecklistItem(ctx, task.ID, uuid.UUID{}, item)
	if err != nil {
		t.Fatalf("AddChecklistItem with sanitised title: %v", err)
	}
	if strings.Contains(items[0].Title, "\x00") {
		t.Errorf("stored title contains null byte: %q", items[0].Title)
	}
}
