package gtd_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestPGStore_Checklist_AddItem verifies that AddChecklistItem appends a new item
// to the task's checklist and returns the full updated slice on Postgres.
func TestPGStore_Checklist_AddItem(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg checklist task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	item := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     "PG step one",
		FileRef:   "internal/gtd/store.go",
		Notes:     "initial pg note",
		CreatedAt: time.Now().UTC(),
	}
	items, err := store.AddChecklistItem(ctx, task.ID, wsID, item)
	if err != nil {
		t.Fatalf("AddChecklistItem: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ID != item.ID {
		t.Errorf("item ID mismatch: got %s, want %s", items[0].ID, item.ID)
	}
	if items[0].Title != "PG step one" {
		t.Errorf("unexpected title: %q", items[0].Title)
	}
	if items[0].Done {
		t.Errorf("new item should not be done")
	}
	if items[0].CompletedAt != nil {
		t.Errorf("new item should not have CompletedAt set")
	}
}

// TestPGStore_Checklist_AddItem_TaskNotFound verifies ErrNotFound for unknown taskID.
func TestPGStore_Checklist_AddItem_TaskNotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	item := gtd.ChecklistItem{ID: uuid.New(), Title: "ghost", CreatedAt: time.Now().UTC()}
	_, err := store.AddChecklistItem(ctx, uuid.New(), wsID, item)
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestPGStore_Checklist_Toggle verifies toggling a checklist item done/undone on Postgres.
func TestPGStore_Checklist_Toggle(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg toggle task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	item := gtd.ChecklistItem{ID: uuid.New(), Title: "toggle me", CreatedAt: time.Now().UTC()}
	items, err := store.AddChecklistItem(ctx, task.ID, wsID, item)
	if err != nil || len(items) == 0 {
		t.Fatalf("AddChecklistItem: %v", err)
	}

	// Mark done=true.
	done := true
	evidenceURL := "https://example.com/pr/99"
	updated, err := store.UpdateChecklistItem(ctx, task.ID, wsID, items[0].ID, gtd.UpdateChecklistItemParams{
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

	// Mark done=false.
	notDone := false
	updated2, err := store.UpdateChecklistItem(ctx, task.ID, wsID, items[0].ID, gtd.UpdateChecklistItemParams{
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

// TestPGStore_Checklist_UpdateItem_ItemNotFound verifies ErrNotFound for unknown itemID.
func TestPGStore_Checklist_UpdateItem_ItemNotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg update miss", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	done := true
	_, err = store.UpdateChecklistItem(ctx, task.ID, wsID, uuid.New(), gtd.UpdateChecklistItemParams{Done: &done})
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown itemID, got %v", err)
	}
}

// TestPGStore_Checklist_Delete verifies that DeleteChecklistItem removes the item on Postgres.
func TestPGStore_Checklist_Delete(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg delete task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	item1 := gtd.ChecklistItem{ID: uuid.New(), Title: "keep me", CreatedAt: time.Now().UTC()}
	item2 := gtd.ChecklistItem{ID: uuid.New(), Title: "delete me", CreatedAt: time.Now().UTC()}

	if _, err := store.AddChecklistItem(ctx, task.ID, wsID, item1); err != nil {
		t.Fatalf("AddChecklistItem item1: %v", err)
	}
	items, err := store.AddChecklistItem(ctx, task.ID, wsID, item2)
	if err != nil || len(items) != 2 {
		t.Fatalf("setup: expected 2 items, got %d, err=%v", len(items), err)
	}

	if err := store.DeleteChecklistItem(ctx, task.ID, wsID, item2.ID); err != nil {
		t.Fatalf("DeleteChecklistItem: %v", err)
	}

	// Verify by reading back via AddChecklistItem with a dummy item.
	dummy := gtd.ChecklistItem{ID: uuid.New(), Title: "verify", CreatedAt: time.Now().UTC()}
	remaining, err := store.AddChecklistItem(ctx, task.ID, wsID, dummy)
	if err != nil {
		t.Fatalf("post-delete AddChecklistItem: %v", err)
	}
	var found1, found2 bool
	for _, it := range remaining {
		if it.ID == item2.ID {
			found2 = true
		}
		if it.ID == item1.ID {
			found1 = true
		}
	}
	if found2 {
		t.Errorf("item2 should have been deleted, still found in remaining list")
	}
	if !found1 {
		t.Errorf("item1 ('keep me') should still be present after deleting item2")
	}
}

// TestPGStore_Checklist_ConcurrentAdd verifies that two concurrent AddChecklistItem
// calls for the same task both survive — no lost update due to the race condition
// that existed before the FOR UPDATE transaction wrapper was added.
func TestPGStore_Checklist_ConcurrentAdd(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "concurrent add task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	itemA := gtd.ChecklistItem{ID: uuid.New(), Title: "concurrent A", CreatedAt: time.Now().UTC()}
	itemB := gtd.ChecklistItem{ID: uuid.New(), Title: "concurrent B", CreatedAt: time.Now().UTC()}

	var wg sync.WaitGroup
	errs := make([]error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errs[0] = store.AddChecklistItem(ctx, task.ID, wsID, itemA)
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = store.AddChecklistItem(ctx, task.ID, wsID, itemB)
	}()
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Errorf("goroutine %d: AddChecklistItem error: %v", i, e)
		}
	}

	// Both items must be present — a lost-update bug would leave only one.
	dummy := gtd.ChecklistItem{ID: uuid.New(), Title: "read-sentinel", CreatedAt: time.Now().UTC()}
	final, err := store.AddChecklistItem(ctx, task.ID, wsID, dummy)
	if err != nil {
		t.Fatalf("final AddChecklistItem: %v", err)
	}
	var foundA, foundB bool
	for _, it := range final {
		if it.ID == itemA.ID {
			foundA = true
		}
		if it.ID == itemB.ID {
			foundB = true
		}
	}
	if !foundA {
		t.Error("itemA was lost — concurrent write race not fixed")
	}
	if !foundB {
		t.Error("itemB was lost — concurrent write race not fixed")
	}
}

// TestPGStore_Checklist_Delete_ItemNotFound verifies ErrNotFound for unknown itemID.
func TestPGStore_Checklist_Delete_ItemNotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg del miss", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	err = store.DeleteChecklistItem(ctx, task.ID, wsID, uuid.New())
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown itemID, got %v", err)
	}
}

// TestPGStore_Checklist_Lifecycle exercises add → toggle done → delete on Postgres.
func TestPGStore_Checklist_Lifecycle(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg lifecycle task", Priority: 2})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Step 1: add item.
	item := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     "pg lifecycle step",
		Notes:     "initial note",
		CreatedAt: time.Now().UTC(),
	}
	items, err := store.AddChecklistItem(ctx, task.ID, wsID, item)
	if err != nil || len(items) != 1 {
		t.Fatalf("AddChecklistItem: len=%d err=%v", len(items), err)
	}

	// Step 2: update title.
	newTitle := "pg updated step"
	items2, err := store.UpdateChecklistItem(ctx, task.ID, wsID, item.ID, gtd.UpdateChecklistItemParams{
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
	items3, err := store.UpdateChecklistItem(ctx, task.ID, wsID, item.ID, gtd.UpdateChecklistItemParams{
		Done: &done,
	})
	if err != nil || !items3[0].Done {
		t.Fatalf("UpdateChecklistItem (done=true): err=%v done=%v", err, items3[0].Done)
	}
	if items3[0].CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}

	// Step 4: delete.
	if err := store.DeleteChecklistItem(ctx, task.ID, wsID, item.ID); err != nil {
		t.Fatalf("DeleteChecklistItem: %v", err)
	}

	// Final: checklist is empty — verify by reading with a sentinel add.
	sentinel := gtd.ChecklistItem{ID: uuid.New(), Title: "sentinel", CreatedAt: time.Now().UTC()}
	final, err := store.AddChecklistItem(ctx, task.ID, wsID, sentinel)
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
