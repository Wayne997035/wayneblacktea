package sqlite_test

import (
	"context"
	"testing"
	"time"

	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/google/uuid"
)

func openDisciplineEventsDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteDisciplineEventM8Store_Insert(t *testing.T) {
	db := openDisciplineEventsDB(t)
	store := wbtsqlite.NewDisciplineEventM8Store(db)
	ctx := context.Background()

	wsID := uuid.New()
	err := store.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: &wsID,
		EventType:   watchdog.EventTypeStuckTask,
		Severity:    watchdog.SeverityWarn,
		Detail:      []byte(`{"task_id":"test"}`),
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	events, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event after Insert, got none")
	}

	found := false
	for _, ev := range events {
		if ev.EventType == watchdog.EventTypeStuckTask {
			found = true
			break
		}
	}
	if !found {
		t.Error("inserted event type not found in ListUnresolved")
	}
}

func TestSQLiteDisciplineEventM8Store_ListUnresolved(t *testing.T) {
	db := openDisciplineEventsDB(t)
	store := wbtsqlite.NewDisciplineEventM8Store(db)
	ctx := context.Background()

	wsA := uuid.New()
	wsB := uuid.New()

	// Insert one event in wsA and one in wsB.
	if err := store.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: &wsA,
		EventType:   watchdog.EventTypeUnloggedDecision,
		Severity:    watchdog.SeverityWarn,
		Detail:      []byte(`{}`),
	}); err != nil {
		t.Fatalf("Insert wsA: %v", err)
	}
	if err := store.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: &wsB,
		EventType:   watchdog.EventTypeProposalFail,
		Severity:    watchdog.SeverityInfo,
		Detail:      []byte(`{}`),
	}); err != nil {
		t.Fatalf("Insert wsB: %v", err)
	}

	// ListUnresolved scoped to wsA must not return wsB event.
	events, err := store.ListUnresolved(ctx, &wsA)
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	for _, ev := range events {
		if ev.WorkspaceID != nil && *ev.WorkspaceID == wsB {
			t.Errorf("event from workspace B leaked into scoped ListUnresolved for A")
		}
	}
	if len(events) == 0 {
		t.Error("expected at least one event for wsA")
	}
}

func TestSQLiteDisciplineEventM8Store_MarkResolved(t *testing.T) {
	db := openDisciplineEventsDB(t)
	store := wbtsqlite.NewDisciplineEventM8Store(db)
	ctx := context.Background()

	wsID := uuid.New()
	if err := store.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: &wsID,
		EventType:   watchdog.EventTypeStaleHandoff,
		Severity:    watchdog.SeverityWarn,
		Detail:      []byte(`{}`),
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	events, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected event after Insert")
	}
	eventID := events[0].ID

	// Resolve the event.
	if err := store.MarkResolved(ctx, eventID); err != nil {
		t.Fatalf("MarkResolved: %v", err)
	}

	// Event must not appear in ListUnresolved.
	after, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved after resolve: %v", err)
	}
	for _, ev := range after {
		if ev.ID == eventID {
			t.Errorf("resolved event %v still appears in ListUnresolved", eventID)
		}
	}
}

func TestSQLiteDisciplineEventM8Store_MarkResolved_NotFound(t *testing.T) {
	db := openDisciplineEventsDB(t)
	store := wbtsqlite.NewDisciplineEventM8Store(db)
	ctx := context.Background()

	err := store.MarkResolved(ctx, uuid.New())
	if err != watchdog.ErrEventNotFound {
		t.Errorf("expected ErrEventNotFound for non-existent ID, got: %v", err)
	}
}

func TestSQLiteDisciplineEventM8Store_PruneOlderThan(t *testing.T) {
	db := openDisciplineEventsDB(t)
	store := wbtsqlite.NewDisciplineEventM8Store(db)
	ctx := context.Background()

	wsID := uuid.New()

	// Insert two events.
	for i := 0; i < 2; i++ {
		if err := store.Insert(ctx, watchdog.InsertParams{
			WorkspaceID: &wsID,
			EventType:   watchdog.EventTypeRepeatedCorrection,
			Severity:    watchdog.SeverityInfo,
			Detail:      []byte(`{}`),
		}); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}

	// Prune with a future cutoff — all just-inserted rows qualify.
	n, err := store.PruneOlderThan(ctx, time.Now().Add(1*time.Minute))
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n < 2 {
		t.Errorf("expected at least 2 rows deleted, got %d", n)
	}

	// Workspace must now be empty.
	remaining, err := store.ListUnresolved(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListUnresolved after prune: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("expected 0 events after prune, got %d", len(remaining))
	}
}
