package sqlite_test

import (
	"context"
	"testing"
	"time"
)

// TestGTDStore_LogActivity_RoundTrip verifies that a row inserted via LogActivity
// is returned by ListActivityLogsSince using the normalised ".000Z07:00" timestamp
// format — regression guard for the RFC3339Nano vs .000 lexicographic ordering bug.
func TestGTDStore_LogActivity_RoundTrip(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	before := time.Now().Add(-time.Second)

	const testAction = "test_action"
	const testNotes = "notes with control \x00 and ANSI \x1b[2J stripped"
	const wantNotes = "notes with control  and ANSI  stripped"

	if err := s.LogActivity(ctx, "test-actor", testAction, nil, testNotes); err != nil {
		t.Fatalf("LogActivity: %v", err)
	}

	logs, err := s.ListActivityLogsSince(ctx, before, 10)
	if err != nil {
		t.Fatalf("ListActivityLogsSince: %v", err)
	}

	var found bool
	for _, l := range logs {
		if l.Action == testAction {
			found = true
			if l.Notes.String != wantNotes {
				t.Errorf("Notes field: got %q, want %q", l.Notes.String, wantNotes)
			}
			break
		}
	}
	if !found {
		t.Errorf("LogActivity row not returned by ListActivityLogsSince — timestamp format mismatch? rows=%+v", logs)
	}
}
